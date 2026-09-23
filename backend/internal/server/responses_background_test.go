package server

import (
	"encoding/json"
	"testing"
)

func TestResponsesRequestMarshalPreservesBackgroundPresence(t *testing.T) {
	testCases := []struct {
		name        string
		payload     string
		wantPresent bool
		wantValue   string
	}{
		{name: "omitted", payload: `{"model":"gpt-x","input":"hi"}`},
		{name: "false", payload: `{"model":"gpt-x","input":"hi","background":false}`, wantPresent: true, wantValue: "false"},
		{name: "true", payload: `{"model":"gpt-x","input":"hi","background":true}`, wantPresent: true, wantValue: "true"},
		{name: "null", payload: `{"model":"gpt-x","input":"hi","background":null}`, wantPresent: true, wantValue: "null"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := responsesRequestFromJSON(t, testCase.payload)
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("marshal Responses request: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatalf("decode marshaled Responses request: %v", err)
			}
			value, present := fields["background"]
			if present != testCase.wantPresent {
				t.Fatalf("background presence = %t, want %t: %s", present, testCase.wantPresent, encoded)
			}
			if present && string(value) != testCase.wantValue {
				t.Fatalf("background value = %s, want %s", value, testCase.wantValue)
			}
		})
	}
}
