package server

import "fmt"

// Canonical finish/stop reason strings for the two protocols this package
// translates between. The mapping is lossy in both directions, so the two
// functions below stay explicit rather than being derived from one table:
// end_turn and stop_sequence both become "stop", and max_tokens and
// model_context_window_exceeded both become "length".
const (
	openAIFinishReasonStop          = "stop"
	openAIFinishReasonLength        = "length"
	openAIFinishReasonToolCalls     = "tool_calls"
	openAIFinishReasonContentFilter = "content_filter"
	// openAIFinishReasonFunctionCall is the pre-tool_calls spelling. It still
	// arrives from older upstreams and means the same thing.
	openAIFinishReasonFunctionCall = "function_call"

	anthropicStopReasonEndTurn         = "end_turn"
	anthropicStopReasonStopSequence    = "stop_sequence"
	anthropicStopReasonMaxTokens       = "max_tokens"
	anthropicStopReasonContextExceeded = "model_context_window_exceeded"
	anthropicStopReasonToolUse         = "tool_use"
	anthropicStopReasonRefusal         = "refusal"
	anthropicStopReasonPauseTurn       = "pause_turn"
)

// anthropicFinishReason maps a stop reason. Unknown values are surfaced as an
// upstream error rather than being flattened to "stop", which would report a
// truncated or refused generation as a normal completion.
func anthropicFinishReason(stopReason string, hasToolCalls bool) (string, error) {
	switch stopReason {
	case anthropicStopReasonToolUse:
		return openAIFinishReasonToolCalls, nil
	case anthropicStopReasonPauseTurn:
		// A paused server-tool loop expects the assistant content to be replayed
		// verbatim so the provider can resume it. That is not something an
		// OpenAI-shaped client can do, and reporting tool_calls would tell the
		// client to run a tool that was never requested.
		return "", unsupportedCapabilityError("provider paused a server-side tool loop, which this route cannot resume")
	case anthropicStopReasonEndTurn, anthropicStopReasonStopSequence:
		if hasToolCalls {
			return openAIFinishReasonToolCalls, nil
		}
		return openAIFinishReasonStop, nil
	case anthropicStopReasonMaxTokens, anthropicStopReasonContextExceeded:
		return openAIFinishReasonLength, nil
	case anthropicStopReasonRefusal:
		return openAIFinishReasonContentFilter, nil
	case "":
		// Streaming responses report the stop reason in message_delta; a
		// non-streaming body without one is still in flight or malformed.
		if hasToolCalls {
			return openAIFinishReasonToolCalls, nil
		}
		return openAIFinishReasonStop, nil
	default:
		return "", invalidProviderResponseError(fmt.Sprintf("provider returned an unrecognized stop_reason %q", stopReason))
	}
}

// openAIFinishReasonToAnthropic is deliberately lenient where
// anthropicFinishReason is strict. It serves a downstream Anthropic client that
// has already been handed content, so there is no point failing the response
// over a reason string: an unknown value degrades to end_turn. Tool calls
// present in the response outrank every reason, because the client still has to
// be told to run them.
func openAIFinishReasonToAnthropic(reason string, hasTools bool) string {
	if hasTools || reason == openAIFinishReasonToolCalls || reason == openAIFinishReasonFunctionCall {
		return anthropicStopReasonToolUse
	}
	switch reason {
	case openAIFinishReasonLength:
		return anthropicStopReasonMaxTokens
	case openAIFinishReasonStop, "":
		return anthropicStopReasonEndTurn
	case openAIFinishReasonContentFilter:
		return anthropicStopReasonRefusal
	default:
		return anthropicStopReasonEndTurn
	}
}
