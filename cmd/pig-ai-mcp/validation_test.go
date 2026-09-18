package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func validInputKey() string { return "validation-key-1" }

func TestValidationNormalizesOrderedReferences(t *testing.T) {
	input := ImageInput{
		Prompt: "ordered", IdempotencyKey: validInputKey(),
		References: []ReferenceInput{
			{Role: " style ", URL: " https://cdn.example/style.png "},
			{ContentBase64: base64.StdEncoding.EncodeToString([]byte("file"))},
		},
	}
	if err := validateImageInput(input); err != nil {
		t.Fatal(err)
	}
	if input.References[0].SourceType != "url" || input.References[0].Slot != 0 || input.References[0].Role != "style" {
		t.Fatalf("first normalized reference = %#v", input.References[0])
	}
	if input.References[1].SourceType != "file" || input.References[1].Slot != 1 {
		t.Fatalf("second normalized reference = %#v", input.References[1])
	}
}

func TestValidationDistinguishesOmittedAndExplicitZeroReferenceSlots(t *testing.T) {
	args := map[string]json.RawMessage{
		"prompt":          json.RawMessage(`"slots"`),
		"idempotency_key": json.RawMessage(`"slots-key-1"`),
		"mode":            json.RawMessage(`"image_to_image"`),
		"n":               json.RawMessage(`1`),
		"references": json.RawMessage(`[
			{"url":"https://cdn.example/one.png","slot":1},
			{"url":"https://cdn.example/zero.png","slot":0}
		]`),
	}
	input, err := parseImageInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageInput(input); err != nil {
		t.Fatal(err)
	}
	if input.References[0].Slot != 1 || input.References[1].Slot != 0 {
		t.Fatalf("explicit slots were changed: %#v", input.References)
	}

	args["references"] = json.RawMessage(`[
		{"url":"https://cdn.example/one.png"},
		{"url":"https://cdn.example/zero.png","slot":0}
	]`)
	input, err = parseImageInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageInput(input); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("duplicate explicit zero slot error = %v", err)
	}
}

func TestValidationRequiresPositiveImageCount(t *testing.T) {
	args := map[string]json.RawMessage{
		"prompt":          json.RawMessage(`"count"`),
		"idempotency_key": json.RawMessage(`"count-key-1"`),
		"mode":            json.RawMessage(`"text_to_image"`),
	}
	if _, err := parseImageInput(args); err == nil || !strings.Contains(err.Error(), "n") {
		t.Fatalf("missing image n error = %v", err)
	}
	args["n"] = json.RawMessage(`1`)
	input, err := parseImageInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageInput(input); err != nil {
		t.Fatalf("n=1 rejected: %v", err)
	}
	args["n"] = json.RawMessage(`0`)
	input, err = parseImageInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateImageInput(input); err == nil || !strings.Contains(err.Error(), "between 1 and 4") {
		t.Fatalf("explicit n=0 error = %v", err)
	}
	billingArgs := map[string]json.RawMessage{"mode": json.RawMessage(`"text_to_image"`)}
	if _, err := parseBillingInput(billingArgs); err == nil || !strings.Contains(err.Error(), "n") {
		t.Fatalf("missing billing n error = %v", err)
	}
	billingArgs["n"] = json.RawMessage(`0`)
	if _, err := parseBillingInput(billingArgs); err == nil || !strings.Contains(err.Error(), "n") {
		t.Fatalf("billing n=0 error = %v", err)
	}
}

