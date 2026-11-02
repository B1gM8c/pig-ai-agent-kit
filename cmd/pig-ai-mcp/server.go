package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName         = "pig-ai"
	maxTextOutputBytes = 512 << 10
)

// serverVersion is set from the release tag with
// -ldflags "-X main.serverVersion=<tag>". Local builds intentionally report dev.
var serverVersion = "dev"

// newMCPServer uses the official MCP Go SDK for lifecycle, tool dispatch, and
// stdio framing. The handlers below are intentionally thin HTTP adapters.
func newMCPServer(client *Client) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, &mcp.ServerOptions{
		Instructions: "Pig AI tools call the authenticated OpenAPI. Generation is asynchronous and idempotent.",
	})
	for _, definition := range toolDefinitions() {
		definition := definition
		server.AddTool(&mcp.Tool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: definition.InputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return invokeSDKTool(ctx, definition.Name, request.Params.Arguments, client)
		})
	}
	return server
}

func runStdio(ctx context.Context, client *Client) error {
	server := newMCPServer(client)
	return server.Run(ctx, &mcp.StdioTransport{MaxLineLength: maxToolRequestBytes + 1024})
}

func main() {
	config, err := LoadConfig()
	if err != nil {
		// Keep stdout reserved for SDK-managed MCP frames. This message contains
		// no environment values and is safe for a host to display.
		_, _ = fmt.Fprintln(os.Stderr, "pig-ai-mcp configuration error:", redactString(err.Error(), ""))
		os.Exit(2)
	}
	client, err := NewClient(config)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "pig-ai-mcp configuration error:", redactString(err.Error(), ""))
		os.Exit(2)
	}
	if err := runStdio(context.Background(), client); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "pig-ai-mcp stopped:", redactString(err.Error(), client.apiKey))
		os.Exit(1)
	}
}

type toolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "list_models",
			Description: "List enabled Pig AI image/video models and their capabilities. Requires PIGAI_API_KEY.",
			InputSchema: objectSchema(map[string]any{}, nil),
		},
		{
			Name:        "preview_billing",
			Description: "Preview the server-side point charge and free allowance. For Dreamina retail models, pass the returned quote_id with the identical specification to create_image/create_video.",
			InputSchema: objectSchema(map[string]any{
				"provider_id":           map[string]any{"type": "string"},
				"model":                 map[string]any{"type": "string"},
				"mode":                  map[string]any{"type": "string", "enum": []string{"text_to_image", "image_to_image", "text_to_video", "image_to_video", "reference_to_video"}},
				"size":                  map[string]any{"type": "string"},
				"quality":               map[string]any{"type": "string"},
				"workflow":              map[string]any{"type": "string"},
				"resolution":            map[string]any{"type": "string"},
				"aspect_ratio":          map[string]any{"type": "string"},
				"width":                 map[string]any{"type": "integer", "minimum": 0},
				"height":                map[string]any{"type": "integer", "minimum": 0},
				"duration":              map[string]any{"type": "integer", "minimum": 0},
				"reference_image_count": map[string]any{"type": "integer", "minimum": 0, "maximum": 30},
				"reference_video_count": map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
				"reference_audio_count": map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
				"transitions":           transitionsSchema(),
				"n":                     map[string]any{"type": "integer", "minimum": 1, "maximum": 4},
			}, []string{"mode", "n"}),
		},
		{
			Name:        "create_image",
			Description: "Submit an asynchronous Pig AI image generation operation; reuse idempotency_key on retries.",
			InputSchema: objectSchema(map[string]any{
				"prompt":          map[string]any{"type": "string", "minLength": minPromptRunes, "maxLength": maxPromptRunes},
				"idempotency_key": map[string]any{"type": "string", "minLength": 8, "maxLength": 128},
				"provider_id":     map[string]any{"type": "string"},
				"model":           map[string]any{"type": "string"},
				"mode":            map[string]any{"type": "string", "enum": []string{"text_to_image", "image_to_image"}},
				"size":            map[string]any{"type": "string"},
				"quality":         map[string]any{"type": "string"},
				"quote_id":        map[string]any{"type": "string", "maxLength": 64},
				"workflow":        map[string]any{"type": "string"},
				"resolution":      map[string]any{"type": "string"},
				"width":           map[string]any{"type": "integer", "minimum": 0},
				"height":          map[string]any{"type": "integer", "minimum": 0},
				"n":               map[string]any{"type": "integer", "minimum": 1, "maximum": 4},
				"references":      referencesSchema(),
				"mask":            referenceSchema(),
			}, []string{"prompt", "idempotency_key", "mode", "n"}),
		},
		{
			Name:        "create_video",
			Description: "Submit an asynchronous Pig AI video generation operation; reuse idempotency_key on retries.",
			InputSchema: objectSchema(map[string]any{
				"prompt":               map[string]any{"type": "string", "minLength": minPromptRunes, "maxLength": maxPromptRunes},
				"idempotency_key":      map[string]any{"type": "string", "minLength": 8, "maxLength": 128},
				"provider_id":          map[string]any{"type": "string"},
				"model":                map[string]any{"type": "string"},
				"aspect_ratio":         map[string]any{"type": "string"},
				"resolution":           map[string]any{"type": "string"},
				"quote_id":             map[string]any{"type": "string", "maxLength": 64},
				"workflow":             map[string]any{"type": "string"},
				"transitions":          transitionsSchema(),
				"reference_video_urls": map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string", "format": "uri"}},
				"reference_audio_urls": map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string", "format": "uri"}},
				"duration":             map[string]any{"type": "integer", "minimum": 0, "maximum": 3600},
				"first_frame":          referenceSchema(),
				"last_frame":           referenceSchema(),
				"reference_images":     map[string]any{"type": "array", "maxItems": maxVideoReferences, "items": referenceSchemaWithMaxSlot(maxVideoReferenceSlot)},
				"keyframes": map[string]any{
					"type": "array", "maxItems": 4,
					"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"image", "timestamp_s"}, "properties": map[string]any{
						"image": referenceSchemaWithMaxSlot(3), "timestamp_s": map[string]any{"type": "number", "exclusiveMinimum": 0},
					}},
				},
				"reference_audios": map[string]any{
					"type": "array", "maxItems": maxVideoVoices,
					"description": "Preset voice order maps to AUDIO_0, AUDIO_1, and AUDIO_2.",
					"items":       map[string]any{"type": "object", "additionalProperties": false, "required": []string{"voice_id"}, "properties": map[string]any{"voice_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}},
				},
			}, []string{"prompt", "idempotency_key"}),
		},
		{
			Name:        "get_operation",
			Description: "Read operation state, settlement, timings, results, partial completion, or refunds.",
			InputSchema: objectSchema(map[string]any{"operation_id": map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()}}, []string{"operation_id"}),
		},
		{
			Name:        "download_result",
			Description: "Stream one result to a local private file and return safe path and metadata, never binary content.",
			InputSchema: objectSchema(map[string]any{
				"operation_id": map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
				"result_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
			}, []string{"operation_id", "result_id"}),
		},
		{
			Name:        "ensure_segments",
			Description: "Request or resume naturally idempotent persisted bbox semantic segments for an operation result.",
			InputSchema: objectSchema(map[string]any{
				"operation_id": map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
				"result_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
			}, []string{"operation_id", "result_id"}),
		},
		{
			Name:        "get_segments",
			Description: "Read persisted bbox semantic segments and their processing status for an operation result.",
			InputSchema: objectSchema(map[string]any{
				"operation_id": map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
				"result_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": maxResourceIDLength, "pattern": idPattern.String()},
			}, []string{"operation_id", "result_id"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) json.RawMessage {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}

func referenceSchema() map[string]any { return referenceSchemaWithMaxSlot(maxReferenceSlot) }

func referenceSchemaWithMaxSlot(maxSlot int) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role":           map[string]any{"type": "string", "maxLength": 80},
			"source_type":    map[string]any{"type": "string", "enum": []string{"url", "file"}},
			"url":            map[string]any{"type": "string", "format": "uri", "maxLength": maxReferenceURLBytes},
			"slot":           map[string]any{"type": "integer", "minimum": 0, "maximum": maxSlot},
			"name":           map[string]any{"type": "string", "maxLength": 128},
			"content_type":   map[string]any{"type": "string", "maxLength": 128},
			"content_base64": map[string]any{"type": "string", "description": "Optional base64 file content; bounded by the MCP client."},
		},
		"additionalProperties": false,
	}
}

func referencesSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": maxImageReferences, "items": referenceSchema()}
}

func invokeSDKTool(ctx context.Context, name string, rawArgs json.RawMessage, client *Client) (*mcp.CallToolResult, error) {
	args, err := parseArguments(rawArgs)
	if err != nil {
		return sdkErrorResult(err, client.apiKey), nil
	}
	result, err := dispatchTool(ctx, name, args, client)
	if err != nil {
		return sdkErrorResult(err, client.apiKey), nil
	}
	return sdkValueResult(result), nil
}

