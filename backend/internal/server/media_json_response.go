package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

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
	return usage, nil
}