func TestValidationAllowsExplicitZeroVideoDuration(t *testing.T) {
	args := map[string]json.RawMessage{
		"prompt":          json.RawMessage(`"duration"`),
		"idempotency_key": json.RawMessage(`"duration-key-1"`),
		"duration":        json.RawMessage(`0`),
	}
	input, err := parseVideoInput(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVideoInput(input); err != nil {
		t.Fatalf("duration=0 rejected: %v", err)
	}
}

func TestValidationMatchesBackendPromptMinimum(t *testing.T) {
	if err := validatePrompt("猪"); err == nil || !strings.Contains(err.Error(), "2-10000") {
		t.Fatalf("one-rune prompt error = %v", err)
	}
	if err := validatePrompt("猪猪"); err != nil {
		t.Fatalf("two-rune prompt rejected: %v", err)
	}
}

func TestValidationRejectsUnsupportedBillingAndImageModes(t *testing.T) {
	if _, err := parseBillingInput(map[string]json.RawMessage{"mode": json.RawMessage(`"not-a-mode"`), "n": json.RawMessage(`1`)}); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("unsupported billing mode error = %v", err)
	}
	args := map[string]json.RawMessage{
		"prompt":          json.RawMessage(`"mode"`),
		"idempotency_key": json.RawMessage(`"mode-key-1"`),
		"mode":            json.RawMessage(`"not-a-mode"`),
	}
	if _, err := parseImageInput(args); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("unsupported image mode error = %v", err)
	}
}

func TestValidationRequiresOneBillingItemForVideoModes(t *testing.T) {
	for _, mode := range []string{"text_to_video", "image_to_video"} {
		t.Run(mode, func(t *testing.T) {
			args := map[string]json.RawMessage{
				"mode": json.RawMessage(strconv.Quote(mode)),
				"n":    json.RawMessage(`2`),
			}
			if _, err := parseBillingInput(args); err == nil || !strings.Contains(err.Error(), "must be 1") {
				t.Fatalf("%s n=2 error = %v", mode, err)
			}
			args["n"] = json.RawMessage(`1`)
			if _, err := parseBillingInput(args); err != nil {
				t.Fatalf("%s n=1 rejected: %v", mode, err)
			}
		})
	}
}

func TestValidationEnforcesImageModeReferenceRelations(t *testing.T) {
	textInput := ImageInput{
		Prompt: "text", Mode: "text_to_image", N: 1, IdempotencyKey: validInputKey(),
		References: []ReferenceInput{{URL: "https://cdn.example/reference.png"}},
	}
	if err := validateImageInput(textInput); err == nil || !strings.Contains(err.Error(), "does not accept") {
		t.Fatalf("text_to_image references error = %v", err)
	}
	imageInput := ImageInput{Prompt: "image", Mode: "image_to_image", N: 1, IdempotencyKey: validInputKey()}
	if err := validateImageInput(imageInput); err == nil || !strings.Contains(err.Error(), "at least one reference") {
		t.Fatalf("image_to_image missing reference error = %v", err)
	}
}

