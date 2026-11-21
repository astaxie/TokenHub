package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// consumeAnthropicResponsesStream converts an Anthropic Messages SSE stream
// into Responses SSE events while relaying, and assembles the final Responses
// object for billing and state storage. Text becomes one assistant message
// item; tool_use blocks become function_call items keyed by the Anthropic tool
// id. Thinking deltas are consumed but not relayed: provider-internal
// reasoning has no Responses representation this gateway mints.
func consumeAnthropicResponsesStream(providerModel string, body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	decoder := newSSEDecoder(body)
	model := providerModel
	responseID := ""
	stopReason := ""
	usage := Usage{}
	// messageItemAdded marks whether the assistant message item has been
	// announced; its deltas stream as output_text.delta.
	messageItemAdded := false
	messageContent := make([]map[string]any, 0, 4)
	type functionCallState struct {
		itemID      string
		callID      string
		name        string
		arguments   strings.Builder
		announced   bool
		doneEmitted bool
	}
	functionCalls := map[int]*functionCallState{}
	functionCallOrder := []int{}
	outputItems := make([]any, 0, 4)

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
	fail := func(err error) (map[string]any, string, Usage, error) {
		return nil, "", usage, err
	}

	for {
		event, err := decoder.Next()
		if err != nil {
			if err == io.EOF {
				return fail(NewHTTPError(http.StatusBadGateway, "anthropic_stream_incomplete",
					"Anthropic stream ended before message_stop"))
			}
			return fail(err)
		}
		if strings.TrimSpace(event.Data) == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil || payload == nil {
			return fail(NewHTTPError(http.StatusBadGateway, "invalid_anthropic_stream_event",
				"Anthropic stream returned an event that is not valid JSON"))
		}
		eventType, _ := payload["type"].(string)
		switch eventType {
		case "error":
			detail, _ := payload["error"].(map[string]any)
			return fail(NewHTTPError(http.StatusBadGateway, "provider_stream_error",
				fmt.Sprintf("Anthropic stream reported an error: %v", detail)))
		case "message_start":
			message, _ := payload["message"].(map[string]any)
			if message == nil {
				return fail(NewHTTPError(http.StatusBadGateway, "invalid_anthropic_stream_event",
					"message_start is missing the message object"))
			}
			anthropicID, _ := message["id"].(string)
			responseID = "resp_" + strings.TrimPrefix(anthropicID, "msg_")
			if upstreamModel, _ := message["model"].(string); upstreamModel != "" {
				model = upstreamModel
			}
			if blockUsage, ok := message["usage"].(map[string]any); ok {
				usage.PromptTokens = int64FromAny(blockUsage["input_tokens"])
			}
			skeleton := map[string]any{
				"id":     responseID,
				"object": "response",
				"status": "in_progress",
				"model":  model,
				"output": []any{},
			}
			if err := emit("response.created", map[string]any{"type": "response.created", "response": skeleton}); err != nil {
				return fail(err)
			}
		case "content_block_start":
			index := indexFromAny(payload["index"])
			block, _ := payload["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				if !messageItemAdded {
					messageItemAdded = true
					if err := emit("response.output_item.added", map[string]any{
						"type":         "response.output_item.added",
						"output_index": len(functionCallOrder) + len(functionCalls),
						"item": map[string]any{
							"type": "message", "id": "msg_0", "role": "assistant", "status": "in_progress", "content": []any{},
						},
					}); err != nil {
						return fail(err)
					}
				}
			case "tool_use":
				blockID, _ := block["id"].(string)
				name, _ := block["name"].(string)
				state := &functionCallState{itemID: fmt.Sprintf("fc_%d", len(functionCallOrder)+1), callID: blockID, name: name}
				functionCalls[index] = state
				functionCallOrder = append(functionCallOrder, index)
				if err := emit("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"output_index": len(functionCallOrder) - 1,
					"item": map[string]any{
						"type": "function_call", "id": state.itemID, "call_id": blockID,
						"name": name, "arguments": "", "status": "in_progress",
					},
				}); err != nil {
					return fail(err)
				}
			case "thinking":
				// Consumed, not relayed.
			}
		case "content_block_delta":
			index := indexFromAny(payload["index"])
			delta, _ := payload["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				if text == "" {
					continue
				}
				if err := emit("response.output_text.delta", map[string]any{
					"type":    "response.output_text.delta",
					"item_id": "msg_0",
					"delta":   text,
				}); err != nil {
					return fail(err)
				}
				messageContent = append(messageContent, map[string]any{"type": "output_text", "text": text})
			case "input_json_delta":
				state, ok := functionCalls[index]
				if !ok {
					continue
				}
				partial, _ := delta["partial_json"].(string)
				state.arguments.WriteString(partial)
				if err := emit("response.function_call_arguments.delta", map[string]any{
					"type":    "response.function_call_arguments.delta",
					"item_id": state.itemID,
					"delta":   partial,
				}); err != nil {
					return fail(err)
				}
			case "thinking_delta", "signature_delta":
				// Consumed, not relayed.
			}
		case "content_block_stop":
			index := indexFromAny(payload["index"])
			if state, ok := functionCalls[index]; ok && !state.doneEmitted {
				state.doneEmitted = true
				outputItems = append(outputItems, map[string]any{
					"type": "function_call", "id": state.itemID, "call_id": state.callID,
					"name": state.name, "arguments": state.arguments.String(), "status": "completed",
				})
				if err := emit("response.output_item.done", map[string]any{
					"type": "response.output_item.done",
					"item": outputItems[len(outputItems)-1],
				}); err != nil {
					return fail(err)
				}
			}
		case "message_delta":
			delta, _ := payload["delta"].(map[string]any)
			if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
				stopReason = reason
			}
			if blockUsage, ok := payload["usage"].(map[string]any); ok {
				usage.CompletionTokens = int64FromAny(blockUsage["output_tokens"])
			}
		case "message_stop":
			if messageItemAdded {
				item := map[string]any{
					"type": "message", "id": "msg_0", "role": "assistant",
					"status": "completed", "content": messageContent,
				}
				if err := emit("response.output_item.done", map[string]any{
					"type": "response.output_item.done",
					"item": item,
				}); err != nil {
					return fail(err)
				}
				outputItems = append(outputItems, item)
			}
			status := "completed"
			incompleteDetails := map[string]any{}
			if stopReason == "max_tokens" {
				status = "incomplete"
				incompleteDetails["reason"] = "max_output_tokens"
			}
			final := map[string]any{
				"id":     responseID,
				"object": "response",
				"status": status,
				"model":  model,
				"output": outputItems,
				"usage":  openAIResponsesUsageObject(usage),
			}
			if status == "incomplete" && len(incompleteDetails) > 0 {
				final["incomplete_details"] = incompleteDetails
			}
			if err := emit("response.completed", map[string]any{
				"type":     "response.completed",
				"response": final,
			}); err != nil {
				return fail(err)
			}
			return final, "", usage, nil
		case "ping":
			continue
		}
	}
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
