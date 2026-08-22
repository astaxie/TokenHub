package server

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// recordingWriter captures frames and counts flushes so tests can assert that
// output actually reaches the client incrementally.
type recordingWriter struct {
	builder strings.Builder
	flushes int
	failAt  int
	writes  int
}

func (w *recordingWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.failAt > 0 && w.writes >= w.failAt {
		return 0, errors.New("downstream write failed")
	}
	return w.builder.Write(data)
}

func (w *recordingWriter) Flush() { w.flushes++ }

func (w *recordingWriter) frames(t *testing.T) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, block := range strings.Split(w.builder.String(), "\n\n") {
		payload := strings.TrimPrefix(strings.TrimSpace(block), "data: ")
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			t.Fatalf("frame is not valid JSON: %v (%q)", err, payload)
		}
		frames = append(frames, frame)
	}
	return frames
}

func frameDelta(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	choices, _ := frame["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	return delta
}

func TestStreamEncoderEmitsRoleThenTextAndFlushesEachFrame(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)

	if err := encoder.EmitText("Hel"); err != nil {
		t.Fatalf("EmitText: %v", err)
	}
	if err := encoder.EmitText("lo"); err != nil {
		t.Fatalf("EmitText: %v", err)
	}
	if err := encoder.Finalize("stop", Usage{}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	frames := writer.frames(t)
	if len(frames) != 4 {
		t.Fatalf("expected role + 2 text + finish frames, got %d", len(frames))
	}
	if role, _ := frameDelta(t, frames[0])["role"].(string); role != "assistant" {
		t.Fatalf("first frame must announce the assistant role, got %v", frames[0])
	}
	if content, _ := frameDelta(t, frames[1])["content"].(string); content != "Hel" {
		t.Fatalf("unexpected first text delta: %v", frames[1])
	}
	// Without a flush per frame net/http buffers the response and the stream
	// only reaches the client at the end.
	if writer.flushes != writer.writes {
		t.Fatalf("expected one flush per write, got %d flushes for %d writes", writer.flushes, writer.writes)
	}
	if !strings.HasSuffix(writer.builder.String(), "data: [DONE]\n\n") {
		t.Fatalf("stream must end with the [DONE] sentinel")
	}
}

func TestStreamEncoderOmitsUsageChunkUnlessRequested(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if err := encoder.Finalize("stop", Usage{PromptTokens: 3, CompletionTokens: 4}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	for _, frame := range writer.frames(t) {
		if _, present := frame["usage"]; present {
			t.Fatalf("usage must be absent when include_usage was not requested: %v", frame)
		}
	}
}

func TestStreamEncoderEmitsTrailingUsageChunkWhenRequested(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", true)
	if err := encoder.EmitText("hi"); err != nil {
		t.Fatalf("EmitText: %v", err)
	}
	if err := encoder.Finalize("stop", Usage{
		PromptTokens:          10,
		CachedInputTokens:     6,
		CacheWriteInputTokens: 2,
		CompletionTokens:      4,
		ReasoningOutputTokens: 3,
		TotalTokens:           14,
	}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	frames := writer.frames(t)
	final := frames[len(frames)-1]
	choices, _ := final["choices"].([]any)
	if len(choices) != 0 {
		t.Fatalf("the usage chunk must carry an empty choices array, got %v", final)
	}
	usage, _ := final["usage"].(map[string]any)
	if usage == nil {
		t.Fatalf("expected a usage payload on the final chunk, got %v", final)
	}
	inputDetails, _ := usage["prompt_tokens_details"].(map[string]any)
	if inputDetails["cached_tokens"] != float64(6) || inputDetails["cache_write_tokens"] != float64(2) {
		t.Fatalf("standard input details = %#v", inputDetails)
	}
	outputDetails, _ := usage["completion_tokens_details"].(map[string]any)
	if outputDetails["reasoning_tokens"] != float64(3) {
		t.Fatalf("standard output details = %#v", outputDetails)
	}
	if usage["cached_input_tokens"] != float64(6) || usage["cache_write_input_tokens"] != float64(2) {
		t.Fatalf("legacy usage aliases = %#v", usage)
	}
	// Every preceding chunk carries an explicit null usage.
	for _, frame := range frames[:len(frames)-1] {
		value, present := frame["usage"]
		if !present || value != nil {
			t.Fatalf("non-final chunks must carry usage:null, got %v", frame)
		}
	}
}

func TestStreamEncoderAbortSuppressesDoneSentinel(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	if err := encoder.EmitText("partial"); err != nil {
		t.Fatalf("EmitText: %v", err)
	}
	encoder.Abort()
	if err := encoder.Finalize("stop", Usage{}); err != nil {
		t.Fatalf("Finalize after Abort must be a no-op: %v", err)
	}
	if strings.Contains(writer.builder.String(), "[DONE]") {
		t.Fatalf("an aborted stream must not tell the client it completed")
	}
}

func TestStreamEncoderAssignsDenseToolSlots(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)

	first := encoder.NextToolSlot()
	second := encoder.NextToolSlot()
	if first != 0 || second != 1 {
		t.Fatalf("tool slots must be dense and zero-based, got %d and %d", first, second)
	}
	if err := encoder.EmitToolCallStart(second, "call_2", "beta"); err != nil {
		t.Fatalf("EmitToolCallStart: %v", err)
	}
	if err := encoder.EmitToolCallArguments(second, `{"a":1}`); err != nil {
		t.Fatalf("EmitToolCallArguments: %v", err)
	}

	frames := writer.frames(t)
	last := frameDelta(t, frames[len(frames)-1])
	calls, _ := last["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	if index, _ := call["index"].(float64); int(index) != 1 {
		t.Fatalf("tool call must report its dense index, got %v", call)
	}
}

func TestStreamEncoderEmitsToolBoundReasoningDetail(t *testing.T) {
	writer := &recordingWriter{}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)

	if err := encoder.EmitToolReasoningDetail("call_1", codexSignatureProvider, "opaque-state"); err != nil {
		t.Fatalf("EmitToolReasoningDetail: %v", err)
	}

	frames := writer.frames(t)
	delta := frameDelta(t, frames[len(frames)-1])
	details, _ := delta["reasoning_details"].([]any)
	if len(details) != 1 {
		t.Fatalf("reasoning_details = %#v, want one item", delta["reasoning_details"])
	}
	detail, _ := details[0].(map[string]any)
	if detail["type"] != "reasoning.encrypted" || detail["id"] != "call_1" || detail["data"] != encodeProviderSignature(codexSignatureProvider, "opaque-state") {
		t.Fatalf("reasoning detail = %#v", detail)
	}
}

func TestStreamEncoderPropagatesWriteErrors(t *testing.T) {
	writer := &recordingWriter{failAt: 2}
	encoder := newOpenAIChatStreamEncoder(writer, "model-x", false)
	// The role frame succeeds, the text frame fails.
	if err := encoder.EmitText("boom"); err == nil {
		t.Fatalf("expected the downstream write error to propagate")
	}
}

func TestSSEDecoderHandlesFramingVariants(t *testing.T) {
	// CRLF line endings, a comment heartbeat, a multi-line data payload and a
	// named event all appear in real provider streams.
	raw := ": heartbeat\r\n" +
		"event: message_start\r\n" +
		"data: {\"type\":\r\n" +
		"data: \"message_start\"}\r\n" +
		"\r\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	decoder := newSSEDecoder(strings.NewReader(raw))

	// The leading comment opens no frame, so it is delivered on its own rather
	// than waiting for one: a keepalive needs no blank line to be complete.
	heartbeat, err := decoder.Next()
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if heartbeat.Event != "" || heartbeat.Data != "" {
		t.Fatalf("a comment carries no fields, got %+v", heartbeat)
	}
	if string(heartbeat.Raw) != ": heartbeat\r\n" {
		t.Fatalf("heartbeat raw = %q", heartbeat.Raw)
	}

	first, err := decoder.Next()
	if err != nil {
		t.Fatalf("first event: %v", err)
	}
	if first.Event != "message_start" {
		t.Fatalf("expected the named event, got %q", first.Event)
	}
	payload, err := decodeSSEData(first)
	if err != nil {
		t.Fatalf("multi-line data must reassemble into valid JSON: %v", err)
	}
	if payload["type"] != "message_start" {
		t.Fatalf("unexpected payload: %v", payload)
	}

	second, err := decoder.Next()
	if err != nil {
		t.Fatalf("second event: %v", err)
	}
	if second.Event != "message_stop" {
		t.Fatalf("expected message_stop, got %q", second.Event)
	}

	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("expected EOF after the last frame, got %v", err)
	}
}

