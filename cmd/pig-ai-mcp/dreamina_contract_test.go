package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func dreaminaArgs(t *testing.T, input map[string]any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

// Exercise the public MCP parser and the real HTTP encoder together. In
// particular, switching to multipart must not discard the frozen quote/spec.
func TestDreaminaQuoteAndCreateContract(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		for _, multipart := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/json", true: "/multipart"}[multipart], func(t *testing.T) {
				spec := map[string]any{"provider_id": "factory", "model": "fixture-model", "workflow": "fixture-workflow", "resolution": "720p"}
				preview := map[string]any{"mode": "reference_to_video", "n": 1, "reference_image_count": 1}
				create := map[string]any{"prompt": "fixture only", "idempotency_key": "fixed-fixture-request"}
				for k, v := range spec {
					preview[k] = v
					create[k] = v
				}
				if kind == "image" {
					preview["mode"] = "image_to_image"
					create["mode"] = "image_to_image"
					create["n"] = 1
					preview["width"] = 1024
					create["width"] = 1024
					preview["height"] = 768
					create["height"] = 768
				} else {
					for k, v := range map[string]any{"duration": 10, "aspect_ratio": "16:9", "transitions": []any{map[string]any{"prompt": "pan", "duration": 2.5}}} {
						preview[k] = v
						create[k] = v
					}
					preview["reference_video_count"] = 1
					preview["reference_audio_count"] = 1
					create["reference_video_urls"] = []string{"https://media.example/reference.mp4"}
					create["reference_audio_urls"] = []string{"https://media.example/reference.wav"}
				}
				ref := map[string]any{"source_type": "url", "url": "https://media.example/reference.png", "slot": 0}
				if multipart {
					ref = map[string]any{"source_type": "file", "content_base64": "aW1hZ2U=", "name": "reference.png", "slot": 0}
				}
				refField := "references"
				if kind == "video" {
					refField = "reference_images"
				}
				create[refField] = []any{ref}
				calls := 0
				client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var got map[string]any
					if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						defer r.MultipartForm.RemoveAll()
						if err := json.Unmarshal([]byte(r.FormValue("request")), &got); err != nil {
							t.Error(err)
							return
						}
						file, _, err := r.FormFile("reference_0")
						if err != nil {
							t.Error(err)
							return
						}
						file.Close()
					} else if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
						return
					}
					if r.URL.Path == "/openapi/v1/ai/billing-preview" {
						expected := map[string]any{}
						data, _ := json.Marshal(preview)
						_ = json.Unmarshal(data, &expected)
						if !reflect.DeepEqual(got, expected) {
							t.Errorf("preview spec changed: got %#v want %#v", got, expected)
						}
						jsonEnvelope(t, w, http.StatusOK, map[string]any{"quote_id": "quote-fixture"})
						return
					}
					if r.URL.Path != "/openapi/v1/ai/"+kind+"s" {
						t.Errorf("path %s", r.URL.Path)
					}
					if r.Header.Get("Idempotency-Key") != "fixed-fixture-request" {
						t.Error("lost idempotency key")
					}
					expected := map[string]any{}
					data, _ := json.Marshal(create)
					_ = json.Unmarshal(data, &expected)
					delete(expected, "idempotency_key")
					// Inline transport metadata belongs only in the file part.
					expectedRef := expected[refField].([]any)[0].(map[string]any)
					delete(expectedRef, "name")
					delete(expectedRef, "content_base64")
					// Existing manifests assign a default semantic role.
					expectedRef["role"] = "reference"
					if !reflect.DeepEqual(got, expected) {
						t.Errorf("create spec changed: got %#v want %#v", got, expected)
					}
					jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-fixture", "generation_status": "uncertain", "progress": 42, "progress_text": "estimated", "error": map[string]any{"code": "50131", "message": "verify original"}}})
				}))
				quoted, err := dispatchTool(context.Background(), "preview_billing", dreaminaArgs(t, preview), client)
				if err != nil {
					t.Fatal(err)
				}
				create["quote_id"] = quoted.(map[string]any)["quote_id"]
				result, err := dispatchTool(context.Background(), "create_"+kind, dreaminaArgs(t, create), client)
				if err != nil {
					t.Fatal(err)
				}
				op := result.(map[string]any)["operation"].(map[string]any)
				if op["progress"] != float64(42) || op["progress_text"] != "estimated" || op["generation_status"] != "uncertain" || op["error"].(map[string]any)["code"] != "50131" {
					t.Fatalf("operation was altered: %#v", op)
				}
				if calls != 2 {
					t.Fatalf("unexpected retries: %d", calls)
				}
			})
		}
	}
}

