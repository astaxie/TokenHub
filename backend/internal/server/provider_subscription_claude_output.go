package server

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

func (r *claudeSubscriptionRun) emit(event claudeSubscriptionEvent) bool {
	select {
	case r.events <- event:
		return true
	case <-r.ctx.Done():
		return false
	}
}

// Claude Code emits incremental Anthropic events and complete assistant
// content blocks. Blocks bind MCP ids; message boundaries batch caller tools
// and usage snapshots are reconciled by message identity.
func (r *claudeSubscriptionRun) readOutput(output io.Reader) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	partial, partialReasoning := false, false
	usageReported := Usage{}
	messageUsage := make(map[string]Usage)
	messageID := ""
	stopReason := ""
	messageOpen := false
	anonymous := 0
	var pendingCalls []map[string]any
	accountUsage := func(id string, snapshot Usage) Usage {
		if id == "" {
			anonymous++
			id = "anonymous:" + strconv.Itoa(anonymous)
		}
		delta := claudeSubscriptionUsageDelta(snapshot, messageUsage[id])
		messageUsage[id] = claudeSubscriptionAddUsage(messageUsage[id], delta)
		usageReported = claudeSubscriptionAddUsage(usageReported, delta)
		return delta
	}
	for scanner.Scan() {
		var item map[string]any
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			continue
		}
		switch item["type"] {
		case "stream_event":
			event, _ := item["event"].(map[string]any)
			switch event["type"] {
			case "message_start":
				message, _ := event["message"].(map[string]any)
				messageID, _ = message["id"].(string)
				stopReason = ""
				messageOpen = true
				if !r.emit(claudeSubscriptionEvent{Usage: accountUsage(messageID, anthropicUsage(message))}) {
					return
				}
			case "message_delta":
				delta, _ := event["delta"].(map[string]any)
				if reason, _ := delta["stop_reason"].(string); reason != "" {
					stopReason = reason
				}
				if !r.emit(claudeSubscriptionEvent{Usage: accountUsage(messageID, anthropicUsage(event))}) {
					return
				}
			case "message_stop":
				messageOpen = false
				if len(pendingCalls) > 0 {
					if !r.emit(claudeSubscriptionEvent{Calls: pendingCalls}) {
						return
					}
					pendingCalls = nil
				}
			case "content_block_delta":
				delta, _ := event["delta"].(map[string]any)
				if delta["type"] == "text_delta" {
					text, _ := delta["text"].(string)
					if text != "" {
						partial = true
						if !r.emit(claudeSubscriptionEvent{Text: text}) {
							return
						}
					}
				}
				if delta["type"] == "thinking_delta" {
					thinking, _ := delta["thinking"].(string)
					if thinking != "" {
						partialReasoning = true
						if !r.emit(claudeSubscriptionEvent{Reasoning: thinking}) {
							return
						}
					}
				}
			}
		case "assistant":
			message, _ := item["message"].(map[string]any)
			if reason, _ := message["stop_reason"].(string); reason != "" {
				stopReason = reason
			}
			content, _ := message["content"].([]any)
			id, _ := message["id"].(string)
			if id == "" && messageOpen {
				id = messageID
			}
			usage := accountUsage(id, anthropicUsage(message))
			var text, reasoning strings.Builder
			for _, value := range content {
				block, _ := value.(map[string]any)
				switch block["type"] {
				case "text":
					if !partial {
						part, _ := block["text"].(string)
						text.WriteString(part)
					}
				case "thinking":
					if !partialReasoning {
						part, _ := block["thinking"].(string)
						reasoning.WriteString(part)
					}
				}
			}
			partial, partialReasoning = false, false
			calls, err := r.registerCalls(content)
			if messageOpen {
				pendingCalls = append(pendingCalls, calls...)
				calls = nil
			}
			if !r.emit(claudeSubscriptionEvent{Text: text.String(), Reasoning: reasoning.String(), Calls: calls, Usage: usage, Err: err}) || err != nil {
				return
			}
		case "result":
			total, _ := item["usage"].(map[string]any)
			remaining := claudeSubscriptionUsageDelta(anthropicUsageFromRawMap(total), usageReported)
			if failed, _ := item["is_error"].(bool); failed {
				r.emit(claudeSubscriptionEvent{Usage: remaining, Err: NewHTTPError(502, "claude_subscription_failed", "Claude Code could not complete the subscription request")})
				return
			}
			if reason, _ := item["stop_reason"].(string); reason != "" {
				stopReason = reason
			}
			finish, err := anthropicFinishReason(stopReason, false)
			if err != nil {
				err = invalidProviderResponseError("Claude Code returned an unsupported stop reason")
			}
			r.emit(claudeSubscriptionEvent{Done: err == nil, FinishReason: finish, Usage: remaining, Err: err})
			return
		}
	}
	if r.ctx.Err() == nil {
		r.emit(claudeSubscriptionEvent{Err: NewHTTPError(502, "claude_subscription_stream_ended", "Claude Code ended before completing the response")})
	}
}
