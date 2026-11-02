package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPStdioHelper is executed in a child test process by
// TestMCPStdioLifecycle. Keeping the helper in the test binary lets the smoke
// test exercise the official SDK stdio transport without building or invoking
// a provider-facing binary.
func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("PIGAI_MCP_STDIO_HELPER") != "1" {
		return
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := runStdio(context.Background(), client); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestMCPStdioLifecycleAndToolDispatch(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer fixture.Close()

	// Do not pass -test.v: the Go test runner writes its progress to stdout,
	// which must remain exclusively MCP JSON frames in the child process.
	cmd := exec.Command(os.Args[0], "-test.run=TestMCPStdioHelper")
	cmd.Env = scrubPigAIEnv(os.Environ(), map[string]string{
		"PIGAI_MCP_STDIO_HELPER":      "1",
		"PIGAI_BASE_URL":              fixture.URL,
		"PIGAI_ALLOW_CUSTOM_BASE_URL": "true",
		"PIGAI_API_KEY":               "TEST_PIGAI_KEY_STDIO",
		"PIGAI_DOWNLOAD_DIR":          t.TempDir(),
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "pig-ai-test-client", Version: "0.1.0"}, nil)
	// Pin the classic initialize handshake so the smoke test proves the
	// stdio lifecycle expected by hosts that have not adopted server/discover.
	session, err := mcpClient.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatal(err)
	}
	initializeResult := session.InitializeResult()
	if initializeResult == nil || initializeResult.ServerInfo == nil || initializeResult.ServerInfo.Version != serverVersion {
		t.Fatalf("server info = %#v, want version %q", initializeResult, serverVersion)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 8 {
		t.Fatalf("tools count = %d, want 8", len(tools.Tools))
	}
	for _, required := range []string{"list_models", "preview_billing", "create_image", "create_video", "get_operation", "download_result", "ensure_segments", "get_segments"} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing tool %q", required)
		}
	}
	for _, tool := range tools.Tools {
		if tool.Name == "create_image" {
			rawBytes, _ := json.Marshal(tool.InputSchema)
			raw := string(rawBytes)
			if strings.Contains(raw, "aspect_ratio") {
				t.Fatalf("image schema advertises unsupported fields: %s", raw)
			}
		}
		if tool.Name == "create_video" {
			rawBytes, _ := json.Marshal(tool.InputSchema)
			raw := string(rawBytes)
			if strings.Contains(raw, `"mode"`) || strings.Contains(raw, `"references"`) {
				t.Fatalf("video schema advertises unsupported fields: %s", raw)
			}
		}
		if tool.Name == "ensure_segments" {
			rawBytes, _ := json.Marshal(tool.InputSchema)
			if strings.Contains(string(rawBytes), "idempotency_key") {
				t.Fatalf("natural-idempotent segment schema requires an unnecessary key: %s", rawBytes)
			}
		}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_video",
		Arguments: map[string]any{
			"prompt": "stdio invalid-field check", "idempotency_key": "stdio-key-1",
			"mode": "text_to_video",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("invalid tool call result = %#v", result)
	}
	if !strings.Contains(textContent(result), "not supported") {
		t.Fatalf("invalid tool call content = %q", textContent(result))
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Logf("stdio session close: %v", closeErr)
	}
	if strings.Contains(stderr.String(), "TEST_PIGAI_KEY_STDIO") {
		t.Fatalf("API key leaked to child stderr: %s", stderr.String())
	}
}

func TestMCPToolSchemasMatchBackendBounds(t *testing.T) {
	for _, definition := range toolDefinitions() {
		var schema map[string]any
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatalf("tool %q schema: %v", definition.Name, err)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"operation_id", "result_id"} {
			property, ok := properties[field].(map[string]any)
			if !ok {
				continue
			}
			if got := int(property["maxLength"].(float64)); got != maxResourceIDLength {
				t.Errorf("tool %q %s maxLength = %d, want %d", definition.Name, field, got, maxResourceIDLength)
			}
		}
		if definition.Name == "create_image" || definition.Name == "create_video" {
			prompt, ok := properties["prompt"].(map[string]any)
			if !ok {
				t.Fatalf("%s schema is missing prompt", definition.Name)
			}
			if got := int(prompt["minLength"].(float64)); got != minPromptRunes {
				t.Errorf("%s prompt minLength = %d, want %d", definition.Name, got, minPromptRunes)
			}
			if got := int(prompt["maxLength"].(float64)); got != maxPromptRunes {
				t.Errorf("%s prompt maxLength = %d, want %d", definition.Name, got, maxPromptRunes)
			}
		}
		if definition.Name != "create_image" {
			if definition.Name == "preview_billing" {
				required, ok := schema["required"].([]any)
				if !ok || !containsString(required, "mode") || !containsString(required, "n") {
					t.Errorf("preview_billing required = %#v, want mode and n", schema["required"])
				}
			}
			continue
		}
		required, ok := schema["required"].([]any)
		if !ok || !containsString(required, "mode") || !containsString(required, "n") {
			t.Errorf("create_image required = %#v, want mode and n", schema["required"])
		}
		references, ok := properties["references"].(map[string]any)
		if !ok {
			t.Fatal("create_image schema is missing references")
		}
		if got := int(references["maxItems"].(float64)); got != maxImageReferences {
			t.Errorf("references maxItems = %d, want %d", got, maxImageReferences)
		}
		items, ok := references["items"].(map[string]any)
		if !ok {
			t.Fatal("create_image references schema is missing items")
		}
		itemProperties, ok := items["properties"].(map[string]any)
		if !ok {
			t.Fatal("create_image reference item schema is missing properties")
		}
		slot, ok := itemProperties["slot"].(map[string]any)
		if !ok {
			t.Fatal("create_image reference schema is missing slot")
		}
		if got := int(slot["maximum"].(float64)); got != maxReferenceSlot {
			t.Errorf("reference slot maximum = %d, want %d", got, maxReferenceSlot)
		}
	}
}

func containsString(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestSDKValueResultTruncatesAtUTF8Boundary(t *testing.T) {
	value := map[string]any{"value": strings.Repeat("猪", maxTextOutputBytes)}
	result := sdkValueResult(value)
	text := textContent(result)
	if !utf8.ValidString(text) {
		t.Fatal("truncated tool text is not valid UTF-8")
	}
	if !strings.Contains(text, "[truncated;") {
		t.Fatal("oversized tool text was not marked as truncated")
	}
}

func textContent(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var builder strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			builder.WriteString(text.Text)
		}
	}
	return builder.String()
}

func scrubPigAIEnv(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		if strings.HasPrefix(entry, "PIGAI_") {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}
