package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The probe replaced two full map[string]any decodes per frame with one decode
// of four raw fields. These tests pin the new parsing to the old one it stands
// in for: legacyProviderStreamEventIsError and legacyUsageFromServerSentEvent
// below are verbatim copies of the code the probe replaced, and every payload
// class the streaming path can meet is run through both.

func legacyProviderStreamEventIsError(event serverSentEvent) bool {
	eventName := strings.ToLower(strings.TrimSpace(event.Event))
	if eventName == "error" || strings.HasSuffix(eventName, ".failed") {
		return true
	}
	payload := strings.TrimSpace(event.Data)
	if payload == "" || payload == "[DONE]" {
		return false
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(payload), &decoded) != nil {
		return false
	}
	if eventType, _ := decoded["type"].(string); strings.EqualFold(strings.TrimSpace(eventType), "error") || strings.HasSuffix(strings.ToLower(strings.TrimSpace(eventType)), ".failed") {
		return true
	}
	if errorValue, hasError := decoded["error"]; hasError && errorValue != nil {
		return true
	}
	response, _ := decoded["response"].(map[string]any)
	errorValue, hasResponseError := response["error"]
	return hasResponseError && errorValue != nil
}

func legacyUsageFromServerSentEvent(frame serverSentEvent) (Usage, bool) {
	if usage, ok := legacyUsageFromSSEPayload(frame.Data); ok {
		return usage, true
	}
	if !strings.Contains(frame.Data, "\n") {
		return Usage{}, false
	}
	var (
		usage Usage
		found bool
	)
	for _, segment := range strings.Split(frame.Data, "\n") {
		if parsed, ok := legacyUsageFromSSEPayload(segment); ok {
			usage, found = parsed, true
		}
	}
	return usage, found
}

func legacyUsageFromSSEPayload(data string) (Usage, bool) {
	payload := strings.TrimSpace(data)
	if payload == "" || payload == "[DONE]" {
		return Usage{}, false
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Usage{}, false
	}
	usageMap, ok := event["usage"].(map[string]any)
	if !ok || len(usageMap) == 0 {
		return Usage{}, false
	}
	return usageFromMap(event), true
}

type providerStreamProbeCase struct {
	name  string
	frame serverSentEvent
}

