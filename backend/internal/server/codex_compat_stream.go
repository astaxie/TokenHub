package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// streamCodexAsAnthropic translates the Codex Responses event stream into the
// Anthropic Messages event stream expected by Claude Code.
func (s *Server) streamCodexAsAnthropic(
	ctx context.Context,
	route RouteSelection,
	req anthropicMessagesRequest,
	headers http.Header,
	writer io.Writer,
) (Usage, error) {
	upstream, err := anthropicToCodexResponsesRequest(req)
	if err != nil {
		return Usage{}, err
	}
	applyClaudeCodeCodexToolConstraints(&upstream, headers)
	reverseNames, err := anthropicCodexToolNameReverse(req.Raw["tools"])
	if err != nil {
		return Usage{}, err
	}
	sink := newCodexAnthropicStreamSink(
		writer,
		req.Model,
		estimateAnthropicInputTokens(req.Raw),
		reverseNames,
	)
	return s.streamCodexCompatibility(ctx, route, upstream, codexAnthropicCompatibilityHeaders(headers, req.Raw), sink)
}

// streamCodexAsChat translates the Codex Responses event stream into OpenAI
// chat.completion.chunk frames.
func (s *Server) streamCodexAsChat(
	ctx context.Context,
	route RouteSelection,
	req ChatCompletionRequest,
	headers http.Header,
	writer io.Writer,
) (Usage, error) {
	upstream, err := chatToCodexResponsesRequest(req)
	if err != nil {
		return Usage{}, err
	}
	reverseNames, err := chatCodexToolNameReverse(req.Tools)
	if err != nil {
		return Usage{}, err
	}
	sink := &codexChatStreamSink{
		encoder:      newOpenAIChatStreamEncoder(writer, req.Model, streamUsageRequested(req)),
		reverseNames: reverseNames,
		toolSlots:    map[string]int{},
	}
	return s.streamCodexCompatibility(ctx, route, upstream, codexChatCompatibilityHeaders(headers, req), sink)
}

func (s *Server) streamCodexCompatibility(
	ctx context.Context,
	route RouteSelection,
	request ResponsesRequest,
	headers http.Header,
	sink codexResponsesStreamSink,
) (Usage, error) {
	adapter, err := s.responsesAdapterForRoute(route)
	if err != nil {
		return Usage{}, err
	}
	opener, ok := adapter.(ResponsesStreamOpener)
	if !ok {
		return Usage{}, NewHTTPError(
			http.StatusBadRequest,
			"adapter_capability_unsupported",
			"Provider adapter does not support streaming Responses",
		)
	}
	opened, err := opener.OpenResponses(
		ctx,
		route.Provider,
		route.ProviderModel,
		request,
		codexCompatibilityHeaders(headers),
	)
	if err != nil {
		if isCodexModelUnsupportedError(err) {
			s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
		}
		return Usage{}, err
	}
	defer opened.Body.Close()

	usage, streamErr := consumeCodexCompatibilityStream(opened.Body, sink)
	applyCodexResponseMetadata(&usage, opened.Header)
	if isCodexModelUnsupportedError(streamErr) {
		s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
	}
	return usage, streamErr
}

type codexResponsesStreamSink interface {
	Start(response map[string]any) error
	TextDelta(delta string) error
	TextDone() error
	ReasoningDelta(delta string) error
	ReasoningDone(signature string) error
	ToolStart(key string, id string, name string) error
	ToolArguments(key string, delta string) error
	ToolDone(key string) error
	Finalize(response map[string]any, usage Usage, hasToolCalls bool) error
	Abort()
}

type codexResponsesStreamDecoder struct {
	sink codexResponsesStreamSink

	functions       map[string]*codexStreamFunction
	functionStates  []*codexStreamFunction
	reasoning       map[string]*codexStreamReasoning
	reasoningStates []*codexStreamReasoning
	lastReasoning   *codexStreamReasoning
	textSeen        map[string]bool
	textSeenAny     bool

	usage        Usage
	hasToolCalls bool
	sawTerminal  bool
}

type codexStreamFunction struct {
	key          string
	callID       string
	itemID       string
	name         string
	arguments    strings.Builder
	emittedBytes int
	sawDelta     bool
	started      bool
	done         bool
}

type codexStreamReasoning struct {
	signature        string
	parts            int
	separatorPending bool
	emittedText      bool
	done             bool
}

