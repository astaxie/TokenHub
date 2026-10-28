package server

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

const subscriptionChatResponseLimit = 32 << 20
const subscriptionChatIndexLimit = 1024

type subscriptionChatChoice struct {
	text, reasoningContent, reasoning, refusal strings.Builder
	hasText                                    bool
	finish                                     string
	tools                                      map[int]*subscriptionChatTool
}

type subscriptionChatTool struct {
	kind                string
	id, name, arguments strings.Builder
}

// Some subscription endpoints require streaming even for buffered callers.
// Retain every choice and trailing usage frame before returning one completion.
func collectSubscriptionChat(reader io.Reader, model string) (any, Usage, error) {
	return collectSubscriptionChatWithLimit(reader, model, subscriptionChatResponseLimit)
}

func collectSubscriptionChatWithLimit(reader io.Reader, model string, limit int) (any, Usage, error) {
	events := newSSEDecoder(reader)
	choices := make(map[int]*subscriptionChatChoice)
	result := map[string]any{"id": NewID("chatcmpl"), "object": "chat.completion", "created": time.Now().Unix(), "model": model}
	var usage Usage
	total := 0
	for {
		event, err := events.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var failure *subscriptionJSONFailure
			if errors.As(err, &failure) {
				if result["usage"] == nil {
					usage = failure.usage
				}
				err = &subscriptionJSONFailure{err: err, usage: usage}
			}
			return nil, usage, err
		}
		total += len(event.Data)
		if total > limit {
			return nil, usage, invalidProviderResponseError("Subscription completion exceeds the response limit")
		}
		if strings.TrimSpace(event.Data) == "[DONE]" {
			break
		}
		payload, present, err := decodeSSEDataNumbers(event)
		if err != nil || present && payload == nil {
			return nil, usage, invalidProviderResponseError("Subscription provider returned an invalid chat stream event")
		}
		if reported, ok := payload["usage"].(map[string]any); ok && len(reported) > 0 {
			usage = usageFromMap(payload)
			result["usage"] = reported
		}
		if _, err := subscriptionJSONError([]byte(event.Data), Provider{}); err != nil {
			return nil, usage, &subscriptionJSONFailure{err: err, usage: usage}
		}
		if providerStreamEventIsError(event) {
			return nil, usage, invalidProviderResponseError("Subscription provider returned an error stream event")
		}
		if !present {
			continue
		}
		for _, key := range []string{"id", "model", "system_fingerprint", "service_tier"} {
			if value, ok := payload[key].(string); ok && value != "" {
				result[key] = value
			}
		}
		if value, ok := payload["created"].(json.Number); ok {
			if created, err := value.Int64(); err == nil && created >= 0 {
				result["created"] = created
			}
		}
		rawChoices, ok := payload["choices"].([]any)
		if !ok && payload["choices"] != nil {
			return nil, usage, invalidProviderResponseError("Subscription stream choices must be an array")
		}
		for _, raw := range rawChoices {
			entry, ok := raw.(map[string]any)
			if !ok {
				return nil, usage, invalidProviderResponseError("Subscription stream choice must be an object")
			}
			index, err := subscriptionChatIndex(entry["index"])
			if err != nil {
				return nil, usage, err
			}
			choice := choices[index]
			if choice == nil {
				choice = &subscriptionChatChoice{tools: make(map[int]*subscriptionChatTool)}
				choices[index] = choice
			}
			if choice.finish != "" {
				return nil, usage, invalidProviderResponseError("Subscription stream continued a finished choice")
			}
			if err := choice.append(entry); err != nil {
				return nil, usage, err
			}
		}
	}
	if len(choices) == 0 {
		return nil, usage, invalidProviderResponseError("Subscription stream ended without a completion")
	}
	output := make([]any, 0, len(choices))
	for _, index := range subscriptionChatSortedIndexes(choices) {
		choice := choices[index]
		if choice.finish == "" {
			return nil, usage, invalidProviderResponseError("Subscription stream ended before reporting a finish reason")
		}
		message := map[string]any{"role": "assistant", "content": nil}
		if choice.hasText {
			message["content"] = choice.text.String()
		}
		for key, text := range map[string]*strings.Builder{"reasoning_content": &choice.reasoningContent, "reasoning": &choice.reasoning, "refusal": &choice.refusal} {
			if text.Len() > 0 {
				message[key] = text.String()
			}
		}
		if len(choice.tools) > 0 {
			tools := make([]any, 0, len(choice.tools))
			for _, toolIndex := range subscriptionChatSortedIndexes(choice.tools) {
				tool := choice.tools[toolIndex]
				tools = append(tools, map[string]any{"id": tool.id.String(), "type": firstNonEmpty(tool.kind, "function"), "function": map[string]any{"name": tool.name.String(), "arguments": tool.arguments.String()}})
			}
			message["tool_calls"] = tools
		}
		output = append(output, map[string]any{"index": index, "message": message, "finish_reason": choice.finish})
	}
	result["choices"] = output
	if result["usage"] == nil {
		result["usage"] = openAIChatUsageObject(usage)
	}
	return result, usage, nil
}

func subscriptionChatIndex(raw any) (int, error) {
	number, ok := raw.(json.Number)
	if !ok {
		return 0, invalidProviderResponseError("Subscription stream index must be an integer")
	}
	index, err := strconv.Atoi(string(number))
	if err != nil || index < 0 || index >= subscriptionChatIndexLimit {
		return 0, invalidProviderResponseError("Subscription stream index is outside the supported range")
	}
	return index, nil
}

func subscriptionChatSortedIndexes[T any](items map[int]T) []int {
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func (c *subscriptionChatChoice) append(entry map[string]any) error {
	delta, ok := entry["delta"].(map[string]any)
	if !ok && entry["delta"] != nil {
		return invalidProviderResponseError("Subscription stream delta must be an object")
	}
	for key, target := range map[string]*strings.Builder{"content": &c.text, "reasoning_content": &c.reasoningContent, "reasoning": &c.reasoning, "refusal": &c.refusal} {
		if raw := delta[key]; raw != nil {
			value, ok := raw.(string)
			if !ok {
				return invalidProviderResponseError("Subscription stream text delta must be a string")
			}
			target.WriteString(value)
			if key == "content" {
				c.hasText = true
			}
		}
	}
	rawTools, ok := delta["tool_calls"].([]any)
	if !ok && delta["tool_calls"] != nil {
		return invalidProviderResponseError("Subscription stream tool calls must be an array")
	}
	for _, raw := range rawTools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return invalidProviderResponseError("Subscription stream tool call must be an object")
		}
		index, err := subscriptionChatIndex(tool["index"])
		if err != nil {
			return err
		}
		current := c.tools[index]
		if current == nil {
			current = &subscriptionChatTool{}
			c.tools[index] = current
		}
		if id, ok := tool["id"].(string); ok && id != "" {
			current.id.WriteString(id)
		}
		if kind, ok := tool["type"].(string); ok && kind != "" {
			current.kind = kind
		}
		function, ok := tool["function"].(map[string]any)
		if !ok && tool["function"] != nil {
			return invalidProviderResponseError("Subscription stream tool function must be an object")
		}
		for key, target := range map[string]*strings.Builder{"name": &current.name, "arguments": &current.arguments} {
			if raw := function[key]; raw != nil {
				value, ok := raw.(string)
				if !ok {
					return invalidProviderResponseError("Subscription stream tool delta must be a string")
				}
				target.WriteString(value)
			}
		}
	}
	if finish, ok := entry["finish_reason"].(string); ok && finish != "" {
		c.finish = finish
	}
	return nil
}