func providerStreamProbeCases() []providerStreamProbeCase {
	return []providerStreamProbeCase{
		{"content chunk", serverSentEvent{Data: chatCompletionChunkPayload}},
		{"empty payload", serverSentEvent{Data: ""}},
		{"blank payload", serverSentEvent{Data: "   "}},
		{"done sentinel", serverSentEvent{Data: "[DONE]"}},
		{"done sentinel padded", serverSentEvent{Data: "  [DONE]  "}},
		{"error event name", serverSentEvent{Event: "error", Data: chatCompletionChunkPayload}},
		{"failed event name", serverSentEvent{Event: "Response.Failed", Data: chatCompletionChunkPayload}},
		{"error object", serverSentEvent{Data: `{"error":{"message":"upstream refused","code":429}}`}},
		{"error key escaped", serverSentEvent{Data: `{"\u0065rror":{"message":"upstream refused"}}`}},
		{"error null", serverSentEvent{Data: `{"error":null}`}},
		{"error key escaped null", serverSentEvent{Data: `{"\u0065rror":null}`}},
		{"error false", serverSentEvent{Data: `{"error":false}`}},
		{"error empty string", serverSentEvent{Data: `{"error":""}`}},
		{"type error", serverSentEvent{Data: `{"type":"error","message":"boom"}`}},
		{"type error escaped value", serverSentEvent{Data: `{"type":"err\u006fr"}`}},
		{"type error key escaped", serverSentEvent{Data: `{"\u0074ype":"error"}`}},
		{"type error padded", serverSentEvent{Data: `{"type":"  ERROR  "}`}},
		{"type empty string", serverSentEvent{Data: `{"type":""}`}},
		{"type escaped quote", serverSentEvent{Data: `{"type":"resp\"onse.FAILED"}`}},
		{"type escaped only", serverSentEvent{Data: `{"type":"\u0065rror"}`}},
		{"type response failed", serverSentEvent{Data: `{"type":"response.FAILED"}`}},
		{"type delta", serverSentEvent{Data: responsesDeltaChunkPayload}},
		{"type number", serverSentEvent{Data: `{"type":7}`}},
		{"type null", serverSentEvent{Data: `{"type":null}`}},
		{"type object", serverSentEvent{Data: `{"type":{"name":"error"}}`}},
		{"type array", serverSentEvent{Data: `{"type":["error"]}`}},
		{"nested response error", serverSentEvent{Data: `{"type":"response.completed","response":{"error":{"message":"boom"}}}`}},
		{"nested response error null", serverSentEvent{Data: `{"response":{"error":null}}`}},
		{"nested response no error", serverSentEvent{Data: `{"response":{"id":"resp_1"}}`}},
		{"response string", serverSentEvent{Data: `{"response":"oops"}`}},
		{"response array", serverSentEvent{Data: `{"response":[{"error":{"message":"boom"}}]}`}},
		{"response null", serverSentEvent{Data: `{"response":null}`}},
		{"usage object", serverSentEvent{Data: usageFramePayload}},
		{"usage key escaped", serverSentEvent{Data: `{"\u0075sage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`}},
		{"usage null", serverSentEvent{Data: `{"usage":null}`}},
		{"usage array", serverSentEvent{Data: `{"usage":[1]}`}},
		{"usage number", serverSentEvent{Data: `{"usage":12}`}},
		{"usage string", serverSentEvent{Data: `{"usage":"none"}`}},
		{"usage empty object", serverSentEvent{Data: `{"usage":{}}`}},
		{"usage with token details", serverSentEvent{Data: `{"usage":{"prompt_tokens":31,"completion_tokens":9,"total_tokens":40,"prompt_tokens_details":{"cached_tokens":16,"audio_tokens":2},"completion_tokens_details":{"reasoning_tokens":5,"accepted_prediction_tokens":1,"rejected_prediction_tokens":2}}}`}},
		{"usage with error", serverSentEvent{Data: `{"error":{"message":"boom"},"usage":{"total_tokens":5}}`}},
		{"usage inner whitespace", serverSentEvent{Data: `{ "usage" :  { "total_tokens" : 5 } }`}},
		{"leading whitespace", serverSentEvent{Data: "  \t\r\n" + usageFramePayload}},
		{"trailing whitespace", serverSentEvent{Data: usageFramePayload + " \n"}},
		{"array payload", serverSentEvent{Data: `[{"usage":{"total_tokens":5}}]`}},
		{"string payload", serverSentEvent{Data: `"error"`}},
		{"number payload", serverSentEvent{Data: `42`}},
		{"null payload", serverSentEvent{Data: `null`}},
		{"true payload", serverSentEvent{Data: `true`}},
		{"malformed payload", serverSentEvent{Data: `{"usage":{"total_tokens":5}`}},
		{"trailing garbage", serverSentEvent{Data: `{"usage":{"total_tokens":5}} oops`}},
		{"joined segments with usage", serverSentEvent{Data: chatCompletionChunkPayload + "\n" + usageFramePayload}},
		{"joined segments escaped usage", serverSentEvent{Data: chatCompletionChunkPayload + "\n" + `{"\u0075sage":{"prompt_tokens":11,"completion_tokens":13,"total_tokens":24}}`}},
		{"joined segments last usage wins", serverSentEvent{Data: usageFramePayload + "\n" + `{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`}},
		{"joined segments with done", serverSentEvent{Data: usageFramePayload + "\n[DONE]"}},
		{"joined segments without usage", serverSentEvent{Data: chatCompletionChunkPayload + "\n" + responsesDeltaChunkPayload}},
		{"joined segments with error", serverSentEvent{Data: `{"error":{"message":"boom"}}` + "\n" + usageFramePayload}},
		{"multiline object split at a value", serverSentEvent{Data: `{"usage":` + "\n" + `{"total_tokens":5}}`}},
		// A JSON key is case-sensitive and the old lookups were exact, so a frame
		// must not be able to hide a field behind a case variant of its name. The
		// overwriting pairs are the dangerous ones: reading the second key as the
		// first would forward an error frame without redacting its secrets.
		{"type shadowed by case variant", serverSentEvent{Data: `{"type":"error","TYPE":null}`}},
		{"error shadowed by case variant", serverSentEvent{Data: `{"error":{"message":"boom"},"ERROR":null}`}},
		{"usage shadowed by case variant", serverSentEvent{Data: `{"usage":{"total_tokens":5},"USAGE":null}`}},
		{"nested error shadowed by case variant", serverSentEvent{Data: `{"response":{"error":{"message":"boom"},"ERROR":null}}`}},
		{"uppercase type only", serverSentEvent{Data: `{"TYPE":"error"}`}},
		{"uppercase error only", serverSentEvent{Data: `{"Error":{"message":"boom"}}`}},
		{"uppercase usage only", serverSentEvent{Data: `{"Usage":{"total_tokens":5}}`}},
		{"duplicate type keys", serverSentEvent{Data: `{"type":"response.output_text.delta","type":"error"}`}},
		{"duplicate error keys", serverSentEvent{Data: `{"error":null,"error":{"message":"boom"}}`}},
		{"duplicate error keys nulled last", serverSentEvent{Data: `{"error":{"message":"boom"},"error":null}`}},
		{"duplicate usage keys", serverSentEvent{Data: `{"usage":{"total_tokens":9},"usage":{"total_tokens":4}}`}},
		{"duplicate usage keys nulled last", serverSentEvent{Data: `{"usage":{"total_tokens":9},"usage":null}`}},
		{"response shadowed by case variant", serverSentEvent{Data: `{"response":{"error":{"message":"boom"}},"RESPONSE":null}`}},
		{"uppercase response only", serverSentEvent{Data: `{"Response":{"error":{"message":"boom"}}}`}},
		{"response key escaped", serverSentEvent{Data: `{"\u0072esponse":{"error":{"message":"boom"}}}`}},
		{"duplicate response keys", serverSentEvent{Data: `{"response":{"id":"resp_1"},"response":{"error":{"message":"boom"}}}`}},
		{"duplicate response keys nulled last", serverSentEvent{Data: `{"response":{"error":{"message":"boom"}},"response":null}`}},
		{"duplicate nested error keys", serverSentEvent{Data: `{"response":{"error":null,"error":{"message":"boom"}}}`}},
		{"duplicate nested error keys nulled last", serverSentEvent{Data: `{"response":{"error":{"message":"boom"},"error":null}}`}},
		{"nested error key escaped", serverSentEvent{Data: `{"response":{"\u0065rror":{"message":"boom"}}}`}},
		{"nested error case variant only", serverSentEvent{Data: `{"response":{"ERROR":{"message":"boom"}}}`}},
		{"type invalid utf8 suffix", serverSentEvent{Data: `{"type":"` + "\xff" + `.FAILED"}`}},
		{"type invalid utf8 word", serverSentEvent{Data: `{"type":"erro` + "\xff" + `"}`}},
		{"joined segments unreadable number last", serverSentEvent{Data: `{"usage":{"total_tokens":9}}` + "\n" + `{"usage":{"total_tokens":4},"unread":1e1000}`}},
		{"joined segments unreadable number first", serverSentEvent{Data: `{"usage":{"total_tokens":4},"unread":1e1000}` + "\n" + `{"usage":{"total_tokens":9}}`}},
		{"joined segments unreadable number only", serverSentEvent{Data: `{"choices":[]}` + "\n" + `{"usage":{"total_tokens":4},"unread":1e1000}`}},
		{"multiline object", serverSentEvent{Data: `{"usage":` + "\n" + `{"total_tokens":5}}`}},
		{"multiline object with unreadable number", serverSentEvent{Data: `{` + "\n" + `"usage":{"total_tokens":4},` + "\n" + `"unread":1e1000,` + "\n" + `"wrapper":` + "\n" + `{"usage":{"total_tokens":9}}` + "\n" + `}`}},
		{"multiline object with unreadable number and no inner usage", serverSentEvent{Data: `{` + "\n" + `"usage":{"total_tokens":4},` + "\n" + `"unread":1e1000` + "\n" + `}`}},
	}
}

