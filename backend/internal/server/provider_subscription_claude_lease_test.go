package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeSubscriptionLeaseSpansParkedProcessAcrossReplicas(t *testing.T) {
	first, provider, ctx := claudeSubscriptionFixture(t)
	second, _, _ := claudeSubscriptionFixture(t)
	semaphore := make(chan struct{}, 1)
	var entered atomic.Int32
	lease := func(ctx context.Context, name string, operation func(context.Context) error) error {
		if name != "subscription-account:fixture-account" {
			t.Errorf("wrong account lease: %s", name)
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		entered.Add(1)
		defer func() { <-semaphore }()
		return operation(ctx)
	}
	first.RunClusterOperation, second.RunClusterOperation = lease, lease
	request := claudeSubscriptionToolRequest()
	body, _, err := first.Chat(ctx, provider, "tools", request)
	if err != nil {
		t.Fatal(err)
	}
	limited, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, _, err := second.Chat(limited, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Another request"}}}); err != context.DeadlineExceeded {
		t.Fatalf("second replica used parked account: %v", err)
	}
	if entered.Load() != 1 {
		t.Fatalf("second replica entered lease before tool completion: %d", entered.Load())
	}
	if _, _, err := first.Chat(ctx, provider, "tools", claudeSubscriptionContinue(t, request, body)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.Chat(ctx, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "After completion"}}}); err != nil {
		t.Fatalf("account lease was not released: %v", err)
	}
	if entered.Load() != 2 {
		t.Fatalf("unexpected lease acquisitions: %d", entered.Load())
	}
}
