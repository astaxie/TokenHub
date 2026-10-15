package server

import (
	"encoding/json"
	"slices"
)

func jevOutputBudget(raw map[string]json.RawMessage, budget int) int {
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		var value int
		if json.Unmarshal(raw[key], &value) == nil && value > budget {
			budget = value
		}
	}
	return budget
}

func jevInputModalitiesFit(model ProviderModel, input any) bool {
	if len(model.InputModalities) == 0 {
		return true
	}
	data, err := json.Marshal(input)
	if err != nil {
		return false
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	var check func(any) bool
	check = func(value any) bool {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				if !check(child) {
					return false
				}
			}
		case map[string]any:
			modality := ""
			switch item["type"] {
			case "image_url", "input_image":
				modality = "image"
			case "input_audio", "audio":
				modality = "audio"
			case "input_video", "video_url":
				modality = "video"
			}
			if modality != "" && !slices.Contains(model.InputModalities, modality) {
				return false
			}
			for _, child := range item {
				if !check(child) {
					return false
				}
			}
		}
		return true
	}
	return check(value)
}
