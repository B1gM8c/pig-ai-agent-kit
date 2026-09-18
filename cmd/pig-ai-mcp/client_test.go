package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func fakeAPIKey() string {
	return "pigai_" + strings.Repeat("t", 32)
}

func testClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		BaseURL:            server.URL,
		APIKey:             fakeAPIKey(),
		HTTPClient:         server.Client(),
		DownloadDir:        t.TempDir(),
		MaxDownloadBytes:   maxDownloadBytes,
		AllowCustomBaseURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

func jsonEnvelope(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": value})
}

func TestClientJSONEnvelopeAuthAndIdempotency(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var keys []string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1/ai/images" {
			t.Errorf("path = %s, want /openapi/v1/ai/images", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != fakeAPIKey() {
			t.Errorf("X-API-Key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization must remain empty, got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-1"}})
	}))

	input := ImageInput{Prompt: "a test image", Mode: "text_to_image", IdempotencyKey: "same-key-123"}
	first, err := client.CreateImage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CreateImage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || string(first) != `{"operation":{"id":"op-1"}}` {
		t.Fatalf("unexpected unwrapped result: first=%s second=%s", first, second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != input.IdempotencyKey || keys[1] != input.IdempotencyKey {
		t.Fatalf("idempotency keys = %#v", keys)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("retry body changed: %#v", bodies)
	}
	var payload map[string]any
	if err := json.Unmarshal(bodies[0], &payload); err != nil {
		t.Fatal(err)
	}
	for _, unsupported := range []string{"aspect_ratio", "resolution"} {
		if _, ok := payload[unsupported]; ok {
			t.Fatalf("image payload unexpectedly contains %s: %s", unsupported, bodies[0])
		}
	}
}

func TestClientEnsureSegmentsUsesNaturalIdempotency(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openapi/v1/ai/operations/op-1/results/res-1/segments/ensure" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("natural-idempotent ensure sent Idempotency-Key %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.TrimSpace(string(body)) != "{}" {
			t.Errorf("body = %q, want {}", body)
		}
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"status": "pending"})
	}))

	result, err := client.EnsureSegments(context.Background(), "op-1", "res-1")
	if err != nil || string(result) != `{"status":"pending"}` {
		t.Fatalf("ensure result = %s, err=%v", result, err)
	}
}

func TestClientRedirectPolicyAllowsSameOriginAndKeepsAPIKey(t *testing.T) {
	finalHits := 0
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi/v1/ai/models":
			http.Redirect(w, r, "/openapi/v1/ai/models-final", http.StatusTemporaryRedirect)
		case "/openapi/v1/ai/models-final":
			finalHits++
			if got := r.Header.Get("X-API-Key"); got != fakeAPIKey() {
				t.Errorf("same-origin redirect X-API-Key = %q", got)
			}
			jsonEnvelope(t, w, http.StatusOK, map[string]any{"models": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	if _, err := client.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if finalHits != 1 {
		t.Fatalf("same-origin redirect final hits = %d", finalHits)
	}
}

func TestClientRedirectPolicyStopsSameOriginLoopAtDefaultLimit(t *testing.T) {
	hits := 0
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, r.URL.Path, http.StatusTemporaryRedirect)
	}))

	_, err := client.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("same-origin redirect loop error = %v", err)
	}
	if hits != 10 {
		t.Fatalf("same-origin redirect loop hits = %d, want 10", hits)
	}
}

func TestClientRedirectPolicyRejectsCrossOriginBeforeSendingAPIKey(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("cross-origin target received X-API-Key = %q", got)
		}
		jsonEnvelope(t, w, http.StatusOK, map[string]any{"models": []any{}})
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/openapi/v1/ai/models", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	client, err := NewClient(Config{
		BaseURL:            source.URL,
		APIKey:             fakeAPIKey(),
		HTTPClient:         source.Client(),
		DownloadDir:        t.TempDir(),
		MaxDownloadBytes:   maxDownloadBytes,
		AllowCustomBaseURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect blocked") {
		t.Fatalf("cross-origin redirect error = %v", err)
	}
	if targetHits != 0 {
		t.Fatalf("cross-origin target was contacted %d times", targetHits)
	}
}

func TestClientRedirectPolicyRejectsHTTPSDowngrade(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		jsonEnvelope(t, w, http.StatusOK, map[string]any{"models": []any{}})
	}))
	t.Cleanup(target.Close)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/openapi/v1/ai/models", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	client, err := NewClient(Config{
		BaseURL:            source.URL,
		APIKey:             fakeAPIKey(),
		HTTPClient:         source.Client(),
		DownloadDir:        t.TempDir(),
		MaxDownloadBytes:   maxDownloadBytes,
		AllowCustomBaseURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTPS cannot downgrade to HTTP") {
		t.Fatalf("HTTPS downgrade error = %v", err)
	}
	if targetHits != 0 {
		t.Fatalf("HTTPS downgrade target was contacted %d times", targetHits)
	}
}