func TestDreaminaUnknownArgsAndQuoteStaleDoNotSubmitAgain(t *testing.T) {
	calls := 0
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"quote_stale","message":"quote changed"}`))
	}))
	base := map[string]any{"prompt": "fixture only", "idempotency_key": "fixed-fixture-request"}
	for _, field := range []string{"mode", "unknown_option"} {
		bad := map[string]any{}
		for k, v := range base {
			bad[k] = v
		}
		bad[field] = "value"
		if _, err := dispatchTool(context.Background(), "create_video", dreaminaArgs(t, bad), client); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
	bad := dreaminaArgs(t, base)
	bad["transitions"] = json.RawMessage(`[{"prompt":"pan","duration":2,"unknown":true}]`)
	if _, err := dispatchTool(context.Background(), "create_video", bad, client); err == nil {
		t.Fatal("accepted unknown transition field")
	}
	if calls != 0 {
		t.Fatal("invalid input reached upstream")
	}
	_, err := dispatchTool(context.Background(), "create_video", dreaminaArgs(t, base), client)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "quote_stale" || apiErr.HTTPStatus != 409 {
		t.Fatalf("quote error lost: %#v", err)
	}
	if calls != 1 {
		t.Fatalf("unexpected automatic retry: %d", calls)
	}
}

func TestDreaminaReferenceURLValidationBeforeHTTP(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid references reached HTTP") }))
	for _, urls := range [][]string{{"file:///private/reference.mp4"}, {"https://user:secret@media.example/ref.mp4"}, make([]string, 11)} {
		_, err := dispatchTool(context.Background(), "create_video", dreaminaArgs(t, map[string]any{"prompt": "fixture only", "idempotency_key": "fixed-fixture-request", "reference_video_urls": urls}), client)
		if err == nil {
			t.Fatal("invalid URL manifest accepted")
		}
	}
	for field, count := range map[string]int{"reference_image_count": 31, "reference_video_count": 11, "reference_audio_count": 11} {
		_, err := dispatchTool(context.Background(), "preview_billing", dreaminaArgs(t, map[string]any{"mode": "reference_to_video", "n": 1, field: count}), client)
		if err == nil {
			t.Fatalf("invalid count %s accepted", field)
		}
	}
}

func TestGetOperationPreservesProviderProgress(t *testing.T) {
	observation := map[string]any{"provider_status": "running", "progress_percent": 37, "progress_source": "provider", "stage": "generating", "queue_position": 0, "queue_total": 120, "estimated_remaining_seconds": 23, "observed_at_ms": 1793520000000}
	envelope := map[string]any{"operation": map[string]any{"id": "op-fixture", "provider_progress": observation, "generation_status": "running", "settlement_status": "reserved"}}
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/openapi/v1/ai/operations/op-fixture" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		jsonEnvelope(t, w, http.StatusOK, envelope)
	}))
	got, err := dispatchTool(context.Background(), "get_operation", dreaminaArgs(t, map[string]any{"operation_id": "op-fixture"}), client)
	if err != nil {
		t.Fatal(err)
	}
	expectedBytes, _ := json.Marshal(envelope)
	gotBytes, _ := json.Marshal(got)
	if string(gotBytes) != string(expectedBytes) {
		t.Fatalf("operation observation altered: %s", gotBytes)
	}
}

func TestValidationAcceptsVideoReferencesAndPresetVoices(t *testing.T) {
	args := map[string]json.RawMessage{
		"prompt":           json.RawMessage(`"video"`),
		"idempotency_key":  json.RawMessage(`"video-key-2"`),
		"reference_images": json.RawMessage(`[{"url":"https://cdn.example/one.png"},{"url":"https://cdn.example/two.png","slot":1}]`),
		"last_frame":       json.RawMessage(`{"url":"https://cdn.example/last.png"}`),
		"keyframes":        json.RawMessage(`[{"image":{"url":"https://cdn.example/key.png"},"timestamp_s":1}]`),
		"reference_audios": json.RawMessage(`[{"voice_id":"eve"},{"voice_id":"leo"}]`),
	}
	input, err := parseVideoInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateVideoInput(input); err != nil {
		t.Fatal(err)
	}
	if input.LastFrame == nil || len(input.ReferenceImages) != 2 || len(input.Keyframes) != 1 || len(input.ReferenceAudios) != 2 {
		t.Fatalf("video input = %#v", input)
	}
	input.Keyframes[0].TimestampS = 0.5
	if err = validateVideoInput(input); err == nil || !strings.Contains(err.Error(), "keyframes") {
		t.Fatalf("off-grid keyframe error = %v", err)
	}
	input.Keyframes[0].TimestampS = 1

	input.ReferenceAudios = append(input.ReferenceAudios, AudioReferenceInput{VoiceID: "ara"}, AudioReferenceInput{VoiceID: "rex"})
	if err = validateVideoInput(input); err == nil || !strings.Contains(err.Error(), "reference_audios") {
		t.Fatalf("too many voices error = %v", err)
	}
}

func TestValidationAcceptsSevenOrderedVideoReferences(t *testing.T) {
	references := make([]ReferenceInput, maxVideoReferences)
	for index := range references {
		references[index] = ReferenceInput{URL: fmt.Sprintf("https://cdn.example/reference-%d.png", index)}
	}
	input := VideoInput{Prompt: "seven references", IdempotencyKey: "video-key-7", ReferenceImages: references}
	if err := validateVideoInput(input); err != nil {
		t.Fatal(err)
	}
	for index, reference := range input.ReferenceImages {
		if reference.Slot != index {
			t.Fatalf("reference %d slot = %d", index, reference.Slot)
		}
	}
}

func TestClientVideoContractIncludesOrderedReferencesAndVoices(t *testing.T) {
	var payload map[string]any
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1/ai/videos" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-video"}})
	}))
	input := VideoInput{
		Prompt: "a test video", AspectRatio: "16:9", Resolution: "720p", Duration: 5,
		IdempotencyKey: "video-key-123",
		FirstFrame:     &ReferenceInput{URL: "https://cdn.example/frame.png", Role: "first_frame"},
		LastFrame:      &ReferenceInput{URL: "https://cdn.example/last.png", Role: "last_frame"},
		ReferenceImages: []ReferenceInput{
			{URL: "https://cdn.example/subject.png", Role: "subject"},
			{URL: "https://cdn.example/style.png", Role: "style", Slot: 1},
		},
		Keyframes:       []VideoKeyframeInput{{Image: ReferenceInput{URL: "https://cdn.example/key.png", Role: "keyframe"}, TimestampS: 1}},
		ReferenceAudios: []AudioReferenceInput{{VoiceID: "eve"}, {VoiceID: "leo"}},
	}
	if _, err := client.CreateVideo(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["mode"]; ok {
		t.Fatal("video payload contains unsupported mode")
	}
	images, ok := payload["reference_images"].([]any)
	if !ok || len(images) != 2 {
		t.Fatalf("reference_images = %#v", payload["reference_images"])
	}
	audios, ok := payload["reference_audios"].([]any)
	if !ok || len(audios) != 2 || audios[0].(map[string]any)["voice_id"] != "eve" || audios[1].(map[string]any)["voice_id"] != "leo" {
		t.Fatalf("reference_audios = %#v", payload["reference_audios"])
	}
	if got := payload["first_frame"].(map[string]any)["source_type"]; got != "url" {
		t.Fatalf("first_frame source_type = %#v", got)
	}
	if got := payload["last_frame"].(map[string]any)["source_type"]; got != "url" {
		t.Fatalf("last_frame source_type = %#v", got)
	}
	keyframes := payload["keyframes"].([]any)
	if len(keyframes) != 1 || keyframes[0].(map[string]any)["timestamp_s"] != float64(1) {
		t.Fatalf("keyframes = %#v", keyframes)
	}
}

func TestVideoMultipartPreservesFramesAndExplicitSlots(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		var payload map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("request")), &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"first_frame", "last_frame", "reference_1", "reference_0", "keyframe_0"} {
			f, _, err := r.FormFile(field)
			if err != nil {
				t.Errorf("missing file %s: %v", field, err)
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			if string(data) != "image" {
				t.Errorf("changed %s bytes", field)
			}
		}
		images := payload["reference_images"].([]any)
		if images[0].(map[string]any)["slot"] != float64(1) || images[1].(map[string]any)["slot"] != float64(0) {
			t.Errorf("explicit ordered slots changed: %#v", images)
		}
		if payload["quote_id"] != "quote-media" || payload["workflow"] != "fixture-workflow" {
			t.Error("lost quote/workflow")
		}
		keyframes := payload["keyframes"].([]any)
		if keyframes[0].(map[string]any)["timestamp_s"] != float64(1) {
			t.Error("lost keyframe time")
		}
		if strings.Contains(r.FormValue("request"), "content_base64") {
			t.Error("file bytes leaked into manifest")
		}
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-media"}})
	}))
	args := dreaminaArgs(t, map[string]any{
		"prompt": "fixture video", "idempotency_key": "media-fixture-key", "duration": 5,
		"quote_id": "quote-media", "workflow": "fixture-workflow",
		"first_frame":      map[string]any{"content_base64": "aW1hZ2U="},
		"last_frame":       map[string]any{"content_base64": "aW1hZ2U="},
		"reference_images": []any{map[string]any{"content_base64": "aW1hZ2U=", "slot": 1}, map[string]any{"content_base64": "aW1hZ2U=", "slot": 0}},
		"keyframes":        []any{map[string]any{"image": map[string]any{"content_base64": "aW1hZ2U="}, "timestamp_s": 1}},
	})
	if _, err := dispatchTool(context.Background(), "create_video", args, client); err != nil {
		t.Fatal(err)
	}
	// An explicit duplicate zero must not be silently renumbered.
	args["reference_images"] = json.RawMessage(`[{"url":"https://cdn.example/one.png","slot":0},{"url":"https://cdn.example/two.png","slot":0}]`)
	if _, err := dispatchTool(context.Background(), "create_video", args, client); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate slot accepted: %v", err)
	}
}
