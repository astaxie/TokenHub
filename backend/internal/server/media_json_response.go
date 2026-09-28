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
	// ParseMediaType retains a valid base type when only parameters are invalid.
	// Broken parameters must not bypass JSON validation or metering.
	mediaType, _, _ := mime.ParseMediaType(contentType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// Direct media JSON must contain one complete object. Retain independently
// reported usage if trailing data makes an otherwise complete response invalid.
func inspectMediaJSON(body []byte) (Usage, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	err := decoder.Decode(&payload)
	usage := usageFromMap(payload)
	var extra any
	if err != nil || payload == nil || decoder.Decode(&extra) != io.EOF {
		usage.MeteringInvalid = true
		return usage, NewHTTPError(http.StatusBadGateway, "invalid_media_response", "Provider returned invalid JSON")
	}
	// A successful HTTP status cannot make an OpenAI error envelope successful.
	// Keep the message generic because upstream errors may echo provider secrets.
	if payload["error"] != nil {
		return usage, NewHTTPError(http.StatusBadGateway, "provider_error", "Media provider returned an error response")
	}
	return usage, nil
}