func consumeCodexCompatibilityStream(body io.Reader, sink codexResponsesStreamSink) (Usage, error) {
	decoder := &codexResponsesStreamDecoder{
		sink:      sink,
		functions: map[string]*codexStreamFunction{},
		reasoning: map[string]*codexStreamReasoning{},
		textSeen:  map[string]bool{},
	}
	events := newSSEDecoder(body)
	for {
		event, err := events.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			sink.Abort()
			return decoder.usage, err
		}
		done, err := decoder.consume(event)
		if err != nil {
			sink.Abort()
			return decoder.usage, err
		}
		if done {
			return decoder.usage, nil
		}
	}
	sink.Abort()
	if decoder.sawTerminal {
		return decoder.usage, nil
	}
	return decoder.usage, NewHTTPError(
		http.StatusBadGateway,
		"codex_stream_incomplete",
		"Codex stream ended before a terminal response event",
	)
}

func (d *codexResponsesStreamDecoder) consume(event serverSentEvent) (bool, error) {
	if strings.TrimSpace(event.Data) == "[DONE]" {
		return false, NewHTTPError(
			http.StatusBadGateway,
			"codex_stream_incomplete",
			"Codex stream ended before a terminal response event",
		)
	}
	payload, err := decodeSSEData(event)
	if err != nil {
		return false, err
	}
	if payload == nil {
		return false, nil
	}
	eventType, _ := payload["type"].(string)
	if eventType == "" {
		eventType = event.Event
	}

	switch eventType {
	case "response.created":
		response, _ := payload["response"].(map[string]any)
		if response == nil {
			return false, invalidProviderResponseError("Codex response.created event is missing response")
		}
		return false, d.sink.Start(response)
	case "response.output_item.added":
		return false, d.outputItemAdded(payload)
	case "response.reasoning_summary_part.added":
		state := d.reasoningFor(payload, nil)
		if state.parts > 0 {
			state.separatorPending = true
		}
		state.parts++
		return false, nil
	case "response.reasoning_summary_text.delta":
		state := d.reasoningFor(payload, nil)
		if state.separatorPending {
			if err := d.sink.ReasoningDelta("\n\n"); err != nil {
				return false, err
			}
			state.separatorPending = false
		}
		delta, _ := payload["delta"].(string)
		if delta != "" {
			state.emittedText = true
		}
		return false, d.sink.ReasoningDelta(delta)
	case "response.content_part.done":
		if err := d.contentPartDone(payload); err != nil {
			return false, err
		}
		return false, nil
	case "response.output_text.delta":
		delta, _ := payload["delta"].(string)
		if delta != "" {
			d.markTextSeen(payload, nil)
		}
		return false, d.sink.TextDelta(delta)
	case "response.output_text.done":
		if !d.hasTextSeen(payload, nil) {
			text, _ := payload["text"].(string)
			if text != "" {
				d.markTextSeen(payload, nil)
				if err := d.sink.TextDelta(text); err != nil {
					return false, err
				}
			}
		}
		return false, d.sink.TextDone()
	case "response.refusal.delta":
		delta, _ := payload["delta"].(string)
		if delta != "" {
			d.markTextSeen(payload, nil)
		}
		return false, d.sink.TextDelta(delta)
	case "response.refusal.done":
		if !d.hasTextSeen(payload, nil) {
			text, _ := payload["refusal"].(string)
			if text != "" {
				d.markTextSeen(payload, nil)
				if err := d.sink.TextDelta(text); err != nil {
					return false, err
				}
			}
		}
		return false, d.sink.TextDone()
	case "response.function_call_arguments.delta":
		return false, d.functionArgumentsDelta(payload)
	case "response.function_call_arguments.done":
		return false, d.functionArgumentsDone(payload)
	case "response.output_item.done":
		return false, d.outputItemDone(payload)
	case "response.completed", "response.incomplete", "response.done":
		response, _ := payload["response"].(map[string]any)
		if response == nil {
			return false, invalidProviderResponseError("Codex terminal event is missing response")
		}
		d.usage = usageFromMap(response)
		if err := d.hydrateTerminal(response); err != nil {
			return false, err
		}
		d.sawTerminal = true
		if err := d.sink.Finalize(response, d.usage, d.hasToolCalls); err != nil {
			return false, err
		}
		return true, nil
	case "response.failed", "error":
		return false, codexStreamEventError(payload)
	default:
		return false, nil
	}
}

