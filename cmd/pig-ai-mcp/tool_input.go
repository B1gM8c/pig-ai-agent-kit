package main

import (
	"encoding/json"
	"strings"
)

func parseBillingInput(args map[string]json.RawMessage) (map[string]any, error) {
	if err := rejectUnknownArgs(args, "provider_id", "model", "mode", "size", "quality", "n", "workflow", "resolution", "width", "height", "duration", "aspect_ratio", "transitions", "reference_image_count", "reference_video_count", "reference_audio_count"); err != nil {
		return nil, err
	}
	input := map[string]any{}
	mode, modeErr := requiredString(args, "mode")
	if modeErr != nil {
		return nil, modeErr
	}
	switch mode {
	case "text_to_image", "image_to_image", "text_to_video", "image_to_video", "reference_to_video":
	default:
		return nil, &ValidationError{Field: "mode", Reason: "must be text_to_image, image_to_image, text_to_video, image_to_video, or reference_to_video"}
	}
	input["mode"] = mode
	for _, key := range []string{"provider_id", "model", "size", "quality", "workflow", "resolution", "aspect_ratio"} {
		value, err := optionalString(args, key)
		if err != nil {
			return nil, err
		}
		if value != "" {
			input[key] = value
		}
	}
	n, provided, err := optionalIntPresence(args, "n")
	if err != nil {
		return nil, err
	}
	if !provided {
		return nil, &ValidationError{Field: "n", Reason: "is required"}
	}
	if provided {
		if n == 0 {
			return nil, &ValidationError{Field: "n", Reason: "must be between 1 and 4 when provided"}
		}
		if n < 1 || n > 4 {
			return nil, &ValidationError{Field: "n", Reason: "must be between 1 and 4"}
		}
		if (mode == "text_to_video" || mode == "image_to_video" || mode == "reference_to_video") && n != 1 {
			return nil, &ValidationError{Field: "n", Reason: "must be 1 for video modes"}
		}
		input["n"] = n
	}
	for _, key := range []string{"width", "height", "duration", "reference_image_count", "reference_video_count", "reference_audio_count"} {
		value, err := optionalInt(args, key)
		if err != nil {
			return nil, err
		}
		if value < 0 {
			return nil, &ValidationError{Field: key, Reason: "must not be negative"}
		}
		limit := map[string]int{"reference_image_count": 30, "reference_video_count": 10, "reference_audio_count": 10}[key]
		if limit > 0 && value > limit {
			return nil, &ValidationError{Field: key, Reason: "exceeds the public reference count limit"}
		}
		if value != 0 {
			input[key] = value
		}
	}
	transitions, err := parseTransitions(args)
	if err != nil {
		return nil, err
	}
	if len(transitions) > 0 {
		input["transitions"] = transitions
	}
	return input, nil
}

func parseImageInput(args map[string]json.RawMessage) (ImageInput, error) {
	if err := rejectUnknownArgs(args, "prompt", "idempotency_key", "provider_id", "model", "mode", "size", "quality", "n", "references", "mask", "quote_id", "workflow", "resolution", "width", "height"); err != nil {
		return ImageInput{}, err
	}
	prompt, err := requiredString(args, "prompt")
	if err != nil {
		return ImageInput{}, err
	}
	key, err := requiredString(args, "idempotency_key")
	if err != nil {
		return ImageInput{}, err
	}
	mode, err := requiredString(args, "mode")
	if err != nil {
		return ImageInput{}, err
	}
	if mode != "text_to_image" && mode != "image_to_image" {
		return ImageInput{}, &ValidationError{Field: "mode", Reason: "must be text_to_image or image_to_image"}
	}
	input := ImageInput{Prompt: prompt, IdempotencyKey: key}
	if err := fillImageVideoStrings(args, &input); err != nil {
		return ImageInput{}, err
	}
	input.Width, err = optionalInt(args, "width")
	if err != nil {
		return ImageInput{}, err
	}
	input.Height, err = optionalInt(args, "height")
	if err != nil {
		return ImageInput{}, err
	}
	input.N, input.nSet, err = optionalIntPresence(args, "n")
	if err != nil {
		return ImageInput{}, err
	}
	if !input.nSet {
		return ImageInput{}, &ValidationError{Field: "n", Reason: "is required"}
	}
	input.Mode = mode
	input.References, err = optionalReferences(args, "references")
	if err != nil {
		return ImageInput{}, err
	}
	input.Mask, err = optionalReference(args, "mask")
	if err != nil {
		return ImageInput{}, err
	}
	return input, nil
}