func TestValidationRejectsConflictingOrUnsupportedReferences(t *testing.T) {
	cases := []struct {
		name  string
		input ImageInput
		want  string
	}{
		{
			name: "conflicting sources",
			input: ImageInput{Prompt: "xx", IdempotencyKey: validInputKey(), References: []ReferenceInput{{
				URL: "https://cdn.example/a.png", ContentBase64: base64.StdEncoding.EncodeToString([]byte("x")),
			}}},
			want: "exactly one",
		},
		{
			name: "unknown source type",
			input: ImageInput{Prompt: "xx", IdempotencyKey: validInputKey(), References: []ReferenceInput{{
				SourceType: "asset", URL: "https://cdn.example/a.png",
			}}},
			want: "source_type must be url or file",
		},
		{
			name: "duplicate slots",
			input: ImageInput{Prompt: "xx", IdempotencyKey: validInputKey(), References: []ReferenceInput{
				{Slot: 2, URL: "https://cdn.example/a.png"},
				{Slot: 2, URL: "https://cdn.example/b.png"},
			}},
			want: "unique",
		},
		{
			name: "credentialed URL",
			input: ImageInput{Prompt: "xx", IdempotencyKey: validInputKey(), References: []ReferenceInput{{
				URL: "https://user:pass@cdn.example/a.png",
			}}},
			want: "credentials",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImageInput(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidationRejectsVideoImageOnlyFieldsAtParserBoundary(t *testing.T) {
	args := map[string]json.RawMessage{
		"prompt":          json.RawMessage(`"video"`),
		"idempotency_key": json.RawMessage(`"video-key-1"`),
		"mode":            json.RawMessage(`"text_to_video"`),
	}
	if _, err := parseVideoInput(args); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported video mode was accepted: %v", err)
	}
}

func TestValidationRejectsOversizedInlineReference(t *testing.T) {
	data := make([]byte, maxInlineReferenceBytes+1)
	input := ImageInput{
		Prompt: "large", IdempotencyKey: validInputKey(),
		References: []ReferenceInput{{ContentBase64: base64.StdEncoding.EncodeToString(data)}},
	}
	err := validateImageInput(input)
	if err == nil {
		t.Fatal("oversized inline reference was accepted")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "references[0].content_base64" {
		t.Fatalf("error = %#v", err)
	}
}

func TestValidationRejectsImageCountsAboveBackendLimit(t *testing.T) {
	for _, n := range []int{5, 16} {
		t.Run("image_n_"+strconv.Itoa(n), func(t *testing.T) {
			err := validateImageInput(ImageInput{Prompt: "count", IdempotencyKey: validInputKey(), N: n})
			if err == nil || !strings.Contains(err.Error(), "between 1 and 4") {
				t.Fatalf("n=%d error = %v", n, err)
			}
		})
		t.Run("billing_n_"+strconv.Itoa(n), func(t *testing.T) {
			_, err := parseBillingInput(map[string]json.RawMessage{"mode": json.RawMessage(`"text_to_image"`), "n": json.RawMessage(strconv.Itoa(n))})
			if err == nil || !strings.Contains(err.Error(), "between 1 and 4") {
				t.Fatalf("n=%d billing error = %v", n, err)
			}
		})
	}
}

func TestValidationRejectsReferenceCountAndSlotAboveBackendLimits(t *testing.T) {
	tooMany := make([]ReferenceInput, maxImageReferences+1)
	for i := range tooMany {
		tooMany[i] = ReferenceInput{URL: "https://cdn.example/reference.png", Slot: i}
	}
	if err := validateImageInput(ImageInput{Prompt: "refs", IdempotencyKey: validInputKey(), References: tooMany}); err == nil || !strings.Contains(err.Error(), "at most 4") {
		t.Fatalf("too many references error = %v", err)
	}

	valid := make([]ReferenceInput, maxImageReferences)
	for i := range valid {
		valid[i] = ReferenceInput{URL: "https://cdn.example/reference.png", Slot: i}
	}
	if err := validateImageInput(ImageInput{Prompt: "refs", IdempotencyKey: validInputKey(), References: valid}); err != nil {
		t.Fatalf("four references were rejected: %v", err)
	}

	invalidSlot := ReferenceInput{URL: "https://cdn.example/reference.png", Slot: maxReferenceSlot + 1}
	if err := validateImageInput(ImageInput{Prompt: "slot", IdempotencyKey: validInputKey(), References: []ReferenceInput{invalidSlot}}); err == nil || !strings.Contains(err.Error(), "between 0 and 3") {
		t.Fatalf("slot=%d error = %v", invalidSlot.Slot, err)
	}
}

func TestValidationIDUsesBackendLimit(t *testing.T) {
	if err := validateID(".leading", "operation_id"); err != nil {
		t.Fatalf("backend-compatible leading punctuation ID was rejected: %v", err)
	}
	valid := strings.Repeat("a", maxResourceIDLength)
	if err := validateID(valid, "operation_id"); err != nil {
		t.Fatalf("64-character ID was rejected: %v", err)
	}
	tooLong := valid + "a"
	if err := validateID(tooLong, "operation_id"); err == nil || !strings.Contains(err.Error(), "at most 64") {
		t.Fatalf("65-character ID error = %v", err)
	}
}

func TestValidationRejectsMalformedArguments(t *testing.T) {
	for _, raw := range []json.RawMessage{[]byte("[]"), []byte("nullx"), []byte(strings.Repeat("x", maxToolRequestBytes+1))} {
		if _, err := parseArguments(raw); err == nil {
			t.Fatalf("arguments %q were accepted", string(raw[:min(len(raw), 20)]))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