func TestProviderStreamEventProbeMatchesTheParsingItReplaced(t *testing.T) {
	for _, testCase := range providerStreamProbeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			if got, want := providerStreamEventIsError(testCase.frame), legacyProviderStreamEventIsError(testCase.frame); got != want {
				t.Errorf("providerStreamEventIsError(%q) = %v, the parsing it replaced said %v", testCase.frame.Data, got, want)
			}
			gotUsage, gotOK := usageFromServerSentEvent(testCase.frame)
			wantUsage, wantOK := legacyUsageFromServerSentEvent(testCase.frame)
			if gotOK != wantOK {
				t.Fatalf("usageFromServerSentEvent(%q) reported ok=%v, the parsing it replaced said %v", testCase.frame.Data, gotOK, wantOK)
			}
			if !reflect.DeepEqual(gotUsage, wantUsage) {
				t.Errorf("usageFromServerSentEvent(%q) = %+v, the parsing it replaced said %+v", testCase.frame.Data, gotUsage, wantUsage)
			}
		})
	}
}

// TestProviderStreamEventProbeReadsFramesTheOldParsingGaveUpOn pins the one
// place the probe knowingly parts company with the parsing it replaced.
//
// Decoding a payload into map[string]any converts every number in it to a
// float64, so one unrelated field holding a number too large for a float64
// failed the whole decode: the frame was billed as carrying no usage, and — the
// part that matters — an error frame was forwarded to the client with its
// secrets unredacted. The probe leaves numbers it does not read alone, so those
// frames now parse. Both directions of the difference are safe ones: usage that
// was silently dropped is now billed, and an error frame that slipped past
// redaction is now redacted.
//
// The usage half only reaches single-line payloads. A payload spanning several
// lines is read by the old parsing throughout, so no frame's billed usage is
// ever replaced by a different one; the multiline cases in the table above hold
// that line.
func TestProviderStreamEventProbeReadsFramesTheOldParsingGaveUpOn(t *testing.T) {
	usageFrame := serverSentEvent{Data: `{"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},"unread":1e1000}`}
	if _, ok := legacyUsageFromServerSentEvent(usageFrame); ok {
		t.Fatal("the old parsing is expected to give up on this frame; the divergence this test documents is gone")
	}
	usage, ok := usageFromServerSentEvent(usageFrame)
	if !ok || usage.TotalTokens != 5 {
		t.Errorf("usageFromServerSentEvent() = %+v, ok=%v, want 5 total tokens", usage, ok)
	}

	errorFrame := serverSentEvent{Data: `{"error":{"message":"boom"},"unread":1e1000}`}
	if legacyProviderStreamEventIsError(errorFrame) {
		t.Fatal("the old parsing is expected to miss this error frame; the divergence this test documents is gone")
	}
	if !providerStreamEventIsError(errorFrame) {
		t.Error("providerStreamEventIsError() = false, an error frame must be redacted before it is forwarded")
	}
}

