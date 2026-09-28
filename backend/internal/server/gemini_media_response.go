package server

import (
	"io"
	"strings"
)

// Gemini reports cumulative usage snapshots, sometimes after its final candidate.
func inspectGeminiMediaResponse(result mediaResponse, stream bool) (Usage, error) {
	metadata := map[string]any{}
	finished := map[int64]bool{}
	blocked := false
	inspect := func(data []byte) error {
		var payload map[string]any
		err := decodeResponsesJSON(data, &payload)
		if reported, ok := payload["usageMetadata"].(map[string]any); ok {
			for key, value := range reported {
				if value != nil {
					metadata[key] = value
				}
			}
		}
		if err != nil || payload == nil {
			return invalidGeminiMediaResponse()
		}
		if payload["error"] != nil {
			return NewHTTPError(502, "provider_error", "Gemini media provider returned an error response")
		}
		if geminiPromptBlockReason(payload) != "" {
			blocked = true
		}
		candidates, _ := payload["candidates"].([]any)
		for i, item := range candidates {
			candidate, ok := item.(map[string]any)
			if !ok {
				return invalidGeminiMediaResponse()
			}
			index := int64(i)
			if value, exists := candidate["index"]; exists {
				index = int64FromAny(value)
			}
			reason, _ := candidate["finishReason"].(string)
			finished[index] = finished[index] || strings.TrimSpace(reason) != ""
		}
		return nil
	}
	var err error
	if stream {
		if !mediaResponseIsSSE(result.ContentType) {
			return Usage{MeteringInvalid: true}, invalidGeminiMediaResponse()
		}
		decoder := newMediaSSEDecoder(result.Body)
		for {
			event, readErr := decoder.Next()
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				err = readErr
				break
			}
			if strings.TrimSpace(event.Data) != "" && strings.TrimSpace(event.Data) != "[DONE]" {
				if err = inspect([]byte(event.Data)); err != nil {
					break
				}
			}
			if sseEventNameIsError(event.Event) {
				err = NewHTTPError(502, "provider_stream_error", "Gemini media provider reported a failed stream")
				break
			}
		}
	} else if !mediaResponseIsJSON(result.ContentType) {
		err = invalidGeminiMediaResponse()
	} else {
		err = inspect(result.Body)
	}
	usage := geminiUsage(map[string]any{"usageMetadata": metadata})
	if err == nil && !blocked {
		if len(finished) == 0 {
			err = invalidGeminiMediaResponse()
		}
		if stream {
			for _, complete := range finished {
				if !complete {
					err = NewHTTPError(502, "media_stream_incomplete", "Gemini media stream ended before a finish reason")
				}
			}
		}
	}
	if err != nil {
		usage.MeteringInvalid = true
	}
	return usage, err
}

func invalidGeminiMediaResponse() error {
	return NewHTTPError(502, "invalid_media_response", "Gemini media provider returned an invalid response")
}