func (d *codexResponsesStreamDecoder) outputItemAdded(payload map[string]any) error {
	item, _ := payload["item"].(map[string]any)
	if item == nil {
		return nil
	}
	switch item["type"] {
	case "reasoning":
		state := d.reasoningFor(payload, item)
		state.signature, _ = item["encrypted_content"].(string)
	case "function_call":
		d.hasToolCalls = true
		state, err := d.functionFor(payload, item)
		if err != nil {
			return err
		}
		d.hydrateFunction(state, item)
		return d.startFunction(state)
	}
	return nil
}

func (d *codexResponsesStreamDecoder) outputItemDone(payload map[string]any) error {
	item, _ := payload["item"].(map[string]any)
	if item == nil {
		return nil
	}
	switch item["type"] {
	case "reasoning":
		state := d.reasoningFor(payload, item)
		return d.finishReasoning(state, item)
	case "message":
		if !d.hasTextSeen(payload, item) {
			if text := codexStreamMessageText(item); text != "" {
				d.markTextSeen(payload, item)
				if err := d.sink.TextDelta(text); err != nil {
					return err
				}
			}
		}
		return d.sink.TextDone()
	case "function_call":
		d.hasToolCalls = true
		state, err := d.functionFor(payload, item)
		if err != nil {
			return err
		}
		return d.finishFunction(state, item)
	}
	return nil
}

func (d *codexResponsesStreamDecoder) contentPartDone(payload map[string]any) error {
	part, _ := payload["part"].(map[string]any)
	if part == nil || (part["type"] != "output_text" && part["type"] != "refusal") {
		return nil
	}
	if !d.hasTextSeen(payload, nil) {
		text, _ := part["text"].(string)
		if text == "" {
			text, _ = part["refusal"].(string)
		}
		if text != "" {
			d.markTextSeen(payload, nil)
			if err := d.sink.TextDelta(text); err != nil {
				return err
			}
		}
	}
	return d.sink.TextDone()
}

func (d *codexResponsesStreamDecoder) reasoningFor(
	payload map[string]any,
	item map[string]any,
) *codexStreamReasoning {
	aliases := codexStreamAliases(payload, item)
	for _, alias := range aliases {
		if state := d.reasoning[alias]; state != nil {
			for _, next := range aliases {
				d.reasoning[next] = state
			}
			d.lastReasoning = state
			return state
		}
	}
	if len(aliases) == 0 && d.lastReasoning != nil && !d.lastReasoning.done {
		return d.lastReasoning
	}
	state := &codexStreamReasoning{}
	d.reasoningStates = append(d.reasoningStates, state)
	for _, alias := range aliases {
		d.reasoning[alias] = state
	}
	d.lastReasoning = state
	return state
}

func (d *codexResponsesStreamDecoder) finishReasoning(
	state *codexStreamReasoning,
	item map[string]any,
) error {
	if state.done {
		return nil
	}
	if !state.emittedText {
		if text := codexStreamReasoningText(item); text != "" {
			if err := d.sink.ReasoningDelta(text); err != nil {
				return err
			}
			state.emittedText = true
		}
	}
	if signature, _ := item["encrypted_content"].(string); signature != "" {
		state.signature = signature
	}
	state.done = true
	return d.sink.ReasoningDone(state.signature)
}

func (d *codexResponsesStreamDecoder) functionFor(
	payload map[string]any,
	item map[string]any,
) (*codexStreamFunction, error) {
	aliases := codexStreamAliases(payload, item)
	if len(aliases) == 0 {
		return nil, invalidProviderResponseError("Codex function call event has no stable identifier")
	}
	var state *codexStreamFunction
	for _, alias := range aliases {
		if existing := d.functions[alias]; existing != nil {
			state = existing
			break
		}
	}
	if state == nil {
		state = &codexStreamFunction{key: aliases[0]}
		d.functionStates = append(d.functionStates, state)
	}
	for _, alias := range aliases {
		d.functions[alias] = state
	}
	return state, nil
}

