package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func encodeMediaPostHookResponse(payload any, jsonResponse bool, limit int) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if jsonResponse {
		if len(data) > limit {
			return nil, mediaPostHookResponseTooLarge()
		}
		return data, nil
	}
	var wrapped struct {
		Data *string `json:"data_base64"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil || wrapped.Data == nil {
		return nil, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Media response hooks must preserve a string data_base64 field")
	}
	encoded := *wrapped.Data
	encodedBytes := len(encoded) - strings.Count(encoded, "\r") - strings.Count(encoded, "\n")
	// DecodedLen includes at most two padding bytes. Reject clearly oversized
	// output before decoding, then enforce the exact bound on decoded bytes.
	if base64.StdEncoding.DecodedLen(encodedBytes) > limit+2 {
		return nil, mediaPostHookResponseTooLarge()
	}
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	data, err = io.ReadAll(io.LimitReader(decoder, int64(limit)+1))
	if len(data) > limit {
		return nil, mediaPostHookResponseTooLarge()
	}
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Media response hooks must return valid base64 data")
	}
	return data, nil
}

func mediaPostHookResponseTooLarge() error {
	return NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Media response hook output exceeds the response size limit")
}
