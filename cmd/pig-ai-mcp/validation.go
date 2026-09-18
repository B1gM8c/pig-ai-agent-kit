package main

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"unicode/utf8"
)

func validateID(value, field string) error {
	value = strings.TrimSpace(value)
	if len(value) > maxResourceIDLength {
		return &ValidationError{Field: field, Reason: fmt.Sprintf("must be at most %d characters", maxResourceIDLength)}
	}
	if !idPattern.MatchString(value) {
		return &ValidationError{Field: field, Reason: "must contain only letters, digits, underscore, dot, colon, or hyphen"}
	}
	return nil
}

func validateIdempotencyKey(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 128 {
		return &ValidationError{Field: "idempotency_key", Reason: "must be 8-128 characters"}
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return &ValidationError{Field: "idempotency_key", Reason: "contains a control character"}
	}
	return nil
}

func validatePrompt(prompt string) error {
	if !utf8.ValidString(prompt) {
		return &ValidationError{Field: "prompt", Reason: "must be valid UTF-8"}
	}
	runes := []rune(strings.TrimSpace(prompt))
	if len(runes) < minPromptRunes || len(runes) > maxPromptRunes {
		return &ValidationError{Field: "prompt", Reason: fmt.Sprintf("must be %d-%d characters", minPromptRunes, maxPromptRunes)}
	}
	return nil
}

func validateReference(reference ReferenceInput, field string) error {
	if err := normalizeReference(&reference, 0, ""); err != nil {
		return &ValidationError{Field: field, Reason: err.Error()}
	}
	hasURL := strings.TrimSpace(reference.URL) != ""
	hasContent := strings.TrimSpace(reference.ContentBase64) != ""
	sourceCount := 0
	for _, present := range []bool{hasURL, hasContent} {
		if present {
			sourceCount++
		}
	}
	if sourceCount != 1 {
		return &ValidationError{Field: field, Reason: "set exactly one of url or content_base64"}
	}
	if (reference.SourceType == "url" && !hasURL) || (reference.SourceType == "file" && !hasContent) {
		return &ValidationError{Field: field, Reason: "source_type does not match its source value"}
	}
	if hasURL {
		raw := strings.TrimSpace(reference.URL)
		if len(raw) > maxReferenceURLBytes {
			return &ValidationError{Field: field + ".url", Reason: "URL is too long"}
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
			return &ValidationError{Field: field + ".url", Reason: "must be an absolute URL without credentials or fragment"}
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			return &ValidationError{Field: field + ".url", Reason: "must use HTTPS (HTTP is allowed only for loopback fixtures)"}
		}
	}
	if reference.ContentType != "" {
		if _, _, err := mime.ParseMediaType(reference.ContentType); err != nil || strings.ContainsAny(reference.ContentType, "\r\n") {
			return &ValidationError{Field: field + ".content_type", Reason: "must be a valid MIME type"}
		}
	}
	if hasContent {
		if _, err := decodeReferenceWithLimit(reference, referenceContentMaxBytes(field)); err != nil {
			return &ValidationError{Field: field + ".content_base64", Reason: err.Error()}
		}
	}
	return nil
}

func validateImageInput(input ImageInput) error {
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return err
	}
	if err := validatePrompt(input.Prompt); err != nil {
		return err
	}
	if input.N < 0 || input.N > 4 || (input.nSet && input.N == 0) {
		return &ValidationError{Field: "n", Reason: "must be between 1 and 4 when provided"}
	}
	if input.Mode != "" && input.Mode != "text_to_image" && input.Mode != "image_to_image" {
		return &ValidationError{Field: "mode", Reason: "must be text_to_image or image_to_image"}
	}
	if input.Mode == "text_to_image" && (len(input.References) > 0 || input.Mask != nil) {
		return &ValidationError{Field: "mode", Reason: "text_to_image does not accept references or mask"}
	}
	if input.Mode == "image_to_image" && len(input.References) == 0 {
		return &ValidationError{Field: "references", Reason: "image_to_image requires at least one reference"}
	}
	if len(input.References) > maxImageReferences {
		return &ValidationError{Field: "references", Reason: fmt.Sprintf("must contain at most %d items", maxImageReferences)}
	}
	seenSlots := make(map[int]struct{}, len(input.References))
	for i, reference := range input.References {
		if err := normalizeReference(&input.References[i], i, "reference"); err != nil {
			return &ValidationError{Field: fmt.Sprintf("references[%d]", i), Reason: err.Error()}
		}
		if _, exists := seenSlots[input.References[i].Slot]; exists {
			return &ValidationError{Field: fmt.Sprintf("references[%d].slot", i), Reason: "slot must be unique"}
		}
		seenSlots[input.References[i].Slot] = struct{}{}
		if err := validateReference(reference, fmt.Sprintf("references[%d]", i)); err != nil {
			return err
		}
	}
	if input.Mask != nil {
		if err := normalizeReference(input.Mask, 0, "mask"); err != nil {
			return &ValidationError{Field: "mask", Reason: err.Error()}
		}
		if err := validateReference(*input.Mask, "mask"); err != nil {
			return err
		}
	}
	return nil
}

