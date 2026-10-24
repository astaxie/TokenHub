package server

import (
	"bufio"
	"encoding/json"
	"io"
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
// messages. Complete messages bind MCP ids and usage; deltas provide text.
func (r *claudeSubscriptionRun) readOutput(output io.Reader) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	partial := false
	usageReported := Usage{}
	for scanner.Scan() {
		var item map[string]any
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			continue
		}
		switch item["type"] {
		case "stream_event":
			event, _ := item["event"].(map[string]any)
			if event["type"] == "content_block_delta" {
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
			}
		case "assistant":
			message, _ := item["message"].(map[string]any)
			content, _ := message["content"].([]any)
			usage := anthropicUsage(message)
			usageReported = claudeSubscriptionAddUsage(usageReported, usage)
			var text strings.Builder
			if !partial {
				for _, value := range content {
					block, _ := value.(map[string]any)
					if block["type"] == "text" {
						part, _ := block["text"].(string)
						text.WriteString(part)
					}
				}
			}
			partial = false
			calls, err := r.registerCalls(content)
			if !r.emit(claudeSubscriptionEvent{Text: text.String(), Calls: calls, Usage: usage, Err: err}) || err != nil {
				return
			}
		case "result":
			if failed, _ := item["is_error"].(bool); failed {
				r.emit(claudeSubscriptionEvent{Err: NewHTTPError(502, "claude_subscription_failed", "Claude Code could not complete the subscription request")})
				return
			}
			total, _ := item["usage"].(map[string]any)
			remaining := anthropicUsageFromRawMap(total)
			remaining.PromptTokens = max(0, remaining.PromptTokens-usageReported.PromptTokens)
			remaining.CompletionTokens = max(0, remaining.CompletionTokens-usageReported.CompletionTokens)
			remaining.CachedInputTokens = max(0, remaining.CachedInputTokens-usageReported.CachedInputTokens)
			remaining.CacheWriteInputTokens = max(0, remaining.CacheWriteInputTokens-usageReported.CacheWriteInputTokens)
			remaining.TotalTokens = remaining.PromptTokens + remaining.CompletionTokens
			r.emit(claudeSubscriptionEvent{Done: true, Usage: remaining})
			return
		}
	}
	if r.ctx.Err() == nil {
		r.emit(claudeSubscriptionEvent{Err: NewHTTPError(502, "claude_subscription_stream_ended", "Claude Code ended before completing the response")})
	}
}