func TestClientMultipartUsesRequestManifestAndOrderedFileParts(t *testing.T) {
	content := []byte("reference-file")
	mask := []byte("mask-file")
	encoded := base64.StdEncoding.EncodeToString(content)
	maskEncoded := base64.StdEncoding.EncodeToString(mask)
	var gotRequest map[string]any
	var gotFiles []string
	var gotFileData map[string][]byte
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1/ai/images" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if values := r.MultipartForm.Value["references"]; len(values) != 0 {
			t.Errorf("legacy generic references field present: %#v", values)
		}
		requestRaw := r.FormValue("request")
		if err := json.Unmarshal([]byte(requestRaw), &gotRequest); err != nil {
			t.Errorf("request part is not JSON: %v", err)
		}
		gotFileData = make(map[string][]byte)
		for field, headers := range r.MultipartForm.File {
			for _, header := range headers {
				file, err := header.Open()
				if err != nil {
					t.Errorf("open %s: %v", field, err)
					continue
				}
				data, _ := io.ReadAll(file)
				_ = file.Close()
				gotFiles = append(gotFiles, field)
				gotFileData[field] = data
			}
		}
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-multipart"}})
	}))

	input := ImageInput{
		Prompt: "ordered refs", Mode: "image_to_image", IdempotencyKey: "multipart-key-1",
		References: []ReferenceInput{
			{Role: "style", URL: "https://cdn.example/style.png"},
			{Role: "subject", ContentBase64: encoded, Name: "subject.png", ContentType: "image/png"},
		},
		Mask: &ReferenceInput{Role: "mask", ContentBase64: maskEncoded, Name: "mask.png", ContentType: "image/png"},
	}
	if _, err := client.CreateImage(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	refs, ok := gotRequest["references"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("request references = %#v", gotRequest["references"])
	}
	first := refs[0].(map[string]any)
	second := refs[1].(map[string]any)
	if first["source_type"] != "url" || first["slot"] != float64(0) || first["url"] != "https://cdn.example/style.png" {
		t.Fatalf("first manifest entry = %#v", first)
	}
	if second["source_type"] != "file" || second["slot"] != float64(1) || second["role"] != "subject" {
		t.Fatalf("second manifest entry = %#v", second)
	}
	maskManifest := gotRequest["mask"].(map[string]any)
	if maskManifest["source_type"] != "file" {
		t.Fatalf("mask manifest = %#v", maskManifest)
	}
	if len(gotFiles) != 2 {
		t.Fatalf("multipart file fields = %#v", gotFiles)
	}
	if !bytes.Equal(gotFileData["reference_1"], content) || !bytes.Equal(gotFileData["mask"], mask) {
		t.Fatalf("multipart data = %#v", gotFileData)
	}
}

func TestClientVideoContractOmitsImageOnlyFields(t *testing.T) {
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
	}
	if _, err := client.CreateVideo(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["mode"]; ok {
		t.Fatal("video payload contains unsupported mode")
	}
	if _, ok := payload["references"]; ok {
		t.Fatal("video payload contains unsupported references")
	}
	if got := payload["first_frame"].(map[string]any)["source_type"]; got != "url" {
		t.Fatalf("first_frame source_type = %#v", got)
	}
}

func TestClientVideoMultipartUsesRequestAndFirstFramePart(t *testing.T) {
	frame := []byte("first-frame")
	var request map[string]any
	var frameData []byte
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if len(r.MultipartForm.Value["references"]) != 0 || len(r.MultipartForm.File["references"]) != 0 {
			t.Errorf("video request contains generic references parts")
		}
		if err := json.Unmarshal([]byte(r.FormValue("request")), &request); err != nil {
			t.Errorf("request part is not JSON: %v", err)
		}
		headers := r.MultipartForm.File["first_frame"]
		if len(headers) != 1 {
			t.Errorf("first_frame parts = %d", len(headers))
		} else {
			file, err := headers[0].Open()
			if err == nil {
				frameData, _ = io.ReadAll(file)
				_ = file.Close()
			}
		}
		jsonEnvelope(t, w, http.StatusAccepted, map[string]any{"operation": map[string]any{"id": "op-video-multipart"}})
	}))

	input := VideoInput{
		Prompt: "video frame", Duration: 4, IdempotencyKey: "video-multipart-key",
		FirstFrame: &ReferenceInput{ContentBase64: base64.StdEncoding.EncodeToString(frame), Name: "frame.png", ContentType: "image/png"},
	}
	if _, err := client.CreateVideo(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	firstFrame := request["first_frame"].(map[string]any)
	if firstFrame["source_type"] != "file" || firstFrame["slot"] != float64(0) {
		t.Fatalf("first_frame manifest = %#v", firstFrame)
	}
	if !bytes.Equal(frameData, frame) {
		t.Fatalf("first_frame data = %q", frameData)
	}
}