func (d *codexResponsesStreamDecoder) hydrateFunction(
	state *codexStreamFunction,
	item map[string]any,
) {
	if item == nil {
		return
	}
	if value, _ := item["call_id"].(string); value != "" {
		state.callID = value
	}
	if value, _ := item["id"].(string); value != "" {
		state.itemID = value
	}
	if value, _ := item["name"].(string); value != "" {
		state.name = value
	}
	if !state.sawDelta && state.arguments.Len() == 0 {
		if arguments, ok := codexStreamArguments(item["arguments"]); ok && arguments != "" {
			state.arguments.WriteString(arguments)
		}
	}
}

func (d *codexResponsesStreamDecoder) startFunction(state *codexStreamFunction) error {
	if state.started || state.done || state.name == "" {
		return nil
	}
	callID := state.callID
	if callID == "" {
		callID = state.itemID
	}
	if callID == "" {
		return nil
	}
	if err := d.sink.ToolStart(state.key, callID, state.name); err != nil {
		return err
	}
	state.started = true
	if !state.sawDelta {
		// A full arguments snapshot is validated at output_item.done before it
		// reaches the client. Only genuine delta events stream immediately.
		return nil
	}
	return d.flushFunctionArguments(state)
}

func (d *codexResponsesStreamDecoder) functionArgumentsDelta(payload map[string]any) error {
	state, err := d.functionFor(payload, nil)
	if err != nil {
		return err
	}
	delta, _ := payload["delta"].(string)
	if delta == "" {
		return nil
	}
	state.sawDelta = true
	state.arguments.WriteString(delta)
	return d.flushFunctionArguments(state)
}

func (d *codexResponsesStreamDecoder) functionArgumentsDone(payload map[string]any) error {
	state, err := d.functionFor(payload, nil)
	if err != nil {
		return err
	}
	if !state.sawDelta && state.arguments.Len() == 0 {
		if arguments, ok := codexStreamArguments(payload["arguments"]); ok {
			state.arguments.WriteString(arguments)
		}
	}
	return nil
}

func (d *codexResponsesStreamDecoder) flushFunctionArguments(state *codexStreamFunction) error {
	if !state.started || state.emittedBytes >= state.arguments.Len() {
		return nil
	}
	arguments := state.arguments.String()
	delta := arguments[state.emittedBytes:]
	if err := d.sink.ToolArguments(state.key, delta); err != nil {
		return err
	}
	state.emittedBytes = len(arguments)
	return nil
}

func (d *codexResponsesStreamDecoder) finishFunction(
	state *codexStreamFunction,
	item map[string]any,
) error {
	if state.done {
		return nil
	}
	d.hydrateFunction(state, item)
	if state.arguments.Len() == 0 {
		state.arguments.WriteString("{}")
	}
	if err := validateCodexStreamArguments(state.arguments.String()); err != nil {
		return err
	}
	if err := d.startFunction(state); err != nil {
		return err
	}
	if !state.started {
		return invalidProviderResponseError("Codex function call is missing call_id or name")
	}
	if err := d.flushFunctionArguments(state); err != nil {
		return err
	}
	if err := d.sink.ToolDone(state.key); err != nil {
		return err
	}
	state.done = true
	return nil
}