func validateVideoInput(input VideoInput) error {
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return err
	}
	if err := validatePrompt(input.Prompt); err != nil {
		return err
	}
	if input.Duration < 0 {
		return &ValidationError{Field: "duration", Reason: "must be non-negative when provided"}
	}
	if input.FirstFrame != nil {
		if err := normalizeReference(input.FirstFrame, 0, "first_frame"); err != nil {
			return &ValidationError{Field: "first_frame", Reason: err.Error()}
		}
		if err := validateReference(*input.FirstFrame, "first_frame"); err != nil {
			return err
		}
	}
	return nil
}

// normalizeReference fills the stable manifest fields that are omitted by a
// caller using the ergonomic URL/base64 form. The resulting source_type and
// slot are included in the request digest by the API and therefore must be
// deterministic across retries.
func normalizeReference(reference *ReferenceInput, index int, defaultRole string) error {
	if reference == nil {
		return fmt.Errorf("reference must not be null")
	}
	if reference.SourceType == "" {
		switch {
		case strings.TrimSpace(reference.URL) != "":
			reference.SourceType = "url"
		case strings.TrimSpace(reference.ContentBase64) != "":
			reference.SourceType = "file"
		default:
			return fmt.Errorf("set url or content_base64")
		}
	}
	reference.SourceType = strings.ToLower(strings.TrimSpace(reference.SourceType))
	switch reference.SourceType {
	case "url", "file":
	default:
		return fmt.Errorf("source_type must be url or file")
	}
	if strings.TrimSpace(reference.Role) == "" && defaultRole != "" {
		reference.Role = defaultRole
	}
	reference.Role = strings.TrimSpace(reference.Role)
	reference.URL = strings.TrimSpace(reference.URL)
	reference.Name = strings.TrimSpace(reference.Name)
	reference.ContentType = strings.TrimSpace(reference.ContentType)
	reference.ContentBase64 = strings.TrimSpace(reference.ContentBase64)
	if len(reference.Role) > 80 || strings.ContainsAny(reference.Role, "\r\n\x00") {
		return fmt.Errorf("role is invalid")
	}
	if reference.Slot < 0 || reference.Slot > maxReferenceSlot {
		return fmt.Errorf("slot must be between 0 and %d", maxReferenceSlot)
	}
	if reference.Slot == 0 && !reference.slotSet && index > 0 {
		reference.Slot = index
	}
	return nil
}

func hasInlineReference(references []ReferenceInput) bool {
	for _, reference := range references {
		if strings.TrimSpace(reference.ContentBase64) != "" {
			return true
		}
	}
	return false
}

func hasInlineReferencePtr(reference *ReferenceInput) bool {
	return reference != nil && strings.TrimSpace(reference.ContentBase64) != ""
}

func referenceContentMaxBytes(field string) int {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(field)), "mask") {
		return maxInlineMaskBytes
	}
	return maxInlineReferenceBytes
}

func parseArguments(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	if len(raw) > maxToolRequestBytes {
		return nil, &ValidationError{Reason: "arguments are too large"}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, &ValidationError{Reason: "arguments must be a JSON object"}
	}
	return args, nil
}

func requiredString(args map[string]json.RawMessage, key string) (string, error) {
	raw, ok := args[key]
	if !ok {
		return "", &ValidationError{Field: key, Reason: "is required"}
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &ValidationError{Field: key, Reason: "must be a string"}
	}
	if strings.TrimSpace(value) == "" {
		return "", &ValidationError{Field: key, Reason: "must not be empty"}
	}
	return strings.TrimSpace(value), nil
}

func optionalString(args map[string]json.RawMessage, key string) (string, error) {
	raw, ok := args[key]
	if !ok || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", &ValidationError{Field: key, Reason: "must be a string"}
	}
	return strings.TrimSpace(value), nil
}

func optionalIntPresence(args map[string]json.RawMessage, key string) (int, bool, error) {
	raw, ok := args[key]
	if !ok || string(raw) == "null" {
		return 0, false, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, true, &ValidationError{Field: key, Reason: "must be an integer"}
	}
	return value, true, nil
}

func optionalReference(args map[string]json.RawMessage, key string) (*ReferenceInput, error) {
	raw, ok := args[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var reference ReferenceInput
	if err := json.Unmarshal(raw, &reference); err != nil {
		return nil, &ValidationError{Field: key, Reason: "must be an object"}
	}
	return &reference, nil
}

func optionalReferences(args map[string]json.RawMessage, key string) ([]ReferenceInput, error) {
	raw, ok := args[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var references []ReferenceInput
	if err := json.Unmarshal(raw, &references); err != nil {
		return nil, &ValidationError{Field: key, Reason: "must be an array of objects"}
	}
	return references, nil
}
