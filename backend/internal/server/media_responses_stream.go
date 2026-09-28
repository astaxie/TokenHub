package server

import (
	"io"
	"net/http"
	"strings"
)

func consumeRoutedResponsesStream(call CallContext, provider Provider, body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	if !modelHasMediaOutput(call.Model) {
		return consumeCodexResponsesStream(body, destination)
	}
	return consumeMediaResponsesStream(provider, body, destination)
}

// Media Responses include native Wan chunks ending in [DONE] and MiniMax
// audio chunks ending in data.status=2, as well as standard Responses events.
// Require a terminal marker so a dropped connection cannot become a success.
func consumeMediaResponsesStream(provider Provider, body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	decoder := newSSEDecoder(body)
	decoder.assembler.limit = maxMediaResponseBytes
	var response map[string]any
	var usage Usage
	for {
		event, err := decoder.Next()
		if err != nil {
			usage.MeteringInvalid = true
			if err == io.EOF {
				err = NewHTTPError(http.StatusBadGateway, "media_stream_incomplete", "Media stream ended before a completion event")
			}
			return response, "", usage, err
		}
		var payload map[string]any
		data := strings.TrimSpace(event.Data)
		invalid := false
		if data != "" && data != "[DONE]" {
			invalid = decodeResponsesJSON([]byte(data), &payload) != nil || payload == nil
		}
		if reported, ok := payload["usage"].(map[string]any); ok && len(reported) > 0 {
			usage = usageFromMap(payload)
		}
		if nested, ok := payload["response"].(map[string]any); ok {
			response = nested
			if reported, ok := nested["usage"].(map[string]any); ok && len(reported) > 0 {
				usage = usageFromMap(nested)
			}
		} else if payload != nil {
			response = payload
		}
		if invalid {
			usage.MeteringInvalid = true
			return response, "", usage, NewHTTPError(http.StatusBadGateway, "invalid_media_response", "Media provider returned an invalid JSON stream event")
		}
		// MiniMax music also reports terminal failures as status/message events.
		status, _ := payload["status"].(string)
		failed := providerStreamEventIsError(event) || status == "failed"
		output := event.Raw
		if failed {
			output = redactProviderStreamEventSecrets(event, provider)
		}
		if destination != nil {
			if _, err := destination.Write(output); err != nil {
				return response, "", usage, err
			}
			if flusher, ok := destination.(streamFlusher); ok {
				flusher.Flush()
			}
		}
		if failed {
			if err, ok := openAIErrorFrame(event, provider); ok {
				return response, "", usage, err
			}
			return response, "", usage, NewHTTPError(http.StatusBadGateway, "provider_stream_error", "Media provider reported a failed stream")
		}
		if mediaResponsesStreamCompleted(event, payload) {
			return response, codexResponseOutputText(response), usage, nil
		}
	}
}

func mediaResponsesStreamCompleted(event serverSentEvent, payload map[string]any) bool {
	if strings.TrimSpace(event.Data) == "[DONE]" {
		return true
	}
	if payload == nil {
		return false
	}
	for _, eventType := range []string{event.Event, guardrailStringValue(payload["type"])} {
		switch eventType {
		case "response.completed", "response.done", "response.incomplete":
			return true
		}
	}
	data, ok := payload["data"].(map[string]any)
	return ok && int64FromAny(data["status"]) == 2
}