func TestClientRejectsInvalidJSONResponse(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "not-json")
	}))
	_, err := client.ListModels(context.Background())
	if err == nil {
		t.Fatal("invalid JSON response was accepted")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_response" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientAPIErrorPreservesRetryMetadataAndRedactsSecret(t *testing.T) {
	secret := fakeAPIKey()
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"retry `+secret+`","details":{"token":"`+secret+`"}}`)
	}))
	_, err := client.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T", err)
	}
	if apiErr.Code != "rate_limited" || apiErr.HTTPStatus != http.StatusTooManyRequests || apiErr.RetryAfter != "7" {
		t.Fatalf("api error = %+v", apiErr)
	}
	result := asMCPError(err, secret)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(apiErr.ResponseRaw), secret) {
		t.Fatalf("secret leaked in error: %s / %s", encoded, apiErr.ResponseRaw)
	}
}

func TestClientIdempotencyConflict(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"idempotency_conflict","message":"same key has a different request"}`)
	}))
	_, err := client.CreateImage(context.Background(), ImageInput{Prompt: "conflict", IdempotencyKey: "conflict-key"})
	if err == nil {
		t.Fatal("expected conflict")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "idempotency_conflict" || apiErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientDownloadStreamsSafelyAndHashes(t *testing.T) {
	data := []byte("video-result")
	sum := sha256.Sum256(data)
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi/v1/ai/operations/op-1/results/result-1/content" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "video/mp4; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="../../escape.mp4"`)
		w.Header().Set("Content-Range", "bytes 0-11/12")
		_, _ = w.Write(data)
	}))
	meta, err := client.DownloadResult(context.Background(), "op-1", "result-1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Bytes != int64(len(data)) || meta.SHA256 != hex.EncodeToString(sum[:]) || meta.MIMEType != "video/mp4" || meta.ContentRange == "" {
		t.Fatalf("metadata = %+v", meta)
	}
	if filepath.Dir(meta.Path) != client.downloadDir || filepath.Base(meta.Path) != "escape.mp4" {
		t.Fatalf("unsafe output path = %s", meta.Path)
	}
	got, err := os.ReadFile(meta.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("downloaded bytes = %q, err=%v", got, err)
	}
}

func TestSafeFilenameWindowsCompatibility(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "forbidden characters", input: `bad<>:"|?*name.txt`, want: "bad-------name.txt"},
		{name: "trailing periods and spaces", input: "report...   ", want: "report"},
		{name: "reserved con with extension", input: "CON.txt", want: "_CON.txt"},
		{name: "reserved prn", input: "PRN", want: "_PRN"},
		{name: "reserved aux", input: "aux.dat", want: "_aux.dat"},
		{name: "reserved nul", input: "NUL.tar.gz", want: "_NUL.tar.gz"},
		{name: "reserved com1", input: "COM1.txt", want: "_COM1.txt"},
		{name: "reserved com case insensitive", input: "com9.log", want: "_com9.log"},
		{name: "reserved lpt without extension", input: "LPT1", want: "_LPT1"},
		{name: "reserved lpt9", input: "lpt9.csv", want: "_lpt9.csv"},
		{name: "path traversal remains a basename", input: "../../escape.mp4", want: "escape.mp4"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeFilename(test.input); got != test.want {
				t.Fatalf("safeFilename(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}

	long := strings.Repeat("a", 140) + ".mp4"
	got := safeFilename(long)
	if len(got) != 128 || !strings.HasSuffix(got, ".mp4") {
		t.Fatalf("long filename = %q (len %d), want 128 bytes with .mp4 extension", got, len(got))
	}
	unicodeName := strings.Repeat("图", 100) + ".png"
	got = safeFilename(unicodeName)
	if len(got) > 128 || !utf8.ValidString(got) || !strings.HasSuffix(got, ".png") {
		t.Fatalf("unicode filename = %q (len %d), want valid UTF-8 <= 128 bytes with .png extension", got, len(got))
	}
}

func TestClientDownloadLimitRemovesTemporaryFile(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	client.maxDownloadBytes = 4
	_, err := client.DownloadResult(context.Background(), "op-1", "result-1")
	if err == nil {
		t.Fatal("expected download limit error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "download_too_large" {
		t.Fatalf("error = %#v", err)
	}
	entries, readErr := os.ReadDir(client.downloadDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %#v", entries)
	}
}

func TestClientConcurrentDownloadsNeverOverwriteSameFilename(t *testing.T) {
	data := []byte("concurrent-result")
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="same.mp4"`)
		_, _ = w.Write(data)
	}))

	const count = 12
	paths := make(chan string, count)
	errs := make(chan error, count)
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			meta, err := client.DownloadResult(context.Background(), "op-1", "result-1")
			if err != nil {
				errs <- err
				return
			}
			paths <- meta.Path
		}()
	}
	group.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, count)
	for path := range paths {
		if _, exists := seen[path]; exists {
			t.Fatalf("duplicate download path %q", path)
		}
		seen[path] = struct{}{}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("downloaded %q = %q, err=%v", path, got, err)
		}
	}
	if len(seen) != count {
		t.Fatalf("download paths = %d, want %d", len(seen), count)
	}
}

func TestLoadConfigRequiresExplicitCustomBaseURL(t *testing.T) {
	t.Setenv("PIGAI_DOWNLOAD_DIR", t.TempDir())
	t.Setenv("PIGAI_HTTP_TIMEOUT", "")
	t.Setenv("PIGAI_MAX_DOWNLOAD_BYTES", "")
	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "")

	t.Setenv("PIGAI_BASE_URL", defaultBaseURL)
	if cfg, err := LoadConfig(); err != nil || cfg.AllowCustomBaseURL {
		t.Fatalf("official config = %#v, err=%v", cfg, err)
	}

	t.Setenv("PIGAI_BASE_URL", "https://api.example.com")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "PIGAI_ALLOW_CUSTOM_BASE_URL=true") {
		t.Fatalf("custom HTTPS URL without opt-in error = %v", err)
	}
	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "true")
	if cfg, err := LoadConfig(); err != nil || !cfg.AllowCustomBaseURL {
		t.Fatalf("explicit custom HTTPS config = %#v, err=%v", cfg, err)
	}

	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "false")
	t.Setenv("PIGAI_BASE_URL", "http://127.0.0.1:1234")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "PIGAI_ALLOW_CUSTOM_BASE_URL=true") {
		t.Fatalf("loopback fixture without opt-in error = %v", err)
	}
	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "true")
	if cfg, err := LoadConfig(); err != nil || !cfg.AllowCustomBaseURL {
		t.Fatalf("explicit loopback fixture config = %#v, err=%v", cfg, err)
	}

	t.Setenv("PIGAI_BASE_URL", "http://example.com")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("remote HTTP URL with opt-in error = %v", err)
	}
	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "yes")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "must be true or false") {
		t.Fatalf("invalid custom URL opt-in error = %v", err)
	}
}

func TestNewClientRequiresExplicitCustomBaseURL(t *testing.T) {
	_, err := NewClient(Config{BaseURL: "https://api.example.com", DownloadDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "PIGAI_ALLOW_CUSTOM_BASE_URL=true") {
		t.Fatalf("custom NewClient URL without opt-in error = %v", err)
	}
	if _, err := NewClient(Config{
		BaseURL:            "https://api.example.com",
		DownloadDir:        t.TempDir(),
		AllowCustomBaseURL: true,
	}); err != nil {
		t.Fatalf("explicit custom NewClient URL: %v", err)
	}
}

func TestLoadConfigBoundsTimeout(t *testing.T) {
	t.Setenv("PIGAI_BASE_URL", defaultBaseURL)
	t.Setenv("PIGAI_ALLOW_CUSTOM_BASE_URL", "false")
	t.Setenv("PIGAI_DOWNLOAD_DIR", t.TempDir())
	t.Setenv("PIGAI_HTTP_TIMEOUT", "11m")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("overlong timeout was accepted")
	}
}

func TestDefaultDownloadDirUsesUserCache(t *testing.T) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cacheDir, "pig-ai-mcp", "downloads")
	if got != want {
		t.Fatalf("default download directory = %q, want %q", got, want)
	}
}

func TestEnsurePrivateDownloadDirCreatesAndTightensPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "downloads")
	if err := ensurePrivateDownloadDir(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("created directory mode = %o, want 700", got)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensurePrivateDownloadDir(dir); err != nil {
			t.Fatal(err)
		}
		info, err = os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("reused directory mode = %o, want 700", got)
		}
	}
}

func TestEnsurePrivateDownloadDirRejectsSymlinkAndNonDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := ensurePrivateDownloadDir(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink directory error = %v", err)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDownloadDir(file); err == nil {
		t.Fatal("ordinary file was accepted as a download directory")
	}
}

func TestEnsurePrivateDownloadDirRejectsFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if err := ensurePrivateDownloadDir(root); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("filesystem root error = %v", err)
	}
}
