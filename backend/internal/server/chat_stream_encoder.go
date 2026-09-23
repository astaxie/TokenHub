package server

import (
	"encoding/json"
	"io"
	"time"
)

// openAIChatStreamEncoder renders provider-agnostic streaming events as OpenAI
// chat.completion.chunk frames. Provider decoders own their wire-format state
// machines and call into this encoder, so the OpenAI-facing output shape is
// defined in exactly one place.
//
// The encoder flushes after every frame. Without an explicit flush net/http
// buffers the response and a genuinely incremental upstream would still reach
// the client as one late burst.
type openAIChatStreamEncoder struct {
	writer       io.Writer
	id           string
	model        string
	created      int64
	includeUsage bool

	roleSent  bool
	finalized bool
	toolSlots int
}

type streamFlusher interface{ Flush() }

// streamUsageRequested reports whether the client asked for the trailing
// usage-only chunk via stream_options.include_usage.
func streamUsageRequested(req ChatCompletionRequest) bool {
	if req.StreamOptions == nil {
		return false
	}
	value, ok := req.StreamOptions["include_usage"].(bool)
	return ok && value
}

func newOpenAIChatStreamEncoder(w io.Writer, model string, includeUsage bool) *openAIChatStreamEncoder {
	return &openAIChatStreamEncoder{
		writer:       w,
		id:           NewID("chatcmpl"),
		model:        model,
		created:      time.Now().Unix(),
		includeUsage: includeUsage,
	}
}

// NextToolSlot allocates a dense OpenAI tool_calls index. Provider block
// indexes are not usable directly: Anthropic interleaves thinking and text
// blocks with tool_use blocks, so its content index is sparse from OpenAI's
// perspective.
func (e *openAIChatStreamEncoder) NextToolSlot() int {
	slot := e.toolSlots
	e.toolSlots++
	return slot
}

func (e *openAIChatStreamEncoder) EmitRole() error {
	if e.roleSent {
		return nil
	}
	e.roleSent = true
	return e.emitDelta(map[string]any{"role": "assistant"})
}

func (e *openAIChatStreamEncoder) EmitText(delta string) error {
	if delta == "" {
		return nil
	}
	if err := e.EmitRole(); err != nil {
		return err
	}
	return e.emitDelta(map[string]any{"content": delta})
}

func (e *openAIChatStreamEncoder) EmitReasoning(delta string) error {
	if delta == "" {
		return nil
	}
	if err := e.EmitRole(); err != nil {
		return err
	}
	return e.emitDelta(map[string]any{"reasoning_content": delta})
}

// EmitReasoningSignature forwards the provider's opaque continuation blob. It is
// emitted as a standalone delta so clients can associate it with the reasoning
// content that preceded it.
func (e *openAIChatStreamEncoder) EmitReasoningSignature(provider string, signature string) error {
	if signature == "" {
		return nil
	}
	if err := e.EmitRole(); err != nil {
		return err
	}
	return e.emitDelta(map[string]any{"reasoning_signature": encodeProviderSignature(provider, signature)})
}

// EmitToolReasoningDetail carries an opaque reasoning continuation beside the
// tool call it precedes. pi-ai persists this OpenRouter-compatible extension
// on the tool call, whereas it treats a standalone reasoning_signature as a
// display-only delta.
func (e *openAIChatStreamEncoder) EmitToolReasoningDetail(toolCallID string, provider string, signature string) error {
	if toolCallID == "" || signature == "" {
		return nil
	}
	if err := e.EmitRole(); err != nil {
		return err
	}
	return e.emitDelta(map[string]any{
		"reasoning_details": []any{map[string]any{
			"type": "reasoning.encrypted",
			"id":   toolCallID,
			"data": encodeProviderSignature(provider, signature),
		}},
	})
}

func (e *openAIChatStreamEncoder) EmitRedactedReasoning(data string) error {
	if data == "" {
		return nil
	}
	if err := e.EmitRole(); err != nil {
		return err
	}
	return e.emitDelta(map[string]any{"redacted_reasoning_content": data})
}

func (e *openAIChatStreamEncoder) EmitToolCallStart(slot int, id string, name string) error {
	if err := e.EmitRole(); err != nil {
		return err
	}
	call := map[string]any{
		"index":    slot,
		"id":       id,
		"type":     "function",
		"function": map[string]any{"name": name, "arguments": ""},
	}
	return e.emitDelta(map[string]any{"tool_calls": []any{call}})
}

func (e *openAIChatStreamEncoder) EmitToolCallArguments(slot int, delta string) error {
	if delta == "" {
		return nil
	}
	call := map[string]any{
		"index":    slot,
		"function": map[string]any{"arguments": delta},
	}
	return e.emitDelta(map[string]any{"tool_calls": []any{call}})
}

// EmitToolCallSignature attaches provider continuation data to a tool call.
// Gemini binds its thought signature to the function call rather than to the
// message, so it travels alongside the tool call instead of the reasoning text.
func (e *openAIChatStreamEncoder) EmitToolCallSignature(slot int, provider string, signature string) error {
	if signature == "" {
		return nil
	}
	call := map[string]any{
		"index":             slot,
		"thought_signature": encodeProviderSignature(provider, signature),
	}
	return e.emitDelta(map[string]any{"tool_calls": []any{call}})
}

// Finalize writes the terminal frames exactly once, in the order OpenAI
// clients expect: the finish_reason chunk, an optional usage-only chunk, then
// the [DONE] sentinel.
func (e *openAIChatStreamEncoder) Finalize(finishReason string, usage Usage) error {
	if e.finalized {
		return nil
	}
	e.finalized = true
	if err := e.EmitRole(); err != nil {
		return err
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	frame := e.newFrame()
	frame["choices"] = []any{map[string]any{
		"index":         0,
		"delta":         map[string]any{},
		"finish_reason": finishReason,
	}}
	if err := e.writeFrame(frame); err != nil {
		return err
	}
	if e.includeUsage {
		usageFrame := e.newFrame()
		usageFrame["choices"] = []any{}
		usageFrame["usage"] = openAIChatUsageObject(usage)
		if err := e.writeFrame(usageFrame); err != nil {
			return err
		}
	}
	if err := e.write("data: [DONE]\n\n"); err != nil {
		return err
	}
	return nil
}

// Abort marks the stream as terminated without emitting a completion sentinel.
// A client must not see [DONE] for a stream that failed midway.
func (e *openAIChatStreamEncoder) Abort() {
	e.finalized = true
}

func (e *openAIChatStreamEncoder) emitDelta(delta map[string]any) error {
	frame := e.newFrame()
	frame["choices"] = []any{map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": nil,
	}}
	return e.writeFrame(frame)
}

func (e *openAIChatStreamEncoder) newFrame() map[string]any {
	frame := map[string]any{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
	}
	if e.includeUsage {
		// OpenAI sets usage to null on every chunk but the final usage-only one.
		frame["usage"] = nil
	}
	return frame
}

func (e *openAIChatStreamEncoder) writeFrame(frame map[string]any) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return e.write("data: " + string(payload) + "\n\n")
}

func (e *openAIChatStreamEncoder) write(payload string) error {
	if _, err := io.WriteString(e.writer, payload); err != nil {
		return err
	}
	if flusher, ok := e.writer.(streamFlusher); ok {
		flusher.Flush()
	}
	return nil
}