func (d *codexResponsesStreamDecoder) hydrateTerminal(response map[string]any) error {
	output, _ := anySlice(response["output"])
	for index, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		payload := map[string]any{"output_index": index}
		switch item["type"] {
		case "reasoning":
			state := d.reasoningFor(payload, item)
			if err := d.finishReasoning(state, item); err != nil {
				return err
			}
		case "message":
			if !d.hasTextSeen(payload, item) {
				if text := codexStreamMessageText(item); text != "" {
					d.markTextSeen(payload, item)
					if err := d.sink.TextDelta(text); err != nil {
						return err
					}
				}
			}
			if err := d.sink.TextDone(); err != nil {
				return err
			}
		case "function_call":
			d.hasToolCalls = true
			state, err := d.functionFor(payload, item)
			if err != nil {
				return err
			}
			if err := d.finishFunction(state, item); err != nil {
				return err
			}
		}
	}
	if !d.textSeenAny {
		if text := codexResponseOutputText(response); text != "" {
			d.markTextSeen(nil, nil)
			if err := d.sink.TextDelta(text); err != nil {
				return err
			}
			if err := d.sink.TextDone(); err != nil {
				return err
			}
		}
	}
	for _, state := range d.reasoningStates {
		if !state.done {
			if err := d.finishReasoning(state, nil); err != nil {
				return err
			}
		}
	}
	for _, state := range d.functionStates {
		if !state.done {
			if err := d.finishFunction(state, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *codexResponsesStreamDecoder) hasTextSeen(
	payload map[string]any,
	item map[string]any,
) bool {
	aliases := codexStreamAliases(payload, item)
	if len(aliases) == 0 {
		return d.textSeenAny
	}
	for _, alias := range aliases {
		if d.textSeen[alias] {
			return true
		}
	}
	return false
}

func (d *codexResponsesStreamDecoder) markTextSeen(
	payload map[string]any,
	item map[string]any,
) {
	d.textSeenAny = true
	for _, alias := range codexStreamAliases(payload, item) {
		d.textSeen[alias] = true
	}
}

func codexStreamAliases(payload map[string]any, item map[string]any) []string {
	aliases := make([]string, 0, 4)
	if value, exists := payload["output_index"]; exists {
		aliases = append(aliases, fmt.Sprintf("output:%d", int64FromAny(value)))
	}
	for _, candidate := range []struct {
		prefix string
		value  any
	}{
		{prefix: "item:", value: payload["item_id"]},
		{prefix: "call:", value: payload["call_id"]},
		{prefix: "item:", value: item["id"]},
		{prefix: "call:", value: item["call_id"]},
	} {
		if value, ok := candidate.value.(string); ok && value != "" {
			aliases = append(aliases, candidate.prefix+value)
		}
	}
	if len(aliases) < 2 {
		return aliases
	}
	seen := make(map[string]struct{}, len(aliases))
	result := aliases[:0]
	for _, alias := range aliases {
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, alias)
	}
	return result
}

func codexStreamArguments(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case map[string]any:
		encoded, err := json.Marshal(typed)
		return string(encoded), err == nil
	case nil:
		return "", false
	default:
		return "", false
	}
}

func validateCodexStreamArguments(arguments string) error {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded == nil {
		if err == nil {
			err = fmt.Errorf("arguments are not a JSON object")
		}
		return invalidProviderResponseError(
			fmt.Sprintf("Codex function call arguments are invalid: %v", err),
		)
	}
	return nil
}

func codexStreamMessageText(item map[string]any) string {
	var result strings.Builder
	content, _ := anySlice(item["content"])
	for _, rawPart := range content {
		part, _ := rawPart.(map[string]any)
		switch part["type"] {
		case "output_text":
			text, _ := part["text"].(string)
			result.WriteString(text)
		case "refusal":
			text, _ := part["refusal"].(string)
			result.WriteString(text)
		}
	}
	return result.String()
}

func codexStreamReasoningText(item map[string]any) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"summary", "content"} {
		if text, ok := item[key].(string); ok && text != "" {
			return text
		}
		parts, _ := anySlice(item[key])
		texts := make([]string, 0, len(parts))
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			if text, _ := part["text"].(string); text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n\n")
		}
	}
	return ""
}

type codexAnthropicStreamSink struct {
	writer       io.Writer
	model        string
	inputTokens  int64
	reverseNames map[string]string

	started        bool
	finalized      bool
	messageID      string
	nextIndex      int
	hasBlock       bool
	textOpen       bool
	textIndex      int
	reasoningOpen  bool
	reasoningIndex int
	reasoningText  strings.Builder
	tools          map[string]*codexAnthropicPendingTool
	toolQueue      []*codexAnthropicPendingTool
}

type codexAnthropicPendingTool struct {
	key       string
	id        string
	name      string
	arguments strings.Builder
	done      bool
}

func newCodexAnthropicStreamSink(
	writer io.Writer,
	model string,
	inputTokens int64,
	reverseNames map[string]string,
) *codexAnthropicStreamSink {
	return &codexAnthropicStreamSink{
		writer:       writer,
		model:        model,
		inputTokens:  inputTokens,
		reverseNames: reverseNames,
		tools:        map[string]*codexAnthropicPendingTool{},
	}
}

func (s *codexAnthropicStreamSink) Start(response map[string]any) error {
	if s.started {
		return nil
	}
	s.started = true
	if response != nil {
		s.messageID, _ = response["id"].(string)
	}
	if s.messageID == "" {
		s.messageID = NewID("msg")
	}
	return s.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": anthropicUsageObject(Usage{
				PromptTokens: s.inputTokens,
				TotalTokens:  s.inputTokens,
			}),
		},
	})
}

