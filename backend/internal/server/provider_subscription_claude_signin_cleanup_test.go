package server

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClaudeSignInCompletionWaitsForConsumedSlotRemoval(t *testing.T) {
	adapter := claudeSignInFixture(t, "code")
	started, err := adapter.startSignIn(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	id := started["session_id"].(string)
	adapter.mu.Lock()
	locked := true
	defer func() {
		if locked {
			adapter.mu.Unlock()
		}
	}()
	login := adapter.signIns[id]
	// Hold the removal lock while cancellation reaps the process. Completion
	// must stay pending until the consumed slot can actually be removed.
	adapter.finishSignIn(login)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(login.root); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled sign-in did not remove its temporary directory")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-login.done:
		t.Fatal("sign-in reported completion before its consumed slot was removed")
	case <-time.After(100 * time.Millisecond):
	}
	adapter.mu.Unlock()
	locked = false
	select {
	case <-login.done:
	case <-time.After(5 * time.Second):
		t.Fatal("sign-in cleanup did not complete after removal was unblocked")
	}
	adapter.mu.Lock()
	remaining := len(adapter.signIns)
	adapter.mu.Unlock()
	if remaining != 0 {
		t.Fatal("completed sign-in retained a consumed slot")
	}
}
