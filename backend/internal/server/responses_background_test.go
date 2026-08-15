package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression: ResponsesRequest.MarshalJSON must not force `background:false`
// onto the wire when the client never sent the field. Strict-whitelist
// upstreams reject the mere presence of an unimplemented parameter, so a
// default-valued field must be omitted. See issue #228.
func TestResponsesRequestMarshalOmitsDefaultBackground(t *testing.T) {
	request := responsesRequestFromJSON(t, `{"model":"gpt-x","input":"hi","stream":true}`)

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), `"background"`) {
		t.Fatalf("default background must be omitted, got: %s", encoded)
	}
}

func TestResponsesRequestMarshalKeepsExplicitBackgroundTrue(t *testing.T) {
	request := responsesRequestFromJSON(t, `{"model":"gpt-x","input":"hi","background":true}`)

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(encoded), `"background":true`) {
		t.Fatalf("explicit background=true must be preserved, got: %s", encoded)
	}
}