// TestProviderStreamEventProbeSharesOneDecode guards the point of the change:
// the error check and the usage read must come out of the same probe, so a
// frame that answers both still decodes once.
func TestProviderStreamEventProbeSharesOneDecode(t *testing.T) {
	frame := serverSentEvent{Data: `{"error":{"message":"boom"},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`}
	probe := probeProviderStreamEvent(frame.Data)

	if !probe.isError() {
		t.Error("the shared probe missed the error field")
	}
	usage, ok := usageFromProbedFrame(frame, probe)
	if !ok {
		t.Fatal("the shared probe missed the usage field")
	}
	if usage.TotalTokens != 5 {
		t.Errorf("usage from the shared probe = %+v, want 5 total tokens", usage)
	}
}

const (
	chatCompletionChunkPayload = `{"id":"chatcmpl-BvQ2n4Xk9pLmT7","object":"chat.completion.chunk","created":1712345678,"model":"gpt-4o-mini","system_fingerprint":"fp_44709d6fcb","choices":[{"index":0,"delta":{"content":" streaming tokens arrive one small delta at a time"},"logprobs":null,"finish_reason":null}]}`
	responsesDeltaChunkPayload = `{"type":"response.output_text.delta","sequence_number":37,"item_id":"msg_68a1c4d2","output_index":0,"content_index":0,"delta":" streaming tokens arrive one small delta at a time","logprobs":[]}`
	usageFramePayload          = `{"id":"chatcmpl-BvQ2n4Xk9pLmT7","object":"chat.completion.chunk","created":1712345678,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":1287,"completion_tokens":413,"total_tokens":1700,"prompt_tokens_details":{"cached_tokens":1024,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":128,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}`
)

// BenchmarkProviderStreamEventParsing measures what the streaming path does per
// frame — decide whether the frame is an error, then read any usage off it —
// against the two-decode parsing it replaced.
func BenchmarkProviderStreamEventParsing(b *testing.B) {
	frames := []providerStreamProbeCase{
		{"chat chunk", serverSentEvent{Data: chatCompletionChunkPayload}},
		{"responses delta", serverSentEvent{Event: "response.output_text.delta", Data: responsesDeltaChunkPayload}},
		{"usage frame", serverSentEvent{Data: usageFramePayload}},
	}
	for _, frame := range frames {
		b.Run(fmt.Sprintf("%s/probe", frame.name), func(b *testing.B) {
			b.SetBytes(int64(len(frame.frame.Data)))
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				probe := probeProviderStreamEvent(frame.frame.Data)
				isError := sseEventNameIsError(frame.frame.Event) || probe.isError()
				usage, ok := usageFromProbedFrame(frame.frame, probe)
				sinkStreamFrameParse(b, isError, usage, ok)
			}
		})
		b.Run(fmt.Sprintf("%s/legacy", frame.name), func(b *testing.B) {
			b.SetBytes(int64(len(frame.frame.Data)))
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				isError := legacyProviderStreamEventIsError(frame.frame)
				usage, ok := legacyUsageFromServerSentEvent(frame.frame)
				sinkStreamFrameParse(b, isError, usage, ok)
			}
		})
	}
}

// sinkStreamFrameParse keeps every parsed value live so the compiler cannot
// drop the work being measured. It asserts nothing; the parsing is pinned by
// TestProviderStreamEventProbeMatchesTheParsingItReplaced.
func sinkStreamFrameParse(b *testing.B, isError bool, usage Usage, ok bool) {
	b.Helper()
	if isError && ok && usage.TotalTokens < 0 {
		b.Fatal("unreachable: a token count cannot be negative")
	}
}
