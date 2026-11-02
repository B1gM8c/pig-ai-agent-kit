package main

import (
	"encoding/json"
	"fmt"
	"math"
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
	return validateReferenceWithMaxSlot(reference, field, maxReferenceSlot)
}

func validateReferenceWithMaxSlot(reference ReferenceInput, field string, maxSlot int) error {
	if err := normalizeReferenceWithMaxSlot(&reference, 0, "", maxSlot); err != nil {
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
	if input.Width < 0 || input.Height < 0 {
		return &ValidationError{Field: "width/height", Reason: "must not be negative"}
	}
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
	if err := validateTransitions(input.Transitions); err != nil {
		return err
	}
	for field, urls := range map[string][]string{"reference_video_urls": input.ReferenceVideoURLs, "reference_audio_urls": input.ReferenceAudioURLs} {
		if len(urls) > 10 {
			return &ValidationError{Field: field, Reason: "must contain at most 10 URLs"}
		}
		for i, raw := range urls {
			if err := validateReference(ReferenceInput{SourceType: "url", URL: raw}, fmt.Sprintf("%s[%d]", field, i)); err != nil {
				return err
			}
		}
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return err
	}
	if input.Workflow == "multiframe2video" {
		if strings.TrimSpace(input.Prompt) != "" {
			return &ValidationError{Field: "prompt", Reason: "must be empty for multiframe2video; use transitions[].prompt"}
		}
	} else if err := validatePrompt(input.Prompt); err != nil {
		return err
	}
	if input.Duration < 0 || input.Duration > 3600 {
		return &ValidationError{Field: "duration", Reason: "must be between 0 and 3600 seconds when provided"}
	}
	if input.FirstFrame != nil {
		if err := normalizeReference(input.FirstFrame, 0, "first_frame"); err != nil {
			return &ValidationError{Field: "first_frame", Reason: err.Error()}
		}
		if err := validateReference(*input.FirstFrame, "first_frame"); err != nil {
			return err
		}
	}
	if input.LastFrame != nil {
		if err := normalizeReference(input.LastFrame, 0, "last_frame"); err != nil {
			return &ValidationError{Field: "last_frame", Reason: err.Error()}
		}
		if err := validateReference(*input.LastFrame, "last_frame"); err != nil {
			return err
		}
	}
	if len(input.ReferenceImages) > maxVideoReferences {
		return &ValidationError{Field: "reference_images", Reason: fmt.Sprintf("must contain at most %d items", maxVideoReferences)}
	}
	seenSlots := make(map[int]struct{}, len(input.ReferenceImages))
	for index := range input.ReferenceImages {
		if err := normalizeReferenceWithMaxSlot(&input.ReferenceImages[index], index, "reference", maxVideoReferenceSlot); err != nil {
			return &ValidationError{Field: fmt.Sprintf("reference_images[%d]", index), Reason: err.Error()}
		}
		if _, exists := seenSlots[input.ReferenceImages[index].Slot]; exists {
			return &ValidationError{Field: fmt.Sprintf("reference_images[%d].slot", index), Reason: "slot must be unique"}
		}
		seenSlots[input.ReferenceImages[index].Slot] = struct{}{}
		if err := validateReferenceWithMaxSlot(input.ReferenceImages[index], fmt.Sprintf("reference_images[%d]", index), maxVideoReferenceSlot); err != nil {
			return err
		}
	}
	if len(input.Keyframes) > 4 {
		return &ValidationError{Field: "keyframes", Reason: "must contain at most 4 items"}
	}
	previousTick := int64(0)
	for index := range input.Keyframes {
		if err := normalizeReferenceWithMaxSlot(&input.Keyframes[index].Image, index, "keyframe", 3); err != nil {
			return &ValidationError{Field: fmt.Sprintf("keyframes[%d].image", index), Reason: err.Error()}
		}
		if err := validateReferenceWithMaxSlot(input.Keyframes[index].Image, fmt.Sprintf("keyframes[%d].image", index), 3); err != nil {
			return err
		}
		timestamp := input.Keyframes[index].TimestampS
		scaled := timestamp * 3
		tick := math.Round(scaled)
		if math.IsNaN(timestamp) || math.IsInf(timestamp, 0) || math.Abs(scaled-tick) > 1e-9 || tick < 1 || int64(tick) <= previousTick || input.Duration > 0 && tick >= float64(input.Duration*3) {
			return &ValidationError{Field: fmt.Sprintf("keyframes[%d].timestamp_s", index), Reason: "must be strictly increasing, inside duration, and on the 1/3-second grid"}
		}
		previousTick = int64(tick)
		input.Keyframes[index].TimestampS = tick / 3
	}
	imageInputCount := len(input.ReferenceImages) + len(input.Keyframes)
	if input.FirstFrame != nil {
		imageInputCount++
	}
	if input.LastFrame != nil {
		imageInputCount++
	}
	if imageInputCount > 9 {
		return &ValidationError{Field: "keyframes", Reason: "first_frame, last_frame, reference_images, and keyframes may contain at most 9 image inputs in total"}
	}
	if len(input.ReferenceAudios) > maxVideoVoices {
		return &ValidationError{Field: "reference_audios", Reason: fmt.Sprintf("must contain at most %d items", maxVideoVoices)}
	}
	for index := range input.ReferenceAudios {
		voiceID := strings.TrimSpace(input.ReferenceAudios[index].VoiceID)
		if voiceID == "" || len(voiceID) > 64 || strings.IndexFunc(voiceID, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return &ValidationError{Field: fmt.Sprintf("reference_audios[%d].voice_id", index), Reason: "must be a valid preset voice identifier"}
		}
		input.ReferenceAudios[index].VoiceID = voiceID
	}
	return nil
}

// normalizeReference fills the stable manifest fields that are omitted by a
// caller using the ergonomic URL/base64 form. The resulting source_type and
// slot are included in the request digest by the API and therefore must be
// deterministic across retries.
func normalizeReference(reference *ReferenceInput, index int, defaultRole string) error {
	return normalizeReferenceWithMaxSlot(reference, index, defaultRole, maxReferenceSlot)
}

func normalizeReferenceWithMaxSlot(reference *ReferenceInput, index int, defaultRole string, maxSlot int) error {
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
	if reference.Slot < 0 || reference.Slot > maxSlot {
		return fmt.Errorf("slot must be between 0 and %d", maxSlot)
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

func validateTransitions(transitions []VideoTransitionInput) error {
	if len(transitions) > 19 {
		return &ValidationError{Field: "transitions", Reason: "must contain at most 19 items"}
	}
	for i, transition := range transitions {
		if math.IsNaN(transition.Duration) || math.IsInf(transition.Duration, 0) || transition.Duration <= 0 {
			return &ValidationError{Field: fmt.Sprintf("transitions[%d].duration", i), Reason: "must be positive"}
		}
	}
	return nil
}

func optionalInt(args map[string]json.RawMessage, key string) (int, error) {
	value, _, err := optionalIntPresence(args, key)
	return value, err
}
