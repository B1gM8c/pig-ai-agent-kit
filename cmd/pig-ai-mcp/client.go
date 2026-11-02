package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func LoadConfig() (Config, error) {
	allowCustomBaseURL, err := explicitBooleanEnv("PIGAI_ALLOW_CUSTOM_BASE_URL")
	if err != nil {
		return Config{}, err
	}
	baseURL := strings.TrimSpace(os.Getenv("PIGAI_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if err := validateBaseURL(baseURL, allowCustomBaseURL); err != nil {
		return Config{}, err
	}

	timeout := defaultHTTPTimeout
	if raw := strings.TrimSpace(os.Getenv("PIGAI_HTTP_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 || d > 10*time.Minute {
			return Config{}, fmt.Errorf("PIGAI_HTTP_TIMEOUT must be a positive duration no longer than 10m")
		}
		timeout = d
	}

	maxDownload := int64(maxDownloadBytes)
	if raw := strings.TrimSpace(os.Getenv("PIGAI_MAX_DOWNLOAD_BYTES")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 || n > 8<<30 {
			return Config{}, fmt.Errorf("PIGAI_MAX_DOWNLOAD_BYTES must be between 1 and 8589934592")
		}
		maxDownload = n
	}

	downloadDir := strings.TrimSpace(os.Getenv("PIGAI_DOWNLOAD_DIR"))
	if downloadDir == "" {
		var err error
		downloadDir, err = defaultDownloadDir()
		if err != nil {
			return Config{}, err
		}
	}
	if !filepath.IsAbs(downloadDir) {
		return Config{}, fmt.Errorf("PIGAI_DOWNLOAD_DIR must be an absolute path")
	}

	return Config{
		BaseURL:            baseURL,
		APIKey:             strings.TrimSpace(os.Getenv("PIGAI_API_KEY")),
		HTTPClient:         &http.Client{Timeout: timeout},
		DownloadDir:        filepath.Clean(downloadDir),
		MaxDownloadBytes:   maxDownload,
		AllowCustomBaseURL: allowCustomBaseURL,
	}, nil
}

func NewClient(cfg Config) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if err := validateBaseURL(baseURL, cfg.AllowCustomBaseURL); err != nil {
		return nil, err
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	httpClient, err := withSafeRedirectPolicy(baseURL, httpClient)
	if err != nil {
		return nil, err
	}
	maxDownload := cfg.MaxDownloadBytes
	if maxDownload <= 0 {
		maxDownload = maxDownloadBytes
	}
	downloadDir := cfg.DownloadDir
	if downloadDir == "" {
		var err error
		downloadDir, err = defaultDownloadDir()
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(downloadDir) {
		return nil, fmt.Errorf("download directory must be absolute")
	}
	return &Client{
		baseURL:          strings.TrimRight(baseURL, "/"),
		apiKey:           strings.TrimSpace(cfg.APIKey),
		httpClient:       httpClient,
		downloadDir:      filepath.Clean(downloadDir),
		maxDownloadBytes: maxDownload,
	}, nil
}

// withSafeRedirectPolicy clones the caller's client before installing the
// API-origin redirect guard. The standard library otherwise follows redirects
// to arbitrary origins, which can send an API key to an unintended host.
// Pig AI requests are fail-closed: only same-scheme, same-host, same-port
// redirects are followed. In particular, HTTPS can never downgrade to HTTP.
func withSafeRedirectPolicy(baseURL string, configured *http.Client) (*http.Client, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil {
		return nil, fmt.Errorf("invalid API base URL for redirect policy")
	}
	client := &http.Client{Timeout: defaultHTTPTimeout}
	if configured != nil {
		clone := *configured
		client = &clone
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request == nil || request.URL == nil {
			return errors.New("redirect blocked: invalid target")
		}
		if err := validateRedirectTarget(base, request.URL); err != nil {
			return err
		}
		// Keep net/http's default hard limit even though installing this
		// policy replaces the client's nil CheckRedirect behavior.
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if previous != nil {
			return previous(request, via)
		}
		return nil
	}
	return client, nil
}

func validateRedirectTarget(base, target *url.URL) error {
	if base == nil || target == nil || target.Scheme == "" || target.Host == "" || target.User != nil {
		return errors.New("redirect blocked: invalid target")
	}
	if strings.EqualFold(base.Scheme, "https") && !strings.EqualFold(target.Scheme, "https") {
		return errors.New("redirect blocked: HTTPS cannot downgrade to HTTP")
	}
	if !sameURLOrigin(base, target) {
		return errors.New("redirect blocked: target origin is outside the configured API origin")
	}
	return nil
}

func sameURLOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(strings.TrimSuffix(left.Hostname(), "."), strings.TrimSuffix(right.Hostname(), ".")) &&
		effectiveURLPort(left) == effectiveURLPort(right)
}

func effectiveURLPort(value *url.URL) string {
	if value == nil {
		return ""
	}
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func (c *Client) requireAPIKey() error {
	if strings.TrimSpace(c.apiKey) == "" {
		return &MCPError{Code: "configuration_missing", Message: "PIGAI_API_KEY is required for this tool"}
	}
	if strings.ContainsAny(c.apiKey, "\r\n") {
		return &MCPError{Code: "configuration_invalid", Message: "PIGAI_API_KEY contains invalid characters"}
	}
	return nil
}

func explicitBooleanEnv(name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func validateBaseURL(raw string, allowCustom bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("PIGAI_BASE_URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if isOfficialBaseURL(u) {
		return nil
	}
	if !allowCustom {
		return fmt.Errorf("custom PIGAI_BASE_URL requires PIGAI_ALLOW_CUSTOM_BASE_URL=true")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("PIGAI_BASE_URL must use HTTPS (HTTP is allowed only for loopback fixtures)")
}

func isOfficialBaseURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https") &&
		strings.EqualFold(u.Host, "www.pigsec.cn") &&
		(u.EscapedPath() == "" || u.EscapedPath() == "/")
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) endpoint(parts ...string) (string, error) {
	if c == nil || c.baseURL == "" {
		return "", fmt.Errorf("client is not configured")
	}
	joined := apiPrefix
	for _, part := range parts {
		if part == "" {
			continue
		}
		joined = path.Join(joined, part)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid API base URL")
	}
	// Preserve an explicit base path while keeping the endpoint path beneath it.
	base.Path = path.Join(strings.TrimSuffix(base.Path, "/"), joined)
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func (c *Client) doJSON(ctx context.Context, method string, endpoint string, body any, idempotencyKey string) (json.RawMessage, error) {
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		if len(payload) > maxToolRequestBytes {
			return nil, &ValidationError{Reason: "request body is too large"}
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-API-Key", c.apiKey)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, redactError(err, c.apiKey)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, parseAPIError(response, c.apiKey)
	}
	limited := io.LimitReader(response.Body, maxJSONBodyBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, redactError(err, c.apiKey)
	}
	if len(payload) > maxJSONBodyBytes {
		return nil, &APIError{HTTPStatus: response.StatusCode, Code: "response_too_large", Message: "API response exceeds JSON limit"}
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(payload) {
		return nil, &APIError{HTTPStatus: response.StatusCode, Code: "invalid_response", Message: "API returned invalid JSON"}
	}
	return unwrapData(payload), nil
}

func unwrapData(payload []byte) json.RawMessage {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(payload, &envelope) == nil {
		if data, ok := envelope["data"]; ok && len(bytes.TrimSpace(data)) > 0 {
			return json.RawMessage(data)
		}
	}
	return json.RawMessage(payload)
}

func (c *Client) doMultipart(ctx context.Context, endpoint string, fields map[string]string, files []multipartFile, idempotencyKey string) (json.RawMessage, error) {
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("encode multipart field: %w", err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.Field, safeFilename(file.Name))
		if err != nil {
			return nil, fmt.Errorf("encode multipart file: %w", err)
		}
		if _, err := part.Write(file.Data); err != nil {
			return nil, fmt.Errorf("encode multipart file: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart body: %w", err)
	}
	if int64(buffer.Len()) > maxToolRequestBytes {
		return nil, &ValidationError{Reason: "multipart request body is too large"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buffer)
	if err != nil {
		return nil, fmt.Errorf("build multipart request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-API-Key", c.apiKey)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, redactError(err, c.apiKey)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, parseAPIError(response, c.apiKey)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBodyBytes+1))
	if err != nil {
		return nil, redactError(err, c.apiKey)
	}
	if len(payload) > maxJSONBodyBytes || !json.Valid(payload) {
		return nil, &APIError{HTTPStatus: response.StatusCode, Code: "invalid_response", Message: "API returned an invalid or oversized JSON response"}
	}
	return unwrapData(payload), nil
}

type multipartFile struct {
	Field string
	Name  string
	Data  []byte
}

func (c *Client) ListModels(ctx context.Context) (json.RawMessage, error) {
	endpoint, err := c.endpoint("models")
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, endpoint, nil, "")
}

func (c *Client) PreviewBilling(ctx context.Context, input map[string]any) (json.RawMessage, error) {
	endpoint, err := c.endpoint("billing-preview")
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodPost, endpoint, input, "")
}

func (c *Client) CreateImage(ctx context.Context, input ImageInput) (json.RawMessage, error) {
	if err := validateImageInput(input); err != nil {
		return nil, err
	}
	endpoint, err := c.endpoint("images")
	if err != nil {
		return nil, err
	}
	if hasInlineReference(input.References) || hasInlineReferencePtr(input.Mask) {
		fields, files, err := imageMultipart(input)
		if err != nil {
			return nil, err
		}
		return c.doMultipart(ctx, endpoint, fields, files, input.IdempotencyKey)
	}
	payload := imageRequestPayload(input)
	return c.doJSON(ctx, http.MethodPost, endpoint, compactMap(payload), input.IdempotencyKey)
}

func (c *Client) CreateVideo(ctx context.Context, input VideoInput) (json.RawMessage, error) {
	if err := validateVideoInput(input); err != nil {
		return nil, err
	}
	endpoint, err := c.endpoint("videos")
	if err != nil {
		return nil, err
	}
	if hasInlineReferencePtr(input.FirstFrame) || hasInlineReferencePtr(input.LastFrame) || hasInlineReference(input.ReferenceImages) || hasInlineVideoKeyframe(input.Keyframes) {
		fields, files, err := videoMultipart(input)
		if err != nil {
			return nil, err
		}
		return c.doMultipart(ctx, endpoint, fields, files, input.IdempotencyKey)
	}
	payload := videoRequestPayload(input)
	return c.doJSON(ctx, http.MethodPost, endpoint, compactMap(payload), input.IdempotencyKey)
}

func compactMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				continue
			}
		case int:
			if v == 0 {
				continue
			}
		case nil:
			continue
		}
		output[key] = value
	}
	return output
}