func (s *codexAnthropicStreamSink) TextDelta(delta string) error {
	if delta == "" {
		return nil
	}
	if err := s.closeReasoning(); err != nil {
		return err
	}
	if !s.textOpen {
		if err := s.Start(nil); err != nil {
			return err
		}
		s.textIndex = s.allocateIndex()
		s.textOpen = true
		s.hasBlock = true
		if err := s.emit("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         s.textIndex,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
	}
	return s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.textIndex,
		"delta": map[string]any{"type": "text_delta", "text": delta},
	})
}

func (s *codexAnthropicStreamSink) TextDone() error {
	return s.closeText()
}

func (s *codexAnthropicStreamSink) ReasoningDelta(delta string) error {
	s.reasoningText.WriteString(delta)
	return nil
}

func (s *codexAnthropicStreamSink) ReasoningDone(signature string) error {
	if signature == "" {
		s.reasoningText.Reset()
		return nil
	}
	if err := s.closeText(); err != nil {
		return err
	}
	if err := s.openReasoning(); err != nil {
		return err
	}
	if s.reasoningText.Len() > 0 {
		if err := s.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": s.reasoningIndex,
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": s.reasoningText.String(),
			},
		}); err != nil {
			return err
		}
	}
	s.reasoningText.Reset()
	if err := s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.reasoningIndex,
		"delta": map[string]any{
			"type":      "signature_delta",
			"signature": encodeProviderSignature(codexSignatureProvider, signature),
		},
	}); err != nil {
		return err
	}
	return s.closeReasoning()
}

func (s *codexAnthropicStreamSink) ToolStart(
	key string,
	id string,
	name string,
) error {
	if _, exists := s.tools[key]; exists {
		return invalidProviderResponseError("Codex reopened a function call")
	}
	if err := s.closeText(); err != nil {
		return err
	}
	if err := s.closeReasoning(); err != nil {
		return err
	}
	tool := &codexAnthropicPendingTool{
		key:  key,
		id:   codexShortIdentifier(id),
		name: codexRestoreToolName(s.reverseNames, name),
	}
	s.tools[key] = tool
	s.toolQueue = append(s.toolQueue, tool)
	return nil
}

func (s *codexAnthropicStreamSink) ToolArguments(key string, delta string) error {
	if delta == "" {
		return nil
	}
	tool, ok := s.tools[key]
	if !ok {
		return invalidProviderResponseError("Codex sent arguments for an unopened function call")
	}
	tool.arguments.WriteString(delta)
	return nil
}

func (s *codexAnthropicStreamSink) ToolDone(key string) error {
	tool, ok := s.tools[key]
	if !ok {
		return invalidProviderResponseError("Codex closed an unopened function call")
	}
	tool.done = true
	return s.drainTools()
}

func (s *codexAnthropicStreamSink) Finalize(
	response map[string]any,
	usage Usage,
	hasToolCalls bool,
) error {
	if s.finalized {
		return nil
	}
	s.finalized = true
	if err := s.Start(response); err != nil {
		return err
	}
	if err := s.closeText(); err != nil {
		return err
	}
	if err := s.closeReasoning(); err != nil {
		return err
	}
	if err := s.drainTools(); err != nil {
		return err
	}
	if len(s.toolQueue) != 0 {
		return invalidProviderResponseError("Codex ended with an incomplete function call")
	}
	if !s.hasBlock {
		index := s.allocateIndex()
		if err := s.emit("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		if err := s.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}); err != nil {
			return err
		}
	}
	if err := s.emit("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   codexAnthropicStopReason(response, hasToolCalls),
			"stop_sequence": codexStopSequence(response),
		},
		"usage": map[string]any{"output_tokens": usage.CompletionTokens},
	}); err != nil {
		return err
	}
	return s.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (s *codexAnthropicStreamSink) Abort() {
	s.finalized = true
}

func (s *codexAnthropicStreamSink) openReasoning() error {
	if s.reasoningOpen {
		return nil
	}
	if err := s.Start(nil); err != nil {
		return err
	}
	s.reasoningIndex = s.allocateIndex()
	s.reasoningOpen = true
	s.hasBlock = true
	return s.emit("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.reasoningIndex,
		"content_block": map[string]any{"type": "thinking", "thinking": ""},
	})
}

