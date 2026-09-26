package server

import (
	"bytes"
	"encoding/json"
	"strings"
)

func decodeResponsesJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// Generations are side effects, and a task query must see the current status.
// Media models using the existing Responses/Chat surfaces bypass response caches.
func mediaRequestBypassesCache(call CallContext, payload any) bool {
	switch payload.(type) {
	case ResponsesRequest, ChatCompletionRequest:
	default:
		return false
	}
	modalities := append([]string{call.Model.Modality}, call.Model.OutputModalities...)
	for _, modality := range modalities {
		switch strings.ToLower(modality) {
		case "image", "audio", "video", "music":
			return true
		}
	}
	return false
}
