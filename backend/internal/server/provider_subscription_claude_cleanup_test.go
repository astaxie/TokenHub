package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeSubscriptionCancellationSavesRotatedCredentialsBeforeCleanup(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotation", true: "concurrent-edit"}[superseded], func(t *testing.T) {
			adapter, provider, ctx := claudeSubscriptionFixture(t)
			original := `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
			adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
				return ProviderResourceCredentials{SessionJSON: original}, nil
			}
			saved := make(chan struct{}, 1)
			var saves atomic.Int32
			adapter.SaveSession = func(ctx context.Context, _, _, previous, next string) error {
				saves.Add(1)
				if ctx.Err() != nil || previous != original || !strings.Contains(next, "rotated-refresh") {
					t.Error("cancelled request did not preserve credential rotation")
				}
				saved <- struct{}{}
				if superseded {
					return NewHTTPError(409, "provider_credentials_changed", "Credentials changed")
				}
				return nil
			}
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, _, err := adapter.Chat(canceled, provider, "refresh-idle", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Wait"}}})
				finished <- err
			}()
			var root string
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				adapter.mu.Lock()
				run := adapter.runs["fixture-account"]
				adapter.mu.Unlock()
				if run != nil {
					run.mu.Lock()
					root = run.root
					config := run.configDir
					run.mu.Unlock()
					if config != "" {
						raw, _ := os.ReadFile(filepath.Join(config, ".credentials.json"))
						if strings.Contains(string(raw), "rotated-refresh") {
							break
						}
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if root == "" {
				t.Fatal("CLI did not start")
			}
			cancel()
			select {
			case err := <-finished:
				if err != context.Canceled {
					t.Fatalf("cancel returned %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancel did not stop CLI")
			}
			select {
			case <-saved:
			case <-time.After(2 * time.Second):
				t.Fatal("rotation was discarded during cancellation")
			}
			adapter.Close()
			if saves.Load() != 1 {
				t.Fatalf("rotation saved %d times", saves.Load())
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("completed credential cleanup retained the private directory")
			}
		})
	}
}