func imageMultipart(input ImageInput) (map[string]string, []multipartFile, error) {
	request, err := json.Marshal(compactMap(imageRequestPayload(input)))
	if err != nil {
		return nil, nil, fmt.Errorf("encode image request: %w", err)
	}
	files := make([]multipartFile, 0, len(input.References)+1)
	for _, reference := range input.References {
		if reference.SourceType != "file" {
			continue
		}
		data, decodeErr := decodeReferenceWithLimit(reference, maxInlineReferenceBytes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		files = append(files, multipartFile{
			Field: fmt.Sprintf("reference_%d", reference.Slot),
			Name:  reference.NameOrDefault(reference.Slot),
			Data:  data,
		})
	}
	if input.Mask != nil && input.Mask.SourceType == "file" {
		data, decodeErr := decodeReferenceWithLimit(*input.Mask, maxInlineMaskBytes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		files = append(files, multipartFile{Field: "mask", Name: input.Mask.NameOrDefault(0), Data: data})
	}
	return map[string]string{"request": string(request)}, files, nil
}

func videoMultipart(input VideoInput) (map[string]string, []multipartFile, error) {
	request, err := json.Marshal(compactMap(videoRequestPayload(input)))
	if err != nil {
		return nil, nil, fmt.Errorf("encode video request: %w", err)
	}
	files := make([]multipartFile, 0, 2+len(input.ReferenceImages)+len(input.Keyframes))
	if input.FirstFrame != nil {
		if input.FirstFrame.SourceType == "file" {
			data, decodeErr := decodeReferenceWithLimit(*input.FirstFrame, maxInlineReferenceBytes)
			if decodeErr != nil {
				return nil, nil, decodeErr
			}
			files = append(files, multipartFile{Field: "first_frame", Name: input.FirstFrame.NameOrDefault(0), Data: data})
		}
	}
	if input.LastFrame != nil && input.LastFrame.SourceType == "file" {
		data, decodeErr := decodeReferenceWithLimit(*input.LastFrame, maxInlineReferenceBytes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		files = append(files, multipartFile{Field: "last_frame", Name: input.LastFrame.NameOrDefault(0), Data: data})
	}
	for _, reference := range input.ReferenceImages {
		if reference.SourceType != "file" {
			continue
		}
		data, decodeErr := decodeReferenceWithLimit(reference, maxInlineReferenceBytes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		files = append(files, multipartFile{Field: fmt.Sprintf("reference_%d", reference.Slot), Name: reference.NameOrDefault(reference.Slot), Data: data})
	}
	for index, keyframe := range input.Keyframes {
		if keyframe.Image.SourceType != "file" {
			continue
		}
		data, decodeErr := decodeReferenceWithLimit(keyframe.Image, maxInlineReferenceBytes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		files = append(files, multipartFile{Field: fmt.Sprintf("keyframe_%d", index), Name: keyframe.Image.NameOrDefault(index), Data: data})
	}
	return map[string]string{"request": string(request)}, files, nil
}

// referenceManifest is the public ordered media manifest. File bytes and
// local-only metadata never enter the request JSON; they are sent as the
// matching reference_N multipart part instead.
type referenceManifest struct {
	Role       string `json:"role,omitempty"`
	SourceType string `json:"source_type"`
	URL        string `json:"url,omitempty"`
	Slot       int    `json:"slot"`
}

func manifestReference(reference ReferenceInput) referenceManifest {
	return referenceManifest{
		Role: reference.Role, SourceType: reference.SourceType,
		URL: reference.URL, Slot: reference.Slot,
	}
}

func imageRequestPayload(input ImageInput) map[string]any {
	payload := map[string]any{
		"quote_id":    input.QuoteID,
		"workflow":    input.Workflow,
		"resolution":  input.Resolution,
		"width":       input.Width,
		"height":      input.Height,
		"provider_id": input.ProviderID,
		"model":       input.Model,
		"mode":        input.Mode,
		"prompt":      input.Prompt,
		"size":        input.Size,
		"quality":     input.Quality,
		"n":           input.N,
	}
	if len(input.References) > 0 {
		refs := make([]referenceManifest, 0, len(input.References))
		for _, reference := range input.References {
			refs = append(refs, manifestReference(reference))
		}
		payload["references"] = refs
	}
	if input.Mask != nil {
		payload["mask"] = manifestReference(*input.Mask)
	}
	return payload
}

func videoRequestPayload(input VideoInput) map[string]any {
	payload := map[string]any{
		"quote_id":     input.QuoteID,
		"workflow":     input.Workflow,
		"provider_id":  input.ProviderID,
		"model":        input.Model,
		"prompt":       input.Prompt,
		"aspect_ratio": input.AspectRatio,
		"resolution":   input.Resolution,
		"duration":     input.Duration,
	}
	if len(input.ReferenceVideoURLs) > 0 {
		payload["reference_video_urls"] = input.ReferenceVideoURLs
	}
	if len(input.ReferenceAudioURLs) > 0 {
		payload["reference_audio_urls"] = input.ReferenceAudioURLs
	}
	if len(input.Transitions) > 0 {
		payload["transitions"] = input.Transitions
	}
	if input.FirstFrame != nil {
		payload["first_frame"] = manifestReference(*input.FirstFrame)
	}
	if input.LastFrame != nil {
		payload["last_frame"] = manifestReference(*input.LastFrame)
	}
	if len(input.ReferenceImages) > 0 {
		references := make([]referenceManifest, 0, len(input.ReferenceImages))
		for _, reference := range input.ReferenceImages {
			references = append(references, manifestReference(reference))
		}
		payload["reference_images"] = references
	}
	if len(input.Keyframes) > 0 {
		keyframes := make([]map[string]any, 0, len(input.Keyframes))
		for _, keyframe := range input.Keyframes {
			keyframes = append(keyframes, map[string]any{"image": manifestReference(keyframe.Image), "timestamp_s": keyframe.TimestampS})
		}
		payload["keyframes"] = keyframes
	}
	if len(input.ReferenceAudios) > 0 {
		payload["reference_audios"] = input.ReferenceAudios
	}
	return payload
}

func (r ReferenceInput) NameOrDefault(index int) string {
	if strings.TrimSpace(r.Name) != "" {
		return safeFilename(r.Name)
	}
	ext := ".bin"
	if mt := strings.TrimSpace(r.ContentType); mt != "" {
		if suffixes, _ := mime.ExtensionsByType(mt); len(suffixes) > 0 {
			ext = suffixes[0]
		}
	}
	return fmt.Sprintf("reference-%d%s", index, ext)
}

func decodeReferenceWithLimit(reference ReferenceInput, maxBytes int) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(reference.ContentBase64)
	if err != nil {
		return nil, &ValidationError{Field: "references.content_base64", Reason: "must be valid standard base64"}
	}
	if len(data) == 0 {
		return nil, &ValidationError{Field: "references.content_base64", Reason: "must not be empty"}
	}
	if maxBytes <= 0 {
		maxBytes = maxInlineReferenceBytes
	}
	if len(data) > maxBytes {
		return nil, &ValidationError{Field: "references.content_base64", Reason: "decoded content is too large"}
	}
	return data, nil
}

func safeFilename(raw string) string {
	name := filepath.Base(strings.TrimSpace(raw))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' || strings.ContainsRune("<>:\"|?*", r) {
			return '-'
		}
		return r
	}, name)
	// Windows strips trailing spaces and periods from a filename. Do the same
	// before checking for reserved device names so that inputs such as
	// "CON. " cannot resolve to a device path on Windows.
	name = strings.TrimRight(name, " .")
	if name == "" || name == "." || name == ".." {
		return "result.bin"
	}
	name = truncateFilename(name, 128)
	if isWindowsReservedDeviceName(name) {
		// Prefixing keeps the caller-visible extension while making the name
		// safe on Windows. Reserve one byte of the filename limit for it.
		name = "_" + truncateFilename(name, 127)
	}
	return name
}

func truncateFilename(name string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(name) <= maxBytes {
		return name
	}
	ext := filepath.Ext(name)
	if ext != "" && len(ext) < maxBytes {
		stem := strings.TrimSuffix(name, ext)
		stem = truncateUTF8Prefix(stem, maxBytes-len(ext))
		return stem + ext
	}
	return truncateUTF8Prefix(name, maxBytes)
}

func truncateUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func isWindowsReservedDeviceName(name string) bool {
	name = strings.TrimRight(name, " .")
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		name = name[:dot]
	}
	name = strings.ToUpper(name)
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(name) != 4 {
		return false
	}
	prefix := name[:3]
	return (prefix == "COM" || prefix == "LPT") && name[3] >= '1' && name[3] <= '9'
}

