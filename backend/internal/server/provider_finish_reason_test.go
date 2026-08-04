package server

import (
	"errors"
	"net/http"
	"testing"
)

// The two finish-reason directions are golden-locked rather than round-tripped:
// the mapping is lossy on purpose. end_turn and stop_sequence both collapse to
// "stop", and max_tokens and model_context_window_exceeded both collapse to
// "length", so feeding an output back through the other direction does not
// return the original value. Each direction is pinned to its own table instead.

func TestAnthropicFinishReasonGolden(t *testing.T) {
	cases := []struct {
		stopReason   string
		hasToolCalls bool
		want         string
	}{
		{stopReason: "", hasToolCalls: false, want: "stop"},
		{stopReason: "", hasToolCalls: true, want: "tool_calls"},
		{stopReason: "end_turn", hasToolCalls: false, want: "stop"},
		{stopReason: "end_turn", hasToolCalls: true, want: "tool_calls"},
		{stopReason: "stop_sequence", hasToolCalls: false, want: "stop"},
		{stopReason: "stop_sequence", hasToolCalls: true, want: "tool_calls"},
		{stopReason: "max_tokens", hasToolCalls: false, want: "length"},
		{stopReason: "max_tokens", hasToolCalls: true, want: "length"},
		{stopReason: "model_context_window_exceeded", hasToolCalls: false, want: "length"},
		{stopReason: "model_context_window_exceeded", hasToolCalls: true, want: "length"},
		{stopReason: "tool_use", hasToolCalls: false, want: "tool_calls"},
		{stopReason: "tool_use", hasToolCalls: true, want: "tool_calls"},
		{stopReason: "refusal", hasToolCalls: false, want: "content_filter"},
		{stopReason: "refusal", hasToolCalls: true, want: "content_filter"},
	}
	for _, tc := range cases {
		got, err := anthropicFinishReason(tc.stopReason, tc.hasToolCalls)
		if err != nil {
			t.Fatalf("anthropicFinishReason(%q, %v) failed: %v", tc.stopReason, tc.hasToolCalls, err)
		}
		if got != tc.want {
			t.Fatalf("anthropicFinishReason(%q, %v) = %q, want %q", tc.stopReason, tc.hasToolCalls, got, tc.want)
		}
	}
}

// A paused server-side tool loop and an unrecognized stop reason are the two
// values this direction refuses to map. Both keep their own classification: the
// pause is a capability gap another candidate may cover, the unknown value is a
// malformed upstream response.
func TestAnthropicFinishReasonGoldenErrors(t *testing.T) {
	cases := []struct {
		stopReason      string
		wantStatus      int
		wantCode        string
		wantDisposition ProviderErrorDisposition
	}{
		{
			stopReason:      "pause_turn",
			wantStatus:      http.StatusNotImplemented,
			wantCode:        "provider_capability_not_supported",
			wantDisposition: ProviderErrorModelUnsupported,
		},
		{
			stopReason: "something_new",
			wantStatus: http.StatusBadGateway,
			wantCode:   "provider_invalid_response",
		},
	}
	for _, tc := range cases {
		for _, hasToolCalls := range []bool{false, true} {
			got, err := anthropicFinishReason(tc.stopReason, hasToolCalls)
			if err == nil {
				t.Fatalf("anthropicFinishReason(%q, %v) = %q, want an error", tc.stopReason, hasToolCalls, got)
			}
			if got != "" {
				t.Fatalf("anthropicFinishReason(%q, %v) returned %q alongside an error", tc.stopReason, hasToolCalls, got)
			}
			httpErr := AsHTTPError(err)
			if httpErr == nil || httpErr.Status != tc.wantStatus || httpErr.Code != tc.wantCode {
				t.Fatalf("anthropicFinishReason(%q, %v) error = %+v, want %d/%s",
					tc.stopReason, hasToolCalls, httpErr, tc.wantStatus, tc.wantCode)
			}
			var invocation *ProviderInvocationError
			if errors.As(err, &invocation) {
				if invocation.Disposition != tc.wantDisposition {
					t.Fatalf("anthropicFinishReason(%q, %v) disposition = %q, want %q",
						tc.stopReason, hasToolCalls, invocation.Disposition, tc.wantDisposition)
				}
			} else if tc.wantDisposition != "" {
				t.Fatalf("anthropicFinishReason(%q, %v) lost disposition %q", tc.stopReason, hasToolCalls, tc.wantDisposition)
			}
		}
	}
}

// The reverse direction is deliberately lenient where the forward one is strict:
// it never fails. An unknown reason degrades to end_turn, and observed tool calls
// outrank every reason including content_filter, because the Anthropic client has
// to be told to run the tools the response already carries.
func TestOpenAIFinishReasonToAnthropicGolden(t *testing.T) {
	cases := []struct {
		reason   string
		hasTools bool
		want     string
	}{
		{reason: "", hasTools: false, want: "end_turn"},
		{reason: "", hasTools: true, want: "tool_use"},
		{reason: "stop", hasTools: false, want: "end_turn"},
		{reason: "stop", hasTools: true, want: "tool_use"},
		{reason: "length", hasTools: false, want: "max_tokens"},
		{reason: "length", hasTools: true, want: "tool_use"},
		{reason: "tool_calls", hasTools: false, want: "tool_use"},
		{reason: "tool_calls", hasTools: true, want: "tool_use"},
		// The legacy function_call reason is mapped like tool_calls.
		{reason: "function_call", hasTools: false, want: "tool_use"},
		{reason: "function_call", hasTools: true, want: "tool_use"},
		{reason: "content_filter", hasTools: false, want: "refusal"},
		{reason: "content_filter", hasTools: true, want: "tool_use"},
		{reason: "something_new", hasTools: false, want: "end_turn"},
		{reason: "something_new", hasTools: true, want: "tool_use"},
	}
	for _, tc := range cases {
		if got := openAIFinishReasonToAnthropic(tc.reason, tc.hasTools); got != tc.want {
			t.Fatalf("openAIFinishReasonToAnthropic(%q, %v) = %q, want %q", tc.reason, tc.hasTools, got, tc.want)
		}
	}
}