func dispatchTool(ctx context.Context, name string, args map[string]json.RawMessage, client *Client) (any, error) {
	if client == nil {
		return nil, &MCPError{Code: "configuration_missing", Message: "client is not configured"}
	}
	switch name {
	case "list_models":
		if len(args) != 0 {
			return nil, &ValidationError{Reason: "list_models takes no arguments"}
		}
		return rawCall(client.ListModels(ctx))
	case "preview_billing":
		input, err := parseBillingInput(args)
		if err != nil {
			return nil, err
		}
		return rawCall(client.PreviewBilling(ctx, input))
	case "create_image":
		input, err := parseImageInput(args)
		if err != nil {
			return nil, err
		}
		return rawCall(client.CreateImage(ctx, input))
	case "create_video":
		input, err := parseVideoInput(args)
		if err != nil {
			return nil, err
		}
		return rawCall(client.CreateVideo(ctx, input))
	case "get_operation":
		if err := rejectUnknownArgs(args, "operation_id"); err != nil {
			return nil, err
		}
		operationID, err := requiredString(args, "operation_id")
		if err != nil {
			return nil, err
		}
		return rawCall(client.GetOperation(ctx, operationID))
	case "download_result":
		if err := rejectUnknownArgs(args, "operation_id", "result_id"); err != nil {
			return nil, err
		}
		operationID, err := requiredString(args, "operation_id")
		if err != nil {
			return nil, err
		}
		resultID, err := requiredString(args, "result_id")
		if err != nil {
			return nil, err
		}
		return client.DownloadResult(ctx, operationID, resultID)
	case "ensure_segments":
		if err := rejectUnknownArgs(args, "operation_id", "result_id"); err != nil {
			return nil, err
		}
		operationID, err := requiredString(args, "operation_id")
		if err != nil {
			return nil, err
		}
		resultID, err := requiredString(args, "result_id")
		if err != nil {
			return nil, err
		}
		return rawCall(client.EnsureSegments(ctx, operationID, resultID))
	case "get_segments":
		if err := rejectUnknownArgs(args, "operation_id", "result_id"); err != nil {
			return nil, err
		}
		operationID, err := requiredString(args, "operation_id")
		if err != nil {
			return nil, err
		}
		resultID, err := requiredString(args, "result_id")
		if err != nil {
			return nil, err
		}
		return rawCall(client.GetSegments(ctx, operationID, resultID))
	default:
		return nil, &MCPError{Code: "unknown_tool", Message: "unknown tool"}
	}
}

func rawCall(raw json.RawMessage, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, &APIError{Code: "invalid_response", Message: "API returned invalid JSON"}
	}
	return value, nil
}

func sdkValueResult(value any) *mcp.CallToolResult {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(`{}`)
	}
	text := string(raw)
	if len(text) > maxTextOutputBytes {
		text = truncateUTF8Prefix(text, maxTextOutputBytes) + "… [truncated; use get_operation or download_result]"
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: value,
	}
}

func sdkErrorResult(err error, secret string) *mcp.CallToolResult {
	mcpErr := asMCPError(err, secret)
	raw, _ := json.Marshal(mcpErr)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: mcpErr,
		IsError:           true,
	}
}

func asMCPError(err error, secret string) MCPError {
	if err == nil {
		return MCPError{Code: "unknown_error", Message: "unknown error"}
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return MCPError{Code: "invalid_tool_input", Message: redactString(validation.Error(), secret), HTTPStatus: 400}
	}
	var mcpErr *MCPError
	if errors.As(err, &mcpErr) && mcpErr != nil {
		copy := *mcpErr
		copy.Message = redactString(copy.Message, secret)
		return copy
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr != nil {
		return MCPError{
			Code:       redactString(apiErr.Code, secret),
			Message:    redactString(apiErr.Message, secret),
			HTTPStatus: apiErr.HTTPStatus,
			RetryAfter: redactString(apiErr.RetryAfter, secret),
			Details:    json.RawMessage(redactBytes(apiErr.Details, secret)),
		}
	}
	return MCPError{Code: "client_error", Message: redactString(err.Error(), secret)}
}

func transitionsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 19, "items": map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"prompt", "duration"},
		"properties": map[string]any{"prompt": map[string]any{"type": "string"}, "duration": map[string]any{"type": "number", "exclusiveMinimum": 0}},
	}}
}
