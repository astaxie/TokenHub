package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Anthropic Messages SSE stream is decoded into an ordered list of turn
// events first, and the Responses rendering is produced from that list alone.
// Keeping the two stages apart makes each testable on its own — the decoder
// answers "what did the upstream say", the renderer answers "what does a
// Responses client see" — and lets a future surface render the same decoded
// turn without a second wire-to-wire conversion.

type anthropicTurnEventKind int

const (
	// anthropicTurnStarted carries the response identity and prompt-side usage.
	anthropicTurnStarted anthropicTurnEventKind = iota
	// anthropicTurnTextDelta is one assistant text delta.
	anthropicTurnTextDelta
	// anthropicTurnToolOpened opens a tool call: ID is the Anthropic tool id,
	// ToolName the function name.
	anthropicTurnToolOpened
	// anthropicTurnToolArguments carries one partial-arguments JSON delta.
	anthropicTurnToolArguments
	// anthropicTurnToolFinished closes a tool call with its full arguments.
	anthropicTurnToolFinished
	// anthropicTurnFinished ends the turn: StopReason and completion usage.
	anthropicTurnFinished
	// anthropicTurnFailed ends the turn with Err describing the failure.
	anthropicTurnFailed
)

type anthropicTurnEvent struct {
	Kind         anthropicTurnEventKind
	ResponseID   string
	Model        string
	Text         string
	ToolUseID    string
	CallID       string
	ToolName     string
	Arguments    string
	StopReason   string
	InputTokens  int64
	OutputTokens int64
	Err          error
}

