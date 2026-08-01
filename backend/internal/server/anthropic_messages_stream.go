package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) streamNativeAnthropicMessages(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
	writer io.Writer,
) (Usage, error) {
	payload := cloneAnyMap(req.Raw)
	payload["model"] = route.ProviderModel
	payload["stream"] = true
	resp, err := s.doNativeAnthropicRequest(ctx, route.Provider, "/v1/messages", payload, headers, true)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	return copyNativeAnthropicStream(writer, resp.Body, req.Model)
}

// copyNativeAnthropicStream forwards an upstream Anthropic event stream to the
// client, rewriting only the model name each data frame reports and collecting
// usage on the way past.
func copyNativeAnthropicStream(writer io.Writer, body io.Reader, model string) (Usage, error) {
	events := newSSEDecoder(body)
	var usage Usage
	for {
		event, err := events.Next()
		if err == io.EOF {
			return usage, nil
		}
		if err != nil {
			// A stream that fails mid-frame has already put those bytes on the
			// wire. They are forwarded unrewritten: the model-name swap needs a
			// whole frame, and the failure itself is what the client must see.
			if pending := events.Pending(); len(pending) > 0 {
				if _, writeErr := writer.Write(pending); writeErr != nil {
					return usage, writeErr
				}
			}
			return usage, err
		}
		output := event.Raw
		payload, ok, decodeErr := decodeSSEDataNumbers(event)
		if decodeErr != nil {
			return usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Anthropic provider returned invalid stream JSON")
		}
		if ok {
			if message, ok := payload["message"].(map[string]any); ok {
				message["model"] = model
				if eventUsage, ok := message["usage"].(map[string]any); ok {
					usage = mergeAnthropicStreamUsage(usage, eventUsage)
				}
			}
			if eventUsage, ok := payload["usage"].(map[string]any); ok {
				usage = mergeAnthropicStreamUsage(usage, eventUsage)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return usage, err
			}
			output = rewriteSSEEventData(event.Raw, string(encoded))
		}
		if _, err := writer.Write(output); err != nil {
			return usage, err
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func mergeAnthropicStreamUsage(current Usage, raw map[string]any) Usage {
	inputTokens := int64FromAny(raw["input_tokens"])
	cacheCreationInputTokens := int64FromAny(raw["cache_creation_input_tokens"])
	cacheReadInputTokens := int64FromAny(raw["cache_read_input_tokens"])
	if inputTokens > 0 || cacheCreationInputTokens > 0 || cacheReadInputTokens > 0 {
		current.PromptTokens = inputTokens + cacheCreationInputTokens + cacheReadInputTokens
		current.CachedInputTokens = cacheReadInputTokens
	}
	if value := int64FromAny(raw["output_tokens"]); value > 0 {
		current.CompletionTokens = value
	}
	current.TotalTokens = current.PromptTokens + current.CompletionTokens
	return current
}

func (s *Server) streamOpenAIAsAnthropic(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	writer io.Writer,
) (Usage, error) {
	chatReq, err := anthropicToOpenAIChatRequest(req)
	if err != nil {
		return Usage{}, err
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return Usage{}, err
	}
	converter := newOpenAIAnthropicStreamConverter(writer, req.Model, estimateAnthropicInputTokens(req.Raw))
	usage, err := adapter.ChatStream(ctx, route.Provider, route.ProviderModel, chatReq, converter)
	if err != nil {
		return usage, err
	}
	// The upstream may stop without a terminating blank line. Draining before the
	// usage fallbacks below keeps the last frame's tokens and text in the totals.
	if err := converter.closeStream(); err != nil {
		return usage, err
	}
	if usage.TotalTokens == 0 {
		usage = converter.usage
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateAnthropicInputTokens(req.Raw)
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = EstimateTextTokens(converter.outputText.String() + converter.toolArgumentText())
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if err := converter.Finalize(usage); err != nil {
		return usage, err
	}
	return usage, nil
}

type openAIStreamToolCall struct {
	Index     int
	ID        string
	Name      string
	Arguments strings.Builder
}

type openAIAnthropicStreamConverter struct {
	writer      io.Writer
	model       string
	messageID   string
	inputTokens int64
	events      *sseStreamWriter
	started     bool
	textStarted bool
	textIndex   int
	nextIndex   int
	finish      string
	finalized   bool
	tools       map[int]*openAIStreamToolCall
	usage       Usage
	outputText  strings.Builder
}

func newOpenAIAnthropicStreamConverter(writer io.Writer, model string, inputTokens int64) *openAIAnthropicStreamConverter {
	converter := &openAIAnthropicStreamConverter{
		writer:      writer,
		model:       model,
		messageID:   NewID("msg"),
		inputTokens: inputTokens,
		tools:       map[int]*openAIStreamToolCall{},
	}
	converter.events = newSSEStreamWriter(converter.consumeEvent)
	return converter
}

// Write accepts the upstream OpenAI stream as raw bytes. Framing is delegated to
// sseStreamWriter so this inbound path carries the same per-event size limit as
// every other stream the gateway parses.
func (c *openAIAnthropicStreamConverter) Write(data []byte) (int, error) {
	return c.events.Write(data)
}

// closeStream drains a frame the upstream left without a terminating blank line.
func (c *openAIAnthropicStreamConverter) closeStream() error {
	return c.events.Close()
}

func (c *openAIAnthropicStreamConverter) consumeEvent(frame serverSentEvent) error {
	event, ok, err := decodeSSEDataNumbers(frame)
	if err != nil {
		return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI provider returned invalid stream JSON")
	}
	if !ok {
		return nil
	}
	if id, ok := event["id"].(string); ok && id != "" {
		c.messageID = id
	}
	if parsed := usageFromMap(event); parsed.TotalTokens > 0 {
		c.usage = parsed
	}
	choices, ok := anySlice(event["choices"])
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream choice is invalid")
	}
	if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
		c.finish = finish
	}
	delta, _ := choice["delta"].(map[string]any)
	if text, ok := delta["content"].(string); ok && text != "" {
		if err := c.startMessage(); err != nil {
			return err
		}
		if !c.textStarted {
			c.textIndex = c.nextIndex
			c.nextIndex++
			c.textStarted = true
			if err := c.emit("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": c.textIndex,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			}); err != nil {
				return err
			}
		}
		c.outputText.WriteString(text)
		if err := c.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": c.textIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
		}); err != nil {
			return err
		}
	}
	rawToolCalls, _ := anySlice(delta["tool_calls"])
	for _, item := range rawToolCalls {
		call, ok := item.(map[string]any)
		if !ok {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call is invalid")
		}
		index := int(int64FromAny(call["index"]))
		current := c.tools[index]
		if current == nil {
			current = &openAIStreamToolCall{Index: index}
			c.tools[index] = current
		}
		if id, ok := call["id"].(string); ok {
			current.ID += id
		}
		if function, ok := call["function"].(map[string]any); ok {
			if name, ok := function["name"].(string); ok {
				current.Name += name
			}
			if arguments, ok := function["arguments"].(string); ok {
				current.Arguments.WriteString(arguments)
			}
		}
	}
	return nil
}