func (c *Client) GetOperation(ctx context.Context, operationID string) (json.RawMessage, error) {
	if err := validateID(operationID, "operation_id"); err != nil {
		return nil, err
	}
	endpoint, err := c.endpoint("operations", url.PathEscape(operationID))
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, endpoint, nil, "")
}

func (c *Client) EnsureSegments(ctx context.Context, operationID, resultID string) (json.RawMessage, error) {
	if err := validateID(operationID, "operation_id"); err != nil {
		return nil, err
	}
	if err := validateID(resultID, "result_id"); err != nil {
		return nil, err
	}
	endpoint, err := c.endpoint("operations", url.PathEscape(operationID), "results", url.PathEscape(resultID), "segments", "ensure")
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodPost, endpoint, map[string]any{}, "")
}

func (c *Client) GetSegments(ctx context.Context, operationID, resultID string) (json.RawMessage, error) {
	if err := validateID(operationID, "operation_id"); err != nil {
		return nil, err
	}
	if err := validateID(resultID, "result_id"); err != nil {
		return nil, err
	}
	endpoint, err := c.endpoint("operations", url.PathEscape(operationID), "results", url.PathEscape(resultID), "segments")
	if err != nil {
		return nil, err
	}
	return c.doJSON(ctx, http.MethodGet, endpoint, nil, "")
}

