package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestMultiframePromptContractThroughMCPAndHTTP(t *testing.T) {
	for _, workflow := range []string{"multiframe2video", "frames2video"} {
		for _, multipart := range []bool{false, true} {
			for _, explicitEmpty := range []bool{false, true} {
				name := workflow + map[bool]string{false: "/json", true: "/multipart"}[multipart] + map[bool]string{false: "/omitted", true: "/empty"}[explicitEmpty]
				t.Run(name, func(t *testing.T) {
					args := map[string]any{"workflow": workflow, "model": "fixture-route", "provider_id": "factory", "resolution": "720p", "quote_id": "quote-fixture", "idempotency_key": "fixed-multiframe-fixture"}
					if explicitEmpty {
						args["prompt"] = ""
					}
					makeRef := func(slot int) map[string]any {
						ref := map[string]any{"source_type": "url", "url": "https://media.example/first.png", "slot": slot}
						if slot == 1 {
							ref["url"] = "https://media.example/last.png"
						}
						if multipart {
							ref = map[string]any{"source_type": "file", "content_base64": "aW1hZ2U=", "filename": "frame.png", "slot": slot}
						}
						return ref
					}
					transitions := []any{map[string]any{"prompt": "pan between frames", "duration": float64(3)}}
					if workflow == "multiframe2video" {
						args["reference_images"] = []any{makeRef(0), makeRef(1)}
						args["transitions"] = transitions
					} else {
						args["prompt"] = "preserve first and last frame"
						args["duration"] = 5
						args["first_frame"], args["last_frame"] = makeRef(0), makeRef(0)
					}
					calls := 0
					client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var got map[string]any
						if multipart {
							if err := r.ParseMultipartForm(1 << 20); err != nil {
								t.Fatal(err)
							}
							defer r.MultipartForm.RemoveAll()
							if err := json.Unmarshal([]byte(r.FormValue("request")), &got); err != nil {
								t.Fatal(err)
							}
							fields := []string{"reference_0", "reference_1"}
							if workflow == "frames2video" {
								fields = []string{"first_frame", "last_frame"}
							}
							for _, field := range fields {
								file, _, err := r.FormFile(field)
								if err != nil {
									t.Fatal(err)
								}
								file.Close()
							}
						} else if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Fatal(err)
						}
						if r.URL.Path != "/openapi/v1/ai/videos" || r.Header.Get("Idempotency-Key") != "fixed-multiframe-fixture" {
							t.Errorf("wrong route/key")
						}
						if got["workflow"] != workflow || got["quote_id"] != "quote-fixture" {
							t.Errorf("lost spec: %#v", got)
						}
						if workflow == "multiframe2video" {
							if prompt, _ := got["prompt"].(string); prompt != "" || !reflect.DeepEqual(got["transitions"], transitions) {
								t.Errorf("wrong multiframe prompt: %#v", got)
							}
							if d, ok := got["duration"]; ok && d != float64(0) {
								t.Errorf("unexpected duration: %v", d)
							}
							if a, ok := got["aspect_ratio"]; ok && a != "" {
								t.Errorf("unexpected aspect ratio: %v", a)
							}
							refs, _ := got["reference_images"].([]any)
							if len(refs) != 2 || refs[0].(map[string]any)["slot"] != float64(0) || refs[1].(map[string]any)["slot"] != float64(1) {
								t.Errorf("lost frame order: %#v", refs)
							}
						} else if got["prompt"] != args["prompt"] || got["first_frame"] == nil || got["last_frame"] == nil {
							t.Errorf("lost first/last frame: %#v", got)
						}
						jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-fixture"}})
					}))
					if _, err := dispatchTool(context.Background(), "create_video", dreaminaArgs(t, args), client); err != nil {
						t.Fatal(err)
					}
					if calls != 1 {
						t.Fatalf("expected one local HTTP call, got %d", calls)
					}
				})
			}
		}
	}
}

func TestVideoPromptFailuresStayLocal(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid prompt reached HTTP") }))
	for _, args := range []map[string]any{
		{"workflow": "text2video"}, {"workflow": "text2video", "prompt": ""}, {"workflow": "text2video", "prompt": "x"},
		{"workflow": "multiframe2video", "prompt": "nonempty top-level prompt"},
	} {
		args["idempotency_key"] = "fixed-invalid-prompt"
		if _, err := dispatchTool(context.Background(), "create_video", dreaminaArgs(t, args), client); err == nil {
			t.Fatalf("accepted invalid prompt: %#v", args)
		}
	}
	for _, definition := range toolDefinitions() {
		if definition.Name != "create_video" {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, required := range schema["required"].([]any) {
			if required == "prompt" {
				t.Fatal("schema requires multiframe top-level prompt")
			}
		}
		prompt := schema["properties"].(map[string]any)["prompt"].(map[string]any)
		if _, exists := prompt["minLength"]; exists || !strings.Contains(prompt["description"].(string), "multiframe2video") {
			t.Fatal("schema omits conditional prompt contract")
		}
	}
}
