package server

import (
	"fmt"
	"io"
)

// geminiStreamDecoder translates Gemini's streamGenerateContent SSE output into
// OpenAI chunk frames.
//
// Unlike Anthropic, Gemini does not stream partial tool arguments: each
// functionCall part arrives as a complete JSON object. The arguments are
// therefore emitted in a single delta rather than being reassembled.
type geminiStreamDecoder struct {
	encoder  *openAIChatStreamEncoder
	provider Provider

	usage        map[string]any
	finishReason string
	sawCandidate bool
	hasToolCalls bool
	toolIndex    int
}

func newGeminiStreamDecoder(encoder *openAIChatStreamEncoder) *geminiStreamDecoder {
	return &geminiStreamDecoder{encoder: encoder, usage: map[string]any{}}
}

// streamGeminiChat consumes the upstream SSE body and writes OpenAI chunks.
func streamGeminiChat(body io.Reader, encoder *openAIChatStreamEncoder) (Usage, error) {
	return streamGeminiChatForProvider(body, encoder, Provider{})
}

func streamGeminiChatForProvider(body io.Reader, encoder *openAIChatStreamEncoder, provider Provider) (Usage, error) {
	decoder := newGeminiStreamDecoder(encoder)
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
		if err := decoder.consume(event); err != nil {
			encoder.Abort()
			return decoder.currentUsage(), err
		}
	}
	usage := decoder.currentUsage()
	if !decoder.sawCandidate {
		encoder.Abort()
		return usage, invalidProviderResponseError("provider closed the stream before sending any content")
	}
	if decoder.finishReason == "" {
		// Gemini leaves finishReason empty while generation is still in
		// progress. Reaching EOF without one means the connection dropped, and
		// reporting a normal stop would hide the truncation from the client.
		encoder.Abort()
		return usage, invalidProviderResponseError("provider closed the stream before reporting a finish reason")
	}
	finishReason, err := geminiFinishReason(decoder.finishReason, decoder.hasToolCalls)
	if err != nil {
		encoder.Abort()
		return usage, err
	}
	if err := encoder.Finalize(finishReason, usage); err != nil {
		return usage, err
	}
	return usage, nil
}

func (d *geminiStreamDecoder) consume(event serverSentEvent) error {
	payload, err := decodeSSEData(event)
	if err != nil {
		return err
	}
	if payload == nil {
		return nil
	}
	if detail, ok := payload["error"].(map[string]any); ok {
		message, _ := detail["message"].(string)
		if message == "" {
			message = "provider reported a stream error"
		}
		message = string(redactProviderErrorSecrets([]byte(message), d.provider))
		return NewHTTPError(502, "provider_stream_error", fmt.Sprintf("Gemini stream error: %s", message))
	}
	if usage, ok := payload["usageMetadata"].(map[string]any); ok {
		// Gemini reports cumulative counters; keep the latest snapshot.
		for key, value := range usage {
			if value != nil {
				d.usage[key] = value
			}
		}
	}

	candidates, _ := payload["candidates"].([]any)
	if len(candidates) == 0 {
		if reason := geminiPromptBlockReason(payload); reason != "" {
			return newProviderPolicyRefusal(fmt.Sprintf("Gemini blocked the prompt: %s", reason))
		}
		return nil
	}
	candidate, _ := candidates[0].(map[string]any)
	if candidate == nil {
		return nil
	}
	d.sawCandidate = true
	if reason, ok := candidate["finishReason"].(string); ok && reason != "" {
		d.finishReason = reason
	}
	if err := d.encoder.EmitRole(); err != nil {
		return err
	}

	content, _ := candidate["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, item := range parts {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if err := d.consumePart(part); err != nil {
			return err
		}
	}
	return nil
}

func (d *geminiStreamDecoder) consumePart(part map[string]any) error {
	if call, ok := part["functionCall"].(map[string]any); ok {
		name, _ := call["name"].(string)
		id, _ := call["id"].(string)
		slot := d.encoder.NextToolSlot()
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", NewID("gemini"), slot)
		}
		d.hasToolCalls = true
		d.toolIndex++
		if err := d.encoder.EmitToolCallStart(slot, id, name); err != nil {
			return err
		}
		if err := d.encoder.EmitToolCallArguments(slot, encodeToolCallArguments(call["args"])); err != nil {
			return err
		}
		if signature, ok := part["thoughtSignature"].(string); ok && signature != "" {
			return d.encoder.EmitToolCallSignature(slot, geminiSignatureProvider, signature)
		}
		return nil
	}

	text, _ := part["text"].(string)
	if text == "" {
		return nil
	}
	if thought, _ := part["thought"].(bool); thought {
		return d.encoder.EmitReasoning(text)
	}
	return d.encoder.EmitText(text)
}

func (d *geminiStreamDecoder) currentUsage() Usage {
	return geminiUsage(map[string]any{"usageMetadata": d.usage})
}