func parseVideoInput(args map[string]json.RawMessage) (VideoInput, error) {
	if err := rejectUnknownArgs(args, "prompt", "idempotency_key", "provider_id", "model", "aspect_ratio", "resolution", "duration", "first_frame", "last_frame", "reference_images", "keyframes", "reference_audios", "quote_id", "workflow", "transitions", "reference_video_urls", "reference_audio_urls"); err != nil {
		return VideoInput{}, err
	}
	prompt, err := requiredString(args, "prompt")
	if err != nil {
		return VideoInput{}, err
	}
	key, err := requiredString(args, "idempotency_key")
	if err != nil {
		return VideoInput{}, err
	}
	input := VideoInput{Prompt: prompt, IdempotencyKey: key}
	if err := fillImageVideoStrings(args, &input); err != nil {
		return VideoInput{}, err
	}
	input.Transitions, err = parseTransitions(args)
	if err != nil {
		return VideoInput{}, err
	}
	for field, target := range map[string]*[]string{"reference_video_urls": &input.ReferenceVideoURLs, "reference_audio_urls": &input.ReferenceAudioURLs} {
		if raw, ok := args[field]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, target); err != nil {
				return VideoInput{}, &ValidationError{Field: field, Reason: "must be an array of URLs"}
			}
		}
	}
	input.Duration, err = optionalInt(args, "duration")
	if err != nil {
		return VideoInput{}, err
	}
	input.FirstFrame, err = optionalReference(args, "first_frame")
	if err != nil {
		return VideoInput{}, err
	}
	input.LastFrame, err = optionalReference(args, "last_frame")
	if err != nil {
		return VideoInput{}, err
	}
	input.ReferenceImages, err = optionalReferences(args, "reference_images")
	if err != nil {
		return VideoInput{}, err
	}
	if raw, ok := args["keyframes"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &input.Keyframes); err != nil {
			return VideoInput{}, &ValidationError{Field: "keyframes", Reason: "must be an array of objects"}
		}
	}
	if raw, ok := args["reference_audios"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &input.ReferenceAudios); err != nil {
			return VideoInput{}, &ValidationError{Field: "reference_audios", Reason: "must be an array of objects"}
		}
	}
	return input, nil
}

func fillImageVideoStrings[T interface {
	*ImageInput | *VideoInput
}](args map[string]json.RawMessage, input T) error {
	provider, err := optionalString(args, "provider_id")
	if err != nil {
		return err
	}
	model, err := optionalString(args, "model")
	if err != nil {
		return err
	}
	quoteID, err := optionalString(args, "quote_id")
	if err != nil {
		return err
	}
	workflow, err := optionalString(args, "workflow")
	if err != nil {
		return err
	}
	switch value := any(input).(type) {
	case *ImageInput:
		value.QuoteID, value.Workflow = quoteID, workflow
		value.Resolution, err = optionalString(args, "resolution")
		if err != nil {
			return err
		}
		mode, err := optionalString(args, "mode")
		if err != nil {
			return err
		}
		value.ProviderID, value.Model, value.Mode = provider, model, mode
		value.Size, err = optionalString(args, "size")
		if err != nil {
			return err
		}
		value.Quality, err = optionalString(args, "quality")
		return err
	case *VideoInput:
		value.QuoteID, value.Workflow = quoteID, workflow
		aspect, err := optionalString(args, "aspect_ratio")
		if err != nil {
			return err
		}
		resolution, err := optionalString(args, "resolution")
		if err != nil {
			return err
		}
		value.ProviderID, value.Model = provider, model
		value.AspectRatio, value.Resolution = aspect, resolution
		return nil
	default:
		return &ValidationError{Reason: "invalid media input"}
	}
}

func rejectUnknownArgs(args map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range args {
		if _, ok := set[key]; !ok {
			return &ValidationError{Field: key, Reason: "is not supported by this tool"}
		}
	}
	return nil
}

func parseTransitions(args map[string]json.RawMessage) ([]VideoTransitionInput, error) {
	raw, ok := args["transitions"]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var transitions []VideoTransitionInput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transitions); err != nil {
		return nil, &ValidationError{Field: "transitions", Reason: "must be an array of prompt/duration objects"}
	}
	if err := validateTransitions(transitions); err != nil {
		return nil, err
	}
	return transitions, nil
}
