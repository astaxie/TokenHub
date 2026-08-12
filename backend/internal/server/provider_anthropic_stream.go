package server

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// anthropicStreamDecoder translates the Anthropic Messages SSE event stream into
// OpenAI chunk frames. It owns all Anthropic-specific state; the encoder only
// knows how to render OpenAI frames.
type anthropicStreamDecoder struct {
	encoder  *openAIChatStreamEncoder
	provider Provider

	// toolSlots maps an Anthropic content block index to a dense OpenAI
	// tool_calls index. The two are not interchangeable: thinking and text
	// blocks occupy content indexes but are not tool calls.
	toolSlots map[int]int
	// signatures accumulates signature_delta fragments per content block.
	signatures map[int]*strings.Builder

	// toolArguments accumulates the JSON fragments per block so the assembled
	// arguments can be validated once the block closes. Fragments are still
	// forwarded incrementally as they arrive.
	toolArguments map[int]*strings.Builder

	usage        map[string]any
	stopReason   string
	sawMessage   bool
	sawTerminal  bool
	hasToolCalls bool
}

func newAnthropicStreamDecoder(encoder *openAIChatStreamEncoder) *anthropicStreamDecoder {
	return &anthropicStreamDecoder{
		encoder:       encoder,
		toolSlots:     map[int]int{},
		signatures:    map[int]*strings.Builder{},
		toolArguments: map[int]*strings.Builder{},
		usage:         map[string]any{},
	}
}

// streamAnthropicChat consumes the upstream SSE body and writes OpenAI chunks.
// It returns the usage reported by the provider.
func streamAnthropicChat(body io.Reader, encoder *openAIChatStreamEncoder) (Usage, error) {
	return streamAnthropicChatForProvider(body, encoder, Provider{})
}

func streamAnthropicChatForProvider(body io.Reader, encoder *openAIChatStreamEncoder, provider Provider) (Usage, error) {
	decoder := newAnthropicStreamDecoder(encoder)
	decoder.provider = provider
	events := newSSEDecoder(body)
	for {
		event, err := events.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			encoder.Abort()
			return decoder.currentUsage(), err
		}
		done, err := decoder.consume(event)
		if err != nil {
			encoder.Abort()
			return decoder.currentUsage(), err
		}
		if done {
			break
		}
	}
	usage := decoder.currentUsage()
	if !decoder.sawMessage {
		encoder.Abort()
		return usage, invalidProviderResponseError("provider closed the stream before sending any content")
	}
	if !decoder.sawTerminal {
		// An Anthropic stream always ends with message_stop. Reaching EOF without
		// it means the connection dropped mid-generation; reporting a normal
		// completion would hand the client a silently truncated answer.
		encoder.Abort()
		return usage, invalidProviderResponseError("provider closed the stream before completing the message")
	}
	if len(decoder.toolArguments) > 0 {
		// A tool block that never closed leaves its arguments unvalidated, so the
		// client would receive a tool call it cannot execute.
		encoder.Abort()
		return usage, invalidProviderResponseError("provider ended the message with an unterminated tool call")
	}
	finishReason, err := anthropicFinishReason(decoder.stopReason, decoder.hasToolCalls)
	if err != nil {
		encoder.Abort()
		return usage, err
	}
	if err := encoder.Finalize(finishReason, usage); err != nil {
		return usage, err
	}
	return usage, nil
}

// consume handles one SSE event. The boolean result reports stream completion.
func (d *anthropicStreamDecoder) consume(event serverSentEvent) (bool, error) {
	payload, err := decodeSSEData(event)
	if err != nil {
		return false, err
	}
	if payload == nil {
		return false, nil
	}
	eventType := event.Event
	if eventType == "" {
		eventType, _ = payload["type"].(string)
	}

	switch eventType {
	case "message_start":
		d.sawMessage = true
		if message, ok := payload["message"].(map[string]any); ok {
			d.mergeUsage(message["usage"])
		}
		return false, d.encoder.EmitRole()
	case "content_block_start":
		return false, d.handleBlockStart(payload)
	case "content_block_delta":
		return false, d.handleBlockDelta(payload)
	case "content_block_stop":
		return false, d.handleBlockStop(payload)
	case "message_delta":
		if delta, ok := payload["delta"].(map[string]any); ok {
			if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
				d.stopReason = reason
			}
		}
		d.mergeUsage(payload["usage"])
		return false, nil
	case "message_stop":
		d.sawTerminal = true
		return true, nil
	case "error":
		return false, anthropicStreamError(payload, d.provider)
	case "ping":
		return false, nil
	default:
		// Anthropic adds event types over time; ignoring unknown ones keeps the
		// stream usable instead of failing on a forward-compatible addition.
		return false, nil
	}
}

