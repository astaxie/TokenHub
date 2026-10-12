package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMediaResponsesRejectMalformedJSONEvents(t *testing.T) {
	const usage = `"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}`
	for _, tc := range []struct {
		name, event string
	}{
		{"trailing object on completion", "event: response.completed\ndata: {\"response\":{" + usage + "}}{}\n\n"},
		{"trailing garbage on error", "data: {\"error\":{\"message\":\"upstream-fixture-secret\"}," + usage + "}garbage\n\n"},
		{"truncated object", "data: {\"text\":\"upstream-fixture-secret\"\n\n"},
		{"null", "data: null\n\n"},
		{"array", "data: []\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			prefix := "data: {" + usage + "}\n\n"
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, prefix+tc.event+"data: [DONE]\n\n")
			}, "image")
			store.AddRoute(ModelRoute{ID: "fallback-malformed-event", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "fixture", "stream": true}, key)
			if calls != 1 || response.Body != prefix {
				t.Errorf("malformed frame forwarded or retried: calls=%d body=%s", calls, response.Body)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway || logs[0].ErrorCode != "invalid_media_response" {
				t.Errorf("malformed event not recorded as failure: %+v", logs)
			}
			if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 10 {
				t.Errorf("reported stream usage lost: %+v", records)
			}
		})
	}
}

func TestMediaResponsesMalformedCompletionRetainsLeadingObjectUsage(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}}{}\n\n"
	var output strings.Builder
	_, _, usage, err := consumeMediaResponsesStream(Provider{}, strings.NewReader(body), &output)
	if err == nil || AsHTTPError(err).Code != "invalid_media_response" || usage.TotalTokens != 10 || !usage.MeteringInvalid || output.Len() != 0 {
		t.Fatalf("malformed completion accepted or usage lost: usage=%+v error=%v output=%s", usage, err, output.String())
	}
}

func TestMediaResponsesJSONValidationPreservesHeartbeatsAndDone(t *testing.T) {
	body := ": keepalive\n\ndata: {\"output\":[],\"usage\":{\"output_tokens\":7,\"total_tokens\":7}} \n\ndata: [DONE]\n\n"
	var output strings.Builder
	_, _, usage, err := consumeMediaResponsesStream(Provider{}, strings.NewReader(body), &output)
	if err != nil || usage.TotalTokens != 7 || output.String() != body {
		t.Fatalf("valid event stream changed: usage=%+v error=%v output=%q", usage, err, output.String())
	}
}