// decodeAnthropicTurn reads an Anthropic Messages SSE body and returns its
// ordered turn events. Thinking and signature deltas are consumed but not
// represented: provider-internal reasoning has no counterpart the gateway
// mints on the Responses surface.
func decodeAnthropicTurn(providerModel string, body io.Reader) []anthropicTurnEvent {
	decoder := newSSEDecoder(body)
	events := []anthropicTurnEvent{}
	responseID := ""
	model := providerModel
	stopReason := ""
	inputTokens := int64(0)
	outputTokens := int64(0)
	toolByIndex := map[int]*struct {
		useID     string
		callID    string
		name      string
		arguments strings.Builder
		closed    bool
	}{}

	for {
		event, err := decoder.Next()
		if err != nil {
			if err == io.EOF {
				events = append(events, anthropicTurnEvent{
					Kind: anthropicTurnFailed,
					Err: NewHTTPError(http.StatusBadGateway, "anthropic_stream_incomplete",
						"Anthropic stream ended before message_stop"),
				})
			} else {
				events = append(events, anthropicTurnEvent{Kind: anthropicTurnFailed, Err: err})
			}
			return events
		}
		if strings.TrimSpace(event.Data) == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil || payload == nil {
			events = append(events, anthropicTurnEvent{
				Kind: anthropicTurnFailed,
				Err: NewHTTPError(http.StatusBadGateway, "invalid_anthropic_stream_event",
					"Anthropic stream returned an event that is not valid JSON"),
			})
			return events
		}
		switch eventType, _ := payload["type"].(string); eventType {
		case "error":
			detail, _ := payload["error"].(map[string]any)
			events = append(events, anthropicTurnEvent{
				Kind: anthropicTurnFailed,
				Err: NewHTTPError(http.StatusBadGateway, "provider_stream_error",
					fmt.Sprintf("Anthropic stream reported an error: %v", detail)),
			})
			return events
		case "message_start":
			message, _ := payload["message"].(map[string]any)
			if message == nil {
				events = append(events, anthropicTurnEvent{
					Kind: anthropicTurnFailed,
					Err: NewHTTPError(http.StatusBadGateway, "invalid_anthropic_stream_event",
						"message_start is missing the message object"),
				})
				return events
			}
			anthropicID, _ := message["id"].(string)
			responseID = "resp_" + strings.TrimPrefix(anthropicID, "msg_")
			if upstreamModel, _ := message["model"].(string); upstreamModel != "" {
				model = upstreamModel
			}
			if blockUsage, ok := message["usage"].(map[string]any); ok {
				inputTokens = int64FromAny(blockUsage["input_tokens"])
			}
			events = append(events, anthropicTurnEvent{
				Kind: anthropicTurnStarted, ResponseID: responseID, Model: model, InputTokens: inputTokens,
			})
		case "content_block_start":
			index := indexFromAny(payload["index"])
			block, _ := payload["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			if blockType == "tool_use" {
				blockID, _ := block["id"].(string)
				name, _ := block["name"].(string)
				toolByIndex[index] = &struct {
					useID     string
					callID    string
					name      string
					arguments strings.Builder
					closed    bool
				}{useID: blockID, callID: blockID, name: name}
				events = append(events, anthropicTurnEvent{
					Kind: anthropicTurnToolOpened, ToolUseID: blockID, CallID: blockID, ToolName: name,
				})
			}
		case "content_block_delta":
			index := indexFromAny(payload["index"])
			delta, _ := payload["delta"].(map[string]any)
			switch deltaType, _ := delta["type"].(string); deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				if text != "" {
					events = append(events, anthropicTurnEvent{Kind: anthropicTurnTextDelta, Text: text})
				}
			case "input_json_delta":
				if _, ok := toolByIndex[index]; ok {
					partial, _ := delta["partial_json"].(string)
					toolByIndex[index].arguments.WriteString(partial)
					events = append(events, anthropicTurnEvent{
						Kind: anthropicTurnToolArguments, Arguments: partial,
					})
				}
			}
		case "content_block_stop":
			index := indexFromAny(payload["index"])
			if tool, ok := toolByIndex[index]; ok && !tool.closed {
				tool.closed = true
				events = append(events, anthropicTurnEvent{
					Kind:      anthropicTurnToolFinished,
					ToolUseID: tool.useID, CallID: tool.callID, ToolName: tool.name,
					Arguments: tool.arguments.String(),
				})
			}
		case "message_delta":
			delta, _ := payload["delta"].(map[string]any)
			if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
				stopReason = reason
			}
			if blockUsage, ok := payload["usage"].(map[string]any); ok {
				outputTokens = int64FromAny(blockUsage["output_tokens"])
			}
		case "message_stop":
			events = append(events, anthropicTurnEvent{
				Kind: anthropicTurnFinished, ResponseID: responseID, Model: model,
				StopReason: stopReason, InputTokens: inputTokens, OutputTokens: outputTokens,
			})
			return events
		case "ping":
			continue
		}
	}
}