func (d *anthropicStreamDecoder) handleBlockStart(payload map[string]any) error {
	index, err := blockIndex(payload)
	if err != nil {
		return err
	}
	block, _ := payload["content_block"].(map[string]any)
	if block == nil {
		return nil
	}
	switch blockType, _ := block["type"].(string); blockType {
	case "tool_use":
		if _, exists := d.toolSlots[index]; exists {
			return invalidProviderResponseError(fmt.Sprintf("provider reopened content block %d", index))
		}
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		slot := d.encoder.NextToolSlot()
		d.toolSlots[index] = slot
		d.toolArguments[index] = &strings.Builder{}
		d.hasToolCalls = true
		return d.encoder.EmitToolCallStart(slot, id, name)
	case "redacted_thinking":
		data, _ := block["data"].(string)
		return d.encoder.EmitRedactedReasoning(data)
	default:
		return nil
	}
}

func (d *anthropicStreamDecoder) handleBlockDelta(payload map[string]any) error {
	index, err := blockIndex(payload)
	if err != nil {
		return err
	}
	delta, _ := payload["delta"].(map[string]any)
	if delta == nil {
		return nil
	}
	switch deltaType, _ := delta["type"].(string); deltaType {
	case "text_delta":
		text, _ := delta["text"].(string)
		return d.encoder.EmitText(text)
	case "thinking_delta":
		text, _ := delta["thinking"].(string)
		return d.encoder.EmitReasoning(text)
	case "signature_delta":
		signature, _ := delta["signature"].(string)
		if signature == "" {
			return nil
		}
		builder, ok := d.signatures[index]
		if !ok {
			builder = &strings.Builder{}
			d.signatures[index] = builder
		}
		builder.WriteString(signature)
		return nil
	case "input_json_delta":
		slot, ok := d.toolSlots[index]
		if !ok {
			// Forwarding this against an arbitrary slot would corrupt an
			// unrelated tool call's arguments.
			return invalidProviderResponseError(fmt.Sprintf("provider sent tool arguments for unopened content block %d", index))
		}
		partial, _ := delta["partial_json"].(string)
		if builder, ok := d.toolArguments[index]; ok {
			builder.WriteString(partial)
		}
		return d.encoder.EmitToolCallArguments(slot, partial)
	default:
		// citations_delta and future delta types carry no OpenAI equivalent.
		return nil
	}
}

func (d *anthropicStreamDecoder) handleBlockStop(payload map[string]any) error {
	index, err := blockIndex(payload)
	if err != nil {
		return err
	}

	// A tool call whose arguments do not parse is unusable to the client, and
	// the truncation is invisible once the fragments have been forwarded.
	if builder, ok := d.toolArguments[index]; ok {
		delete(d.toolArguments, index)
		arguments := strings.TrimSpace(builder.String())
		if arguments == "" {
			// A tool taking no arguments produces no partial_json deltas, but
			// Anthropic still guarantees an object. Emitting nothing would leave
			// the client with an empty, unparsable arguments string.
			arguments = "{}"
			if slot, ok := d.toolSlots[index]; ok {
				if err := d.encoder.EmitToolCallArguments(slot, arguments); err != nil {
					return err
				}
			}
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return invalidProviderResponseError(fmt.Sprintf("provider sent tool arguments that are not a JSON object: %v", err))
		}
	}

	builder, ok := d.signatures[index]
	if !ok || builder.Len() == 0 {
		return nil
	}
	delete(d.signatures, index)
	return d.encoder.EmitReasoningSignature(anthropicSignatureProvider, builder.String())
}

// blockIndex reads a content block index, rejecting events that omit it.
// Defaulting a missing index to 0 would misroute deltas into the first block.
func blockIndex(payload map[string]any) (int, error) {
	value, present := payload["index"]
	if !present {
		return 0, invalidProviderResponseError("provider sent a content block event without an index")
	}
	number, ok := value.(float64)
	if !ok {
		return 0, invalidProviderResponseError("provider sent a non-numeric content block index")
	}
	return int(number), nil
}

// mergeUsage applies a usage snapshot. Anthropic reports cumulative totals, so
// fields are overwritten rather than summed.
func (d *anthropicStreamDecoder) mergeUsage(value any) {
	usage, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, item := range usage {
		if item == nil {
			continue
		}
		d.usage[key] = item
	}
}

func (d *anthropicStreamDecoder) currentUsage() Usage {
	return anthropicUsageFromRawMap(d.usage)
}

func anthropicStreamError(payload map[string]any, provider Provider) error {
	detail, _ := payload["error"].(map[string]any)
	message, _ := detail["message"].(string)
	if message == "" {
		message = "provider reported a stream error"
	}
	message = string(redactProviderErrorSecrets([]byte(message), provider))
	code, _ := detail["type"].(string)
	if code == "" {
		code = "provider_stream_error"
	}
	return NewHTTPError(502, code, fmt.Sprintf("Anthropic stream error: %s", message))
}
