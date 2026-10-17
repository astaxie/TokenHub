package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

func mediaResponseIsJSON(contentType string) bool {
	mediaType := mediaResponseMIMEType(contentType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func mediaResponseMIMEType(contentType string) string {
	// Classify the base type independently: malformed or duplicate parameters
	// must not bypass response validation, metering, or protocol-specific hooks.
	base, _, _ := strings.Cut(contentType, ";")
	mediaType, _, _ := mime.ParseMediaType(base)
	return mediaType
}

// Direct media JSON must contain one complete object. Retain independently
// reported usage if trailing data makes an otherwise complete response invalid.
func inspectMediaJSON(body []byte, endpoints ...string) (Usage, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	err := decoder.Decode(&payload)
	usage := captureAudioDuration(mediaUsageFromMap(payload), payload, audioTranscriptionEndpoint(endpoints))
	var extra any
	if err != nil || payload == nil || decoder.Decode(&extra) != io.EOF {
		usage.MeteringInvalid = true
		return usage, NewHTTPError(http.StatusBadGateway, "invalid_media_response", "Provider returned invalid JSON")
	}
	if err := validateAudioDuration(usage); err != nil {
		return usage, err
	}
	// A successful HTTP status cannot make an OpenAI error envelope successful.
	// Keep the message generic because upstream errors may echo provider secrets.
	if payload["error"] != nil || (int64FromAny(payload["code"]) >= 400 && int64FromAny(payload["code"]) <= 599) {
		return usage, NewHTTPError(http.StatusBadGateway, "provider_error", "Media provider returned an error response")
	}
	return usage, nil
}
