package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// This is the repository's canonical HTTPS origin. Local fixtures may set
	// PIGAI_BASE_URL explicitly to a loopback HTTP URL.
	defaultBaseURL     = "https://www.pigsec.cn"
	apiPrefix          = "/openapi/v1/ai"
	defaultHTTPTimeout = 60 * time.Second
	maxJSONBodyBytes   = 8 << 20
	// The backend accepts image references up to 12 MiB (and masks up to
	// 4 MiB). Base64 and multipart overhead make a 2 MiB MCP frame too small
	// for a supported inline reference, so keep a bounded 32 MiB envelope.
	maxToolRequestBytes     = 32 << 20
	maxInlineReferenceBytes = 12 << 20
	maxInlineMaskBytes      = 4 << 20
	maxDownloadBytes        = 1 << 30
	minPromptRunes          = 2
	maxPromptRunes          = 10000
	maxReferenceURLBytes    = 4096
	maxImageReferences      = 4
	maxReferenceSlot        = 3
	maxResourceIDLength     = 64
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// Config contains only client configuration. APIKey is never included in
// tool output or logs.
type Config struct {
	BaseURL            string
	APIKey             string
	HTTPClient         *http.Client
	DownloadDir        string
	MaxDownloadBytes   int64
	AllowCustomBaseURL bool
}

// Client is a thin HTTP adapter. It deliberately contains no provider,
// generation, or billing logic; those remain server-side.
type Client struct {
	baseURL          string
	apiKey           string
	httpClient       *http.Client
	downloadDir      string
	maxDownloadBytes int64
}

type APIError struct {
	HTTPStatus  int
	Code        string
	Message     string
	RequestID   string
	RetryAfter  string
	Details     json.RawMessage
	ResponseRaw json.RawMessage
}

func (e *APIError) Error() string {
	if e == nil {
		return "pig ai api error"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.HTTPStatus)
	}
	if e.Code != "" {
		msg = e.Code + ": " + msg
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("pig ai api error (%d): %s", e.HTTPStatus, redactString(msg, ""))
	}
	return "pig ai api error: " + redactString(msg, "")
}

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid tool input"
	}
	if e.Field == "" {
		return "invalid tool input: " + e.Reason
	}
	return "invalid tool input (" + e.Field + "): " + e.Reason
}

type DownloadMetadata struct {
	Path         string `json:"path"`
	MIMEType     string `json:"mime_type,omitempty"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	OperationID  string `json:"operation_id"`
	ResultID     string `json:"result_id"`
	ContentRange string `json:"content_range,omitempty"`
}

type ReferenceInput struct {
	Role          string `json:"role,omitempty"`
	SourceType    string `json:"source_type"` // url or file
	URL           string `json:"url,omitempty"`
	Slot          int    `json:"slot,omitempty"`
	Name          string `json:"name,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`

	// slotSet preserves the distinction between an omitted slot and an
	// explicitly supplied zero. It is populated while decoding tool JSON and
	// intentionally stays out of the public wire shape.
	slotSet bool
}

type ImageInput struct {
	ProviderID     string           `json:"provider_id,omitempty"`
	Model          string           `json:"model,omitempty"`
	Mode           string           `json:"mode,omitempty"`
	Prompt         string           `json:"prompt"`
	Size           string           `json:"size,omitempty"`
	Quality        string           `json:"quality,omitempty"`
	N              int              `json:"n,omitempty"`
	References     []ReferenceInput `json:"references,omitempty"`
	Mask           *ReferenceInput  `json:"mask,omitempty"`
	IdempotencyKey string           `json:"-"`
	nSet           bool             `json:"-"`
}

type VideoInput struct {
	ProviderID     string          `json:"provider_id,omitempty"`
	Model          string          `json:"model,omitempty"`
	Prompt         string          `json:"prompt"`
	AspectRatio    string          `json:"aspect_ratio,omitempty"`
	Resolution     string          `json:"resolution,omitempty"`
	Duration       int             `json:"duration,omitempty"`
	FirstFrame     *ReferenceInput `json:"first_frame,omitempty"`
	IdempotencyKey string          `json:"-"`
}

// UnmarshalJSON records whether slot was present so callers can distinguish an
// omitted slot (which is assigned the array index) from an explicit slot: 0.
func (r *ReferenceInput) UnmarshalJSON(data []byte) error {
	type referenceAlias ReferenceInput
	var decoded referenceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = ReferenceInput(decoded)
	_, r.slotSet = fields["slot"]
	if raw, ok := fields["slot"]; ok && string(raw) == "null" {
		r.slotSet = false
	}
	return nil
}

type MCPError struct {
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	HTTPStatus int             `json:"http_status,omitempty"`
	RetryAfter string          `json:"retry_after,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

func (m MCPError) Error() string { return m.Code + ": " + m.Message }

func defaultDownloadDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	if !filepath.IsAbs(cacheDir) {
		return "", fmt.Errorf("user cache directory must be absolute")
	}
	return filepath.Join(cacheDir, "pig-ai-mcp", "downloads"), nil
}

func ensurePrivateDownloadDir(dir string) error {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || !filepath.IsAbs(clean) {
		return fmt.Errorf("download directory must be absolute")
	}
	volumeRoot := filepath.VolumeName(clean) + string(filepath.Separator)
	if clean == volumeRoot {
		return fmt.Errorf("download directory must not be a filesystem root")
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("inspect download directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("download directory must not be a symbolic link")
	}
	if !info.IsDir() {
		return fmt.Errorf("download path is not a directory")
	}

	handle, err := os.Open(clean)
	if err != nil {
		return fmt.Errorf("open download directory: %w", err)
	}
	defer handle.Close()
	openedInfo, err := handle.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened download directory: %w", err)
	}
	if !openedInfo.IsDir() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("download directory changed while it was being checked")
	}
	if err := handle.Chmod(0o700); err != nil {
		return fmt.Errorf("secure download directory permissions: %w", err)
	}

	currentInfo, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("reinspect download directory: %w", err)
	}
	if currentInfo.Mode()&os.ModeSymlink != 0 || !currentInfo.IsDir() || !os.SameFile(openedInfo, currentInfo) {
		return fmt.Errorf("download directory changed while it was being secured")
	}
	return nil
}
