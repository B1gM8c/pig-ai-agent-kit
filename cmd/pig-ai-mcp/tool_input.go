package main

import "encoding/json"

func parseBillingInput(args map[string]json.RawMessage) (map[string]any, error) {
	if err := rejectUnknownArgs(args, "provider_id", "model", "mode", "size", "quality", "n"); err != nil {
		return nil, err
	}
	input := map[string]any{}
	mode, modeErr := requiredString(args, "mode")
	if modeErr != nil {
		return nil, modeErr
	}
	switch mode {
	case "text_to_image", "image_to_image", "text_to_video", "image_to_video":
	default:
		return nil, &ValidationError{Field: "mode", Reason: "must be text_to_image, image_to_image, text_to_video, or image_to_video"}
	}
	input["mode"] = mode
	for _, key := range []string{"provider_id", "model", "size", "quality"} {
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
		if (mode == "text_to_video" || mode == "image_to_video") && n != 1 {
			return nil, &ValidationError{Field: "n", Reason: "must be 1 for video modes"}
		}
		input["n"] = n
	}
	return input, nil
}

func parseImageInput(args map[string]json.RawMessage) (ImageInput, error) {
	if err := rejectUnknownArgs(args, "prompt", "idempotency_key", "provider_id", "model", "mode", "size", "quality", "n", "references", "mask"); err != nil {
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
	if err := rejectUnknownArgs(args, "prompt", "idempotency_key", "provider_id", "model", "aspect_ratio", "resolution", "duration", "first_frame"); err != nil {
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
	input.Duration, _, err = optionalIntPresence(args, "duration")
	if err != nil {
		return VideoInput{}, err
	}
	input.FirstFrame, err = optionalReference(args, "first_frame")
	if err != nil {
		return VideoInput{}, err
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
	switch value := any(input).(type) {
	case *ImageInput:
		value.ProviderID, value.Model = provider, model
		value.Size, err = optionalString(args, "size")
		if err != nil {
			return err
		}
		value.Quality, err = optionalString(args, "quality")
		return err
	case *VideoInput:
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
