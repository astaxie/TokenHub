package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
		usageFrame["usage"] = usage
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

// serverSentEvent is one decoded SSE frame.
type serverSentEvent struct {
	Event string
	Data  string
}

// maxSSEEventBytes bounds a single decoded event. bufio.Scanner's 64 KiB
// default is too small for reasoning and tool-argument frames, but an unbounded
// reader lets a faulty or hostile upstream grow one event until the process runs
// out of memory.
const maxSSEEventBytes = 8 << 20

// sseDecoder reads Server-Sent Events without the 64 KiB ceiling bufio.Scanner
// imposes by default.
type sseDecoder struct {
	reader *bufio.Reader
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	return &sseDecoder{reader: bufio.NewReader(r)}
}

// Next returns the next event, or io.EOF once the stream is exhausted.
func (d *sseDecoder) Next() (serverSentEvent, error) {
	var (
		event serverSentEvent
		data  []string
		seen  bool
		size  int
	)
	for {
		line, err := d.readLine(&size)
		if err != nil && err != io.EOF {
			return serverSentEvent{}, err
		}
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				// Blank line terminates the frame. Ignore stray separators
				// between frames.
				if seen {
					event.Data = strings.Join(data, "\n")
					return event, nil
				}
			case strings.HasPrefix(trimmed, ":"):
				// Comment/heartbeat line.
				seen = true
			default:
				name, value := splitSSEField(trimmed)
				seen = true
				switch name {
				case "event":
					event.Event = value
				case "data":
					data = append(data, value)
				default:
					// id/retry and unknown fields are not meaningful here.
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if seen {
					event.Data = strings.Join(data, "\n")
					return event, nil
				}
				return serverSentEvent{}, io.EOF
			}
			return serverSentEvent{}, err
		}
	}
}

// readLine reads one line while charging its bytes against the event budget.
// bufio.Reader.ReadString would allocate the whole line before returning, so a
// line that never terminates could exhaust memory before any limit was checked.
// ReadSlice hands back bounded chunks, letting the budget stop the read early.
func (d *sseDecoder) readLine(size *int) (string, error) {
	var builder strings.Builder
	for {
		chunk, err := d.reader.ReadSlice('\n')
		*size += len(chunk)
		if *size > maxSSEEventBytes {
			return "", invalidProviderResponseError("provider sent a stream event that exceeds the size limit")
		}
		builder.Write(chunk)
		if err == bufio.ErrBufferFull {
			continue
		}
		return builder.String(), err
	}
}

func splitSSEField(line string) (string, string) {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return line, ""
	}
	name := line[:colon]
	value := line[colon+1:]
	// A single leading space after the colon is part of the framing.
	value = strings.TrimPrefix(value, " ")
	return name, value
}

// decodeSSEData unmarshals an event payload, rejecting malformed frames rather
// than skipping them: a dropped frame silently truncates the response.
func decodeSSEData(event serverSentEvent) (map[string]any, error) {
	payload := strings.TrimSpace(event.Data)
	if payload == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, invalidProviderResponseError(fmt.Sprintf("provider sent a malformed stream event: %v", err))
	}
	return decoded, nil
}
