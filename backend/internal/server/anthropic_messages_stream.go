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
	payload := nativeAnthropicPayload(req.Raw)
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
	sawEvent := false
	sawMessageStop := false
	for {
		event, err := events.Next()
		if err == io.EOF {
			// A stream that delivered events but never reached message_stop was
			// truncated: the client saw bytes, then the stream died. An empty
			// body stays a confirmed empty success, which the handler commits.
			if sawEvent && !sawMessageStop {
				return usage, NewHTTPError(http.StatusBadGateway, "provider_stream_error", "Anthropic provider stream ended before message_stop")
			}
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
			message, rewrite := payload["message"].(map[string]any)
			if rewrite {
				message["model"] = model
				if eventUsage, ok := message["usage"].(map[string]any); ok {
					usage = mergeAnthropicStreamUsage(usage, eventUsage)
				}
			}
			if eventUsage, ok := payload["usage"].(map[string]any); ok {
				usage = mergeAnthropicStreamUsage(usage, eventUsage)
			}
			// Only a frame carrying a model name needs rewriting. Re-encoding the
			// rest would reorder their JSON keys and re-frame their data lines for
			// no reason; forwarding the provider's own bytes is both faithful and
			// cheaper, and every frame but message_start takes this path.
			if rewrite {
				encoded, err := json.Marshal(payload)
				if err != nil {
					return usage, err
				}
				output = rewriteSSEEventData(event.Raw, string(encoded))
			}
		}
		if event.Event != "" || len(event.Data) > 0 {
			sawEvent = true
		}
		if event.Event == "error" || (ok && payload["type"] == "error") {
			// The upstream's terminal error frame is forwarded as-is — the client
			// must see it — then the stream is reported failed. The marker tells
			// the handler the terminal event already reached the client, so it
			// must not append a second one.
			if _, err := writer.Write(output); err != nil {
				return usage, err
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			errorType, message := "api_error", "Anthropic provider stream error"
			if errorObj, ok := payload["error"].(map[string]any); ok {
				if value, ok := errorObj["type"].(string); ok {
					errorType = value
				}
				if value, ok := errorObj["message"].(string); ok {
					message = value
				}
			}
			return usage, &anthropicErrorFrameForwarded{err: NewHTTPError(anthropicErrorStatus(errorType), "provider_stream_error", message)}
		}
		if event.Event == "message_stop" || (ok && payload["type"] == "message_stop") {
			sawMessageStop = true
		}
		if _, err := writer.Write(output); err != nil {
			return usage, err
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

// anthropicErrorFrameForwarded marks a native Anthropic stream whose upstream
// event: error frame was already forwarded to the client. The handler must
// classify the stream as failed without appending a second terminal error
// event.
type anthropicErrorFrameForwarded struct {
	err error
}

func (e *anthropicErrorFrameForwarded) Error() string { return e.err.Error() }

func (e *anthropicErrorFrameForwarded) Unwrap() error { return e.err }

// anthropicErrorStatus maps a native Anthropic error type to the HTTP status
// the gateway reports for it. Unrecognized types are upstream failures, so
// they surface as gateway errors rather than client errors.
func anthropicErrorStatus(errorType string) int {
	switch errorType {
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "overloaded_error":
		return http.StatusServiceUnavailable
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "invalid_request_error", "request_too_large":
		return http.StatusBadRequest
	case "not_found_error":
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

// mergeAnthropicStreamUsage folds one streamed usage snapshot into the running
// total. A stream splits its usage across message_start and message_delta, and
// a frame that reports nothing new omits the fields entirely, so a frame only
// overwrites what it actually carries. The three input classes move together as
// one group: any positive component replaces the whole input side, including
// siblings the frame left out, because a frame that restates the input counts
// restates all of them. The output count is merged on its own.
func mergeAnthropicStreamUsage(current Usage, raw map[string]any) Usage {
	snapshot := anthropicUsageFromRawMap(raw)
	if snapshot.PromptTokens > 0 || snapshot.CachedInputTokens > 0 || snapshot.CacheWriteInputTokens > 0 {
		current.PromptTokens = snapshot.PromptTokens
		current.CachedInputTokens = snapshot.CachedInputTokens
		// CacheWriteInputTokens is knowingly left unmerged here. This path has
		// never reported it, and starting to would change billed usage, which is
		// a behavior change rather than part of consolidating the parsing.
	}
	if snapshot.CompletionTokens > 0 {
		current.CompletionTokens = snapshot.CompletionTokens
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
	if raw, ok := event["error"]; ok {
		// The upstream signaled a terminal error inside the 200 stream. Emit an
		// Anthropic error event so the client sees the failure, then fail the
		// stream through the forwarded marker so the handler does not append a
		// second terminal event.
		errorType, message := "api_error", "OpenAI provider stream error"
		if errorObj, ok := raw.(map[string]any); ok {
			if value, ok := errorObj["type"].(string); ok && value != "" {
				errorType = value
			}
			if value, ok := errorObj["message"].(string); ok && value != "" {
				message = value
			}
		}
		if err := c.emit("error", map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errorType, "message": message},
		}); err != nil {
			return err
		}
		return &anthropicErrorFrameForwarded{err: NewHTTPError(openAIErrorStatus(errorType), "provider_stream_error", message)}
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