func (s *codexAnthropicStreamSink) closeText() error {
	if !s.textOpen {
		return nil
	}
	s.textOpen = false
	return s.emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.textIndex,
	})
}

func (s *codexAnthropicStreamSink) closeReasoning() error {
	if !s.reasoningOpen {
		return nil
	}
	s.reasoningOpen = false
	return s.emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.reasoningIndex,
	})
}

// drainTools serializes completed Responses function calls into whole Anthropic
// content blocks. Responses may interleave argument deltas for several calls,
// while Anthropic requires one content block to close before the next starts.
func (s *codexAnthropicStreamSink) drainTools() error {
	for len(s.toolQueue) > 0 && s.toolQueue[0].done {
		tool := s.toolQueue[0]
		s.toolQueue = s.toolQueue[1:]
		delete(s.tools, tool.key)
		if err := s.Start(nil); err != nil {
			return err
		}
		index := s.allocateIndex()
		s.hasBlock = true
		if err := s.emit("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    tool.id,
				"name":  tool.name,
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		if tool.arguments.Len() > 0 {
			if err := s.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": tool.arguments.String(),
				},
			}); err != nil {
				return err
			}
		}
		if err := s.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *codexAnthropicStreamSink) allocateIndex() int {
	index := s.nextIndex
	s.nextIndex++
	return index
}

func (s *codexAnthropicStreamSink) emit(event string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	if flusher, ok := s.writer.(streamFlusher); ok {
		flusher.Flush()
	}
	return nil
}

type codexChatStreamSink struct {
	encoder                   *openAIChatStreamEncoder
	reverseNames              map[string]string
	toolSlots                 map[string]int
	pendingReasoningSignature string
}

func (s *codexChatStreamSink) Start(map[string]any) error {
	return s.encoder.EmitRole()
}

func (s *codexChatStreamSink) TextDelta(delta string) error {
	return s.encoder.EmitText(delta)
}

func (s *codexChatStreamSink) TextDone() error {
	return nil
}

func (s *codexChatStreamSink) ReasoningDelta(delta string) error {
	return s.encoder.EmitReasoning(delta)
}

func (s *codexChatStreamSink) ReasoningDone(signature string) error {
	s.pendingReasoningSignature = signature
	return s.encoder.EmitReasoningSignature(codexSignatureProvider, signature)
}

func (s *codexChatStreamSink) ToolStart(
	key string,
	id string,
	name string,
) error {
	if _, exists := s.toolSlots[key]; exists {
		return invalidProviderResponseError("Codex reopened a function call")
	}
	slot := s.encoder.NextToolSlot()
	s.toolSlots[key] = slot
	toolCallID := codexShortIdentifier(id)
	if err := s.encoder.EmitToolCallStart(
		slot,
		toolCallID,
		codexRestoreToolName(s.reverseNames, name),
	); err != nil {
		return err
	}
	if s.pendingReasoningSignature == "" {
		return nil
	}
	signature := s.pendingReasoningSignature
	s.pendingReasoningSignature = ""
	return s.encoder.EmitToolReasoningDetail(toolCallID, codexSignatureProvider, signature)
}

func (s *codexChatStreamSink) ToolArguments(key string, delta string) error {
	slot, ok := s.toolSlots[key]
	if !ok {
		return invalidProviderResponseError("Codex sent arguments for an unopened function call")
	}
	return s.encoder.EmitToolCallArguments(slot, delta)
}

func (s *codexChatStreamSink) ToolDone(string) error {
	return nil
}

func (s *codexChatStreamSink) Finalize(
	response map[string]any,
	usage Usage,
	hasToolCalls bool,
) error {
	finishReason := "stop"
	if hasToolCalls {
		finishReason = "tool_calls"
	} else {
		switch reason := codexResponseIncompleteReason(response); reason {
		case "max_output_tokens", "max_tokens":
			finishReason = "length"
		case "content_filter":
			finishReason = "content_filter"
		default:
			if reason != "" {
				finishReason = "length"
			}
		}
	}
	return s.encoder.Finalize(finishReason, usage)
}

func (s *codexChatStreamSink) Abort() {
	s.encoder.Abort()
}