func (c *Client) DownloadResult(ctx context.Context, operationID, resultID string) (DownloadMetadata, error) {
	if err := validateID(operationID, "operation_id"); err != nil {
		return DownloadMetadata{}, err
	}
	if err := validateID(resultID, "result_id"); err != nil {
		return DownloadMetadata{}, err
	}
	if err := c.requireAPIKey(); err != nil {
		return DownloadMetadata{}, err
	}
	if err := ensurePrivateDownloadDir(c.downloadDir); err != nil {
		return DownloadMetadata{}, err
	}
	endpoint, err := c.endpoint("operations", url.PathEscape(operationID), "results", url.PathEscape(resultID), "content")
	if err != nil {
		return DownloadMetadata{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return DownloadMetadata{}, fmt.Errorf("build download request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream, video/*, image/*")
	request.Header.Set("X-API-Key", c.apiKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return DownloadMetadata{}, redactError(err, c.apiKey)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return DownloadMetadata{}, parseAPIError(response, c.apiKey)
	}
	tmp, err := os.CreateTemp(c.downloadDir, ".pig-ai-*")
	if err != nil {
		return DownloadMetadata{}, fmt.Errorf("create temporary download: %w", err)
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	limited := io.LimitReader(response.Body, c.maxDownloadBytes+1)
	bytesWritten, err := io.Copy(io.MultiWriter(tmp, hash), limited)
	if err != nil {
		return DownloadMetadata{}, redactError(err, c.apiKey)
	}
	if bytesWritten > c.maxDownloadBytes {
		return DownloadMetadata{}, &APIError{HTTPStatus: response.StatusCode, Code: "download_too_large", Message: "result exceeds configured download limit"}
	}
	if err := tmp.Sync(); err != nil {
		return DownloadMetadata{}, redactError(err, c.apiKey)
	}
	if err := tmp.Close(); err != nil {
		return DownloadMetadata{}, redactError(err, c.apiKey)
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if parsed, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		contentType = parsed
	}
	filename := ""
	if disposition := strings.TrimSpace(response.Header.Get("Content-Disposition")); disposition != "" {
		if _, params, parseErr := mime.ParseMediaType(disposition); parseErr == nil {
			filename = params["filename"]
			if filename == "" {
				filename = params["filename*"]
			}
		}
	}
	filename = safeFilename(filename)
	if filename == "result.bin" {
		filename = safeFilename(operationID + "-" + resultID + extensionForMIME(contentType))
	}
	finalPath := filepath.Join(c.downloadDir, filename)
	if finalPath == filepath.Clean(c.downloadDir) || !isWithinDir(c.downloadDir, finalPath) {
		return DownloadMetadata{}, fmt.Errorf("invalid download filename")
	}
	finalPath, err = commitDownloadNoClobber(tmpName, c.downloadDir, filename)
	if err != nil {
		return DownloadMetadata{}, redactError(err, c.apiKey)
	}
	keep = true
	return DownloadMetadata{
		Path:         finalPath,
		MIMEType:     contentType,
		Bytes:        bytesWritten,
		SHA256:       fmt.Sprintf("%x", hash.Sum(nil)),
		OperationID:  operationID,
		ResultID:     resultID,
		ContentRange: strings.TrimSpace(response.Header.Get("Content-Range")),
	}, nil
}

func extensionForMIME(contentType string) string {
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func isWithinDir(dir, candidate string) bool {
	dir = filepath.Clean(dir)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(dir, candidate)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// commitDownloadNoClobber publishes a completed temporary file without ever
// replacing an existing result. os.Link is an atomic same-directory create on
// the filesystems supported by the client; callers can retry a suffixed name
// when another download wins the race.
func commitDownloadNoClobber(tmpName, dir, filename string) (string, error) {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	for index := 0; index < 10000; index++ {
		candidateName := filename
		if index > 0 {
			candidateName = fmt.Sprintf("%s-%d%s", stem, index, ext)
		}
		candidate := filepath.Join(dir, candidateName)
		if err := os.Link(tmpName, candidate); err == nil {
			if err := os.Remove(tmpName); err != nil {
				_ = os.Remove(candidate)
				return "", fmt.Errorf("remove temporary download: %w", err)
			}
			return candidate, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("unable to allocate a unique download filename")
}

func parseAPIError(response *http.Response, secret string) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	apiErr := &APIError{HTTPStatus: response.StatusCode, RetryAfter: strings.TrimSpace(response.Header.Get("Retry-After"))}
	apiErr.ResponseRaw = json.RawMessage(redactBytes(body, secret))
	var envelope struct {
		Code      any             `json:"code"`
		Message   string          `json:"message"`
		Error     json.RawMessage `json:"error"`
		RequestID string          `json:"request_id"`
		Details   json.RawMessage `json:"details"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		apiErr.Message = redactString(strings.TrimSpace(envelope.Message), secret)
		apiErr.RequestID = strings.TrimSpace(envelope.RequestID)
		apiErr.Details = json.RawMessage(redactBytes(envelope.Details, secret))
		if envelope.Code != nil {
			apiErr.Code = fmt.Sprint(envelope.Code)
		}
		if len(envelope.Error) > 0 {
			var nested struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Details json.RawMessage `json:"details"`
			}
			if json.Unmarshal(envelope.Error, &nested) == nil {
				if apiErr.Code == "" {
					apiErr.Code = nested.Code
				}
				if apiErr.Message == "" {
					apiErr.Message = redactString(nested.Message, secret)
				}
				if len(apiErr.Details) == 0 {
					apiErr.Details = json.RawMessage(redactBytes(nested.Details, secret))
				}
			}
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(response.StatusCode)
	}
	apiErr.Code = strings.TrimSpace(apiErr.Code)
	if apiErr.Code == "" {
		switch response.StatusCode {
		case http.StatusTooManyRequests:
			apiErr.Code = "rate_limited"
		case http.StatusConflict:
			apiErr.Code = "idempotency_conflict"
		default:
			apiErr.Code = "api_error"
		}
	}
	return apiErr
}

func redactError(err error, secret string) error {
	if err == nil {
		return nil
	}
	return errors.New(redactString(err.Error(), secret))
}

func redactBytes(raw []byte, secret string) []byte {
	return []byte(redactString(string(raw), secret))
}

func hasInlineVideoKeyframe(keyframes []VideoKeyframeInput) bool {
	for _, keyframe := range keyframes {
		if strings.TrimSpace(keyframe.Image.ContentBase64) != "" {
			return true
		}
	}
	return false
}
