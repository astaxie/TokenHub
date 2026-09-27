package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMediaPostHookJSONResponseLimit(t *testing.T) {
	payload := json.RawMessage(`{"data":[{"b64_json":"Zml4dHVyZQ=="}],"id":9007199254740993}`)
	for _, limit := range []int{len(payload) - 1, len(payload), len(payload) + 1} {
		data, err := encodeMediaPostHookResponse(payload, true, limit)
		if limit < len(payload) {
			if err == nil || AsHTTPError(err).Status != http.StatusBadGateway || len(data) != 0 {
				t.Errorf("oversized JSON accepted: limit=%d bytes=%d err=%v", limit, len(data), err)
			}
		} else if err != nil || !bytes.Equal(data, payload) {
			t.Errorf("valid JSON changed: limit=%d body=%s err=%v", limit, data, err)
		}
	}
}

func TestMediaPostHookBinaryResponseLimit(t *testing.T) {
	const limit = 64
	for _, size := range []int{0, 1, 2, 3, limit - 1, limit, limit + 1, limit + 2, limit + 3, 2 * limit} {
		original := bytes.Repeat([]byte{255}, size)
		encoded := base64.StdEncoding.EncodeToString(original)
		for _, multiline := range []bool{false, true} {
			value := encoded
			if multiline {
				value = strings.Join(strings.Split(encoded, ""), "\r\n")
			}
			data, err := encodeMediaPostHookResponse(map[string]any{"data_base64": value}, false, limit)
			if size > limit {
				if err == nil || AsHTTPError(err).Status != http.StatusBadGateway || len(data) != 0 {
					t.Errorf("oversized binary accepted: size=%d multiline=%t bytes=%d err=%v", size, multiline, len(data), err)
				}
			} else if err != nil || !bytes.Equal(data, original) {
				t.Errorf("valid binary changed: size=%d multiline=%t bytes=%d err=%v", size, multiline, len(data), err)
			}
		}
	}
}