func TestSSEDecoderHandlesEventsLargerThanScannerLimit(t *testing.T) {
	// bufio.Scanner caps tokens at 64 KiB by default; reasoning and tool
	// argument frames routinely exceed that.
	large := strings.Repeat("x", 128*1024)
	raw := "data: {\"text\":\"" + large + "\"}\n\n"

	event, err := newSSEDecoder(strings.NewReader(raw)).Next()
	if err != nil {
		t.Fatalf("large event: %v", err)
	}
	payload, err := decodeSSEData(event)
	if err != nil {
		t.Fatalf("decode large event: %v", err)
	}
	if text, _ := payload["text"].(string); len(text) != len(large) {
		t.Fatalf("large payload was truncated: got %d bytes", len(text))
	}
}

func TestSSEDecoderRejectsMalformedJSON(t *testing.T) {
	event, err := newSSEDecoder(strings.NewReader("data: {not json}\n\n")).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	// Skipping a malformed frame would silently truncate the response, so it
	// must surface as an upstream error instead.
	if _, err := decodeSSEData(event); err == nil {
		t.Fatalf("expected malformed event data to be rejected")
	}
}

func TestSSEDecoderReturnsFinalFrameWithoutTrailingBlankLine(t *testing.T) {
	event, err := newSSEDecoder(strings.NewReader("data: {\"a\":1}")).Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	payload, err := decodeSSEData(event)
	if err != nil {
		t.Fatalf("decodeSSEData: %v", err)
	}
	if payload["a"].(float64) != 1 {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestStreamUsageRequested(t *testing.T) {
	if streamUsageRequested(ChatCompletionRequest{}) {
		t.Fatalf("usage must not be reported without stream_options")
	}
	req := ChatCompletionRequest{StreamOptions: map[string]any{"include_usage": true}}
	if !streamUsageRequested(req) {
		t.Fatalf("include_usage=true must request the usage chunk")
	}
}
