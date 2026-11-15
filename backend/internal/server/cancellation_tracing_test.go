package server

import (
	"testing"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestCancellationTracing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       string
		level      string
		spanStatus tracepb.Status_StatusCode
	}{
		{"caller", 499, "request_cancelled", "DEFAULT", tracepb.Status_STATUS_CODE_UNSET},
		{"unknown 499", 499, "unknown", "ERROR", tracepb.Status_STATUS_CODE_ERROR},
		{"internal cancellation", 500, "internal_error", "ERROR", tracepb.Status_STATUS_CODE_ERROR},
		{"timeout", 504, "provider_upstream_timeout", "ERROR", tracepb.Status_STATUS_CODE_ERROR},
		{"worker lost", 499, "response_cancelled_worker_lost", "ERROR", tracepb.Status_STATUS_CODE_ERROR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := newFakeOTLPEndpoint(t)
			app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))
			now := time.Now().UTC()
			app.finishCall(GatewayCallCompletion{
				Call:       CallContext{RequestID: "trace-cancellation", Model: Model{Name: "model"}, StartedAt: now.Add(-time.Second)},
				StatusCode: tc.status, ErrorCode: tc.code, ErrorMessage: "private error payload", FinishedAt: now,
				RequestPayload: map[string]any{"prompt": "private prompt"}, ResponsePayload: map[string]any{"content": "private output"},
				Usage: Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, CostUSD: 0.25},
				Attempts: []RouteAttempt{{Invoked: true, Status: tc.status, ErrorCode: tc.code, Error: "private attempt payload",
					StartedAt: now.Add(-time.Second), EndedAt: now, Usage: Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5, CostUSD: 0.25}}},
			})
			flushTracing(t, app)
			spans := endpoint.collected()
			if len(spans) != 2 {
				t.Fatalf("exported %d spans, want root and attempt", len(spans))
			}
			for _, span := range spans {
				if level := spanString(t, span, attrObservationLevel); level != tc.level || span.GetStatus().GetCode() != tc.spanStatus {
					t.Fatalf("span %q level=%s status=%v", span.GetName(), level, span.GetStatus())
				}
				if code := spanString(t, span, "error.type"); code != tc.code {
					t.Fatalf("span error code=%s, want %s", code, tc.code)
				}
				status, ok := spanAttribute(span, "http.response.status_code")
				if !ok || status.GetIntValue() != int64(tc.status) {
					t.Fatalf("span status attribute=%v", status)
				}
				for _, private := range []string{attrObservationInput, attrObservationOutput, "error.message"} {
					if hasAttribute(span, private) {
						t.Fatalf("span exposed %s", private)
					}
				}
				if spanString(t, span, attrObservationType) == observationTypeGeneration {
					if usage := spanString(t, span, attrUsageDetails); usage != `{"input":3,"output":2,"total":5}` {
						t.Fatalf("usage details=%s", usage)
					}
					if cost := spanString(t, span, attrCostDetails); cost != `{"total":0.25}` {
						t.Fatalf("cost details=%s", cost)
					}
				} else if hasAttribute(span, attrUsageDetails) || hasAttribute(span, attrCostDetails) {
					t.Fatal("root duplicates attempt usage or cost")
				}
			}
		})
	}
}