func (c *openAIAnthropicStreamConverter) startMessage() error {
	if c.started {
		return nil
	}
	c.started = true
	return c.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            c.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         c.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  c.inputTokens,
				"output_tokens": int64(0),
			},
		},
	})
}

func (c *openAIAnthropicStreamConverter) Finalize(usage Usage) error {
	if c.finalized {
		return nil
	}
	c.finalized = true
	if err := c.closeStream(); err != nil {
		return err
	}
	if err := c.startMessage(); err != nil {
		return err
	}
	if c.textStarted {
		if err := c.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": c.textIndex,
		}); err != nil {
			return err
		}
	}
	toolIndexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Ints(toolIndexes)
	for _, toolIndex := range toolIndexes {
		call := c.tools[toolIndex]
		if call.ID == "" || call.Name == "" {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call requires id and function name")
		}
		arguments := call.Arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		var input any
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "OpenAI stream tool call arguments are not valid JSON")
		}
		index := c.nextIndex
		c.nextIndex++
		if err := c.emit("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		if err := c.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": arguments,
			},
		}); err != nil {
			return err
		}
		if err := c.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}); err != nil {
			return err
		}
	}
	stopReason := openAIFinishReasonToAnthropic(c.finish, len(c.tools) > 0)
	if err := c.emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": usage.CompletionTokens,
		},
	}); err != nil {
		return err
	}
	return c.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (c *openAIAnthropicStreamConverter) emit(event string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if flusher, ok := c.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (c *openAIAnthropicStreamConverter) toolArgumentText() string {
	var output strings.Builder
	indexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		output.WriteString(c.tools[index].Arguments.String())
	}
	return output.String()
}
