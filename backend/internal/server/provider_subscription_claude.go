package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"
)

const providerClaudeSubscription = "claude_subscription"
const providerResourceClaudeSubscription = "claude_subscription_account"

type subscriptionCallerKey struct{}

// withSubscriptionCaller receives authenticated gateway identities, never body
// fields. A tool continuation must use the same tenant and API credential.
func withSubscriptionCaller(ctx context.Context, tenantID, apiKeyID string) context.Context {
	if tenantID == "" || apiKeyID == "" {
		return ctx
	}
	return context.WithValue(ctx, subscriptionCallerKey{}, tenantID+"\x00"+apiKeyID)
}

// ClaudeSubscriptionAdapter delegates subscription inference to the genuine
// Claude Code executable. Only caller-declared MCP tools are available to it.
type ClaudeSubscriptionAdapter struct {
	RefreshCredentials  func(context.Context, string, bool) (ProviderResourceCredentials, error)
	SaveSession         func(context.Context, string, string, string, string) error
	RunClusterOperation func(context.Context, string, func(context.Context) error) error
	StreamIdleTimeout   time.Duration
	ParkTimeout         time.Duration
	// Test/host injection only; these are never sourced from provider options.
	Executable     string
	ExecutableArgs []string
	mu             sync.Mutex
	runs           map[string]*claudeSubscriptionRun
	calls          map[string]*claudeSubscriptionRun
	signIns        map[string]*claudeSubscriptionSignIn
}

type claudeSubscriptionEvent struct {
	Text  string
	Calls []map[string]any
	Usage Usage
	Done  bool
	Err   error
}

func (a *ClaudeSubscriptionAdapter) Chat(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	text := ""
	var calls []map[string]any
	usage, err := a.complete(ctx, provider, model, request, func(event claudeSubscriptionEvent) error {
		text += event.Text
		calls = append(calls, event.Calls...)
		return nil
	})
	if err != nil {
		return nil, usage, err
	}
	message := map[string]any{"role": "assistant", "content": text}
	finish := "stop"
	if len(calls) > 0 {
		message["tool_calls"] = calls
		finish = "tool_calls"
	}
	return map[string]any{"id": NewID("chatcmpl"), "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens, "total_tokens": usage.TotalTokens}}, usage, nil
}

func (a *ClaudeSubscriptionAdapter) ChatStream(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, w io.Writer) (Usage, error) {
	encoder := newOpenAIChatStreamEncoder(w, model, streamUsageRequested(request))
	finish := "stop"
	usage, err := a.complete(ctx, provider, model, request, func(event claudeSubscriptionEvent) error {
		if err := encoder.EmitText(event.Text); err != nil {
			return err
		}
		for _, call := range event.Calls {
			finish = "tool_calls"
			slot := encoder.NextToolSlot()
			function := call["function"].(map[string]any)
			if err := encoder.EmitToolCallStart(slot, call["id"].(string), function["name"].(string)); err != nil {
				return err
			}
			if err := encoder.EmitToolCallArguments(slot, function["arguments"].(string)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return usage, err
	}
	return usage, encoder.Finalize(finish, usage)
}

func (a *ClaudeSubscriptionAdapter) complete(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, emit func(claudeSubscriptionEvent) error) (Usage, error) {
	credentials, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, false)
	if err != nil {
		return Usage{}, err
	}
	tools, err := claudeSubscriptionTools(request)
	if err != nil {
		return Usage{}, err
	}
	caller, _ := ctx.Value(subscriptionCallerKey{}).(string)
	if len(tools) > 0 && caller == "" {
		return Usage{}, NewHTTPError(400, "subscription_caller_required", "Claude tool requests require an authenticated caller")
	}
	scopeRaw, _ := json.Marshal([]string{provider.Options["resource_id"], caller, credentials.AccessToken, credentials.SessionJSON, model})
	scope := sha256.Sum256(scopeRaw)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.OriginalName
	}
	toolBytes, _ := json.Marshal(struct {
		Tools []claudeSubscriptionTool
		Names []string
	}{tools, names})
	toolHash := sha256.Sum256(toolBytes)
	resultMessages := claudeSubscriptionResults(request.Messages)
	var run *claudeSubscriptionRun
	if len(resultMessages) > 0 {
		run, err = a.resume(hex.EncodeToString(scope[:]), toolHash, request, resultMessages)
	} else {
		run, err = a.start(ctx, provider, model, credentials, hex.EncodeToString(scope[:]), toolHash, tools, request)
	}
	if err != nil {
		return Usage{}, err
	}
	idle := a.StreamIdleTimeout
	if idle <= 0 {
		idle = 2 * time.Minute
	}
	timer := time.NewTimer(idle)
	defer timer.Stop()
	usage := Usage{}
	for {
		select {
		case <-ctx.Done():
			run.close()
			return usage, ctx.Err()
		case <-timer.C:
			run.close()
			return usage, NewHTTPError(504, "claude_subscription_idle", "Claude Code did not respond before the idle timeout")
		case <-run.ctx.Done():
			return usage, NewHTTPError(502, "claude_subscription_closed", "Claude Code session ended")
		case event := <-run.events:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
			usage = claudeSubscriptionAddUsage(usage, event.Usage)
			if event.Err != nil {
				run.close()
				return usage, event.Err
			}
			if len(event.Calls) > 0 {
				if err := run.persist(ctx); err != nil {
					run.close()
					return usage, err
				}
			}
			if err := emit(event); err != nil {
				run.close()
				return usage, err
			}
			if len(event.Calls) > 0 {
				run.mu.Lock()
				run.busy = false
				run.mu.Unlock()
				timeout := a.ParkTimeout
				if timeout <= 0 {
					timeout = 5 * time.Minute
				}
				run.arm(timeout)
				return usage, nil
			}
			if event.Done {
				if err := run.persist(ctx); err != nil {
					run.close()
					return usage, err
				}
				run.close()
				return usage, nil
			}
		}
	}
}

func claudeSubscriptionAddUsage(left, right Usage) Usage {
	left.PromptTokens += right.PromptTokens
	left.CompletionTokens += right.CompletionTokens
	left.TotalTokens = left.PromptTokens + left.CompletionTokens
	left.CachedInputTokens += right.CachedInputTokens
	left.CacheWriteInputTokens += right.CacheWriteInputTokens
	left.CacheWrite5mInputTokens += right.CacheWrite5mInputTokens
	left.CacheWrite1hInputTokens += right.CacheWrite1hInputTokens
	return left
}

func (a *ClaudeSubscriptionAdapter) Close() {
	a.closeSignIns()
	a.mu.Lock()
	runs := make([]*claudeSubscriptionRun, 0, len(a.runs))
	for _, run := range a.runs {
		runs = append(runs, run)
	}
	a.mu.Unlock()
	for _, run := range runs {
		run.close()
	}
	for _, run := range runs {
		<-run.cleanupDone
	}
}

func (a *ClaudeSubscriptionAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Claude subscriptions expose chat completions")
}
func (a *ClaudeSubscriptionAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Claude subscriptions do not expose embeddings")
}