// relayAnthropicTurnAsResponses renders decoded turn events as Responses SSE
// on destination and assembles the final Responses object for billing and
// state storage. Assistant text becomes one message item; a tool call becomes
// a function_call item keyed by the Anthropic tool id.
func relayAnthropicTurnAsResponses(events []anthropicTurnEvent, destination io.Writer) (map[string]any, string, Usage, error) {
	usage := Usage{}
	emit := func(event string, payload map[string]any) error {
		if destination == nil {
			return nil
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(destination, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		if flusher, ok := destination.(streamFlusher); ok {
			flusher.Flush()
		}
		return nil
	}

	messageOpened := false
	messageContent := make([]map[string]any, 0, 4)
	outputItems := make([]any, 0, 4)
	toolStates := map[string]*struct {
		itemID      string
		callID      string
		toolName    string
		arguments   strings.Builder
		doneEmitted bool
	}{}
	toolOrder := []string{}

	for _, event := range events {
		switch event.Kind {
		case anthropicTurnStarted:
			if err := emit("response.created", map[string]any{
				"type": "response.created",
				"response": map[string]any{
					"id": event.ResponseID, "object": "response", "status": "in_progress",
					"model": event.Model, "output": []any{},
				},
			}); err != nil {
				return nil, "", usage, err
			}
			usage.PromptTokens = event.InputTokens
		case anthropicTurnTextDelta:
			if !messageOpened {
				messageOpened = true
				if err := emit("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"output_index": len(toolOrder),
					"item": map[string]any{
						"type": "message", "id": "msg_0", "role": "assistant",
						"status": "in_progress", "content": []any{},
					},
				}); err != nil {
					return nil, "", usage, err
				}
			}
			if err := emit("response.output_text.delta", map[string]any{
				"type": "response.output_text.delta", "item_id": "msg_0", "delta": event.Text,
			}); err != nil {
				return nil, "", usage, err
			}
			messageContent = append(messageContent, map[string]any{"type": "output_text", "text": event.Text})
		case anthropicTurnToolOpened:
			state := &struct {
				itemID      string
				callID      string
				toolName    string
				arguments   strings.Builder
				doneEmitted bool
			}{itemID: fmt.Sprintf("fc_%d", len(toolOrder)+1), callID: event.CallID, toolName: event.ToolName}
			toolStates[event.CallID] = state
			toolOrder = append(toolOrder, event.CallID)
			if err := emit("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": len(toolOrder) - 1,
				"item": map[string]any{
					"type": "function_call", "id": state.itemID, "call_id": event.CallID,
					"name": event.ToolName, "arguments": "", "status": "in_progress",
				},
			}); err != nil {
				return nil, "", usage, err
			}
		case anthropicTurnToolArguments:
			state, ok := toolStates[event.CallID]
			if !ok || event.Arguments == "" {
				continue
			}
			state.arguments.WriteString(event.Arguments)
			if err := emit("response.function_call_arguments.delta", map[string]any{
				"type": "response.function_call_arguments.delta", "item_id": state.itemID, "delta": event.Arguments,
			}); err != nil {
				return nil, "", usage, err
			}
		case anthropicTurnToolFinished:
			state, ok := toolStates[event.CallID]
			if !ok || state.doneEmitted {
				continue
			}
			state.doneEmitted = true
			item := map[string]any{
				"type": "function_call", "id": state.itemID, "call_id": event.CallID,
				"name": event.ToolName, "arguments": event.Arguments, "status": "completed",
			}
			outputItems = append(outputItems, item)
			if err := emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item}); err != nil {
				return nil, "", usage, err
			}
		case anthropicTurnFinished:
			usage.CompletionTokens = event.OutputTokens
			if messageOpened {
				item := map[string]any{
					"type": "message", "id": "msg_0", "role": "assistant",
					"status": "completed", "content": messageContent,
				}
				outputItems = append(outputItems, item)
				if err := emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": item}); err != nil {
					return nil, "", usage, err
				}
			}
			status := "completed"
			incompleteDetails := map[string]any{}
			if event.StopReason == "max_tokens" {
				status = "incomplete"
				incompleteDetails["reason"] = "max_output_tokens"
			}
			final := map[string]any{
				"id": event.ResponseID, "object": "response", "status": status,
				"model": event.Model, "output": outputItems,
				"usage": openAIResponsesUsageObject(usage),
			}
			if status == "incomplete" && len(incompleteDetails) > 0 {
				final["incomplete_details"] = incompleteDetails
			}
			if err := emit("response.completed", map[string]any{"type": "response.completed", "response": final}); err != nil {
				return nil, "", usage, err
			}
			return final, "", usage, nil
		case anthropicTurnFailed:
			return nil, "", usage, event.Err
		}
	}
	return nil, "", usage, NewHTTPError(http.StatusBadGateway, "anthropic_stream_incomplete",
		"Anthropic stream ended before message_stop")
}

// consumeAnthropicResponsesStream decodes the Anthropic SSE body into turn
// events and relays them as Responses SSE, returning the final Responses
// object for billing and state storage.
func consumeAnthropicResponsesStream(providerModel string, body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	return relayAnthropicTurnAsResponses(decodeAnthropicTurn(providerModel, body), destination)
}

func indexFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	default:
		return 0
	}
}
