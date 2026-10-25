package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func copilotSubscriptionFixture(t *testing.T, handler http.HandlerFunc) (*CopilotSubscriptionAdapter, Provider) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	a := &CopilotSubscriptionAdapter{Client: upstream.Client(), StreamClient: upstream.Client(), CredentialRefreshClient: upstream.Client(), APIBaseURL: upstream.URL, TokenURL: upstream.URL + "/token", CLIUserURL: upstream.URL + "/user",
		RefreshCredentials: func(_ context.Context, id string, _ bool) (ProviderResourceCredentials, error) {
			if id != "account" {
				t.Errorf("unexpected resource %q", id)
			}
			return ProviderResourceCredentials{AccessToken: "github-secret"}, nil
		}}
	return a, Provider{Type: ProviderGitHubCopilotSubscription, Options: map[string]string{"resource_id": "account"}}
}

func TestCopilotSubscriptionSessionAndStreaming(t *testing.T) {
	var exchanges atomic.Int32
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			exchanges.Add(1)
			if r.Header.Get("Authorization") != "token github-secret" {
				t.Errorf("incorrect GitHub authorization")
			}
			_, _ = fmt.Fprintf(w, `{"token":"copilot-secret","expires_at":%d,"endpoints":{"api":"https://api.githubcopilot.com"}}`, time.Now().Add(time.Hour).Unix())
			return
		}
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"copilot-model"}]}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer copilot-secret" || r.Header.Get("X-Initiator") != "user" || r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
			t.Errorf("incorrect Copilot headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "copilot-model" {
			t.Errorf("wrong model: %v", body["model"])
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	})
	request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
	_, usage, err := a.Chat(context.Background(), provider, "copilot-model", request)
	if err != nil || usage.TotalTokens != 6 {
		t.Fatalf("chat: usage=%+v err=%v", usage, err)
	}
	var output bytes.Buffer
	usage, err = a.ChatStream(context.Background(), provider, "copilot-model", request, &output)
	if err != nil || usage.TotalTokens != 6 || !strings.Contains(output.String(), "hello") {
		t.Fatalf("stream: usage=%+v err=%v output=%s", usage, err, output.String())
	}
	if exchanges.Load() != 1 {
		t.Fatalf("session was not cached: exchanges=%d", exchanges.Load())
	}
}

func TestCopilotSubscriptionRetriesExpiredSessionOnce(t *testing.T) {
	var exchanges, calls atomic.Int32
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			n := exchanges.Add(1)
			_, _ = fmt.Fprintf(w, `{"token":"session-%d","expires_at":%d}`, n, time.Now().Add(time.Hour).Unix())
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer session-1" {
			w.WriteHeader(401)
			_, _ = fmt.Fprint(w, `{"error":"github-secret session-1"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"ok","usage":{"total_tokens":2}}`)
	})
	if _, _, err := a.Responses(context.Background(), provider, "test-model", ResponsesRequest{Input: "hello"}); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 2 || calls.Load() != 2 {
		t.Fatalf("exchanges=%d calls=%d", exchanges.Load(), calls.Load())
	}
}

func TestCopilotSubscriptionConcurrentSessionsAndCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, _ := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
	})
	creds := ProviderResourceCredentials{AccessToken: "token"}
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		if _, err := a.session(context.Background(), creds, false); err != nil {
			t.Error(err)
		}
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.session(ctx, creds, false); err != context.Canceled {
		t.Fatalf("wait was not cancellable: %v", err)
	}
	close(release)
	group.Wait()
}

func TestCopilotSubscriptionRejectsCredentialRedirectAndHost(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, _, err := a.Chat(context.Background(), provider, "model", ChatCompletionRequest{}); err == nil {
		t.Fatal("redirect was accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("credential followed redirect")
	}
	provider.BaseURL = "https://api.githubcopilot.com.attacker.invalid"
	if _, _, err := a.Chat(context.Background(), provider, "model", ChatCompletionRequest{}); err == nil {
		t.Fatal("untrusted provider endpoint was accepted")
	}
	a2, p2 := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"token":"secret","expires_at":%d,"endpoints":{"api":"https://attacker.invalid"}}`, time.Now().Add(time.Hour).Unix())
	})
	if _, _, err := a2.Chat(context.Background(), p2, "model", ChatCompletionRequest{}); err == nil {
		t.Fatal("untrusted session endpoint was accepted")
	}
}

func TestCopilotSubscriptionModelsAndSecretSafeErrors(t *testing.T) {
	a, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"chat-model","capabilities":{"type":"chat","limits":{"max_context_window_tokens":32000}}},{"id":"responses-model","supported_endpoints":["/responses"],"capabilities":{"type":"chat"}},{"id":"embedding","capabilities":{"type":"embeddings"}}]}`)
		default:
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, `{"error":"github-secret session"}`)
		}
	})
	provider.Options = nil
	catalog, _, err := a.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
	if err != nil || len(catalog.Models) != 3 || catalog.Models[0].ContextWindow != 32000 || catalog.Models[1].Metadata["supported_endpoints"] != "/responses" || catalog.Models[2].ID != "auto" {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	provider.Options = map[string]string{"resource_id": "account"}
	_, _, err = a.Chat(context.Background(), provider, "model", ChatCompletionRequest{})
	if err == nil || strings.Contains(err.Error(), "github-secret") || strings.Contains(err.Error(), "session") {
		t.Fatalf("error exposed upstream content: %v", err)
	}
}

func TestCopilotSubscriptionDeviceAuthorization(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_id") != copilotSubscriptionClientID {
			t.Error("missing device client")
		}
		if r.URL.Path == "/device" {
			_, _ = fmt.Fprint(w, `{"device_code":"secret-device","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":0}`)
			return
		}
		if r.Form.Get("device_code") != "secret-device" {
			t.Error("wrong device code")
		}
		switch polls.Add(1) {
		case 1:
			_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
		case 2:
			_, _ = fmt.Fprint(w, `{"error":"slow_down"}`)
		default:
			_, _ = fmt.Fprint(w, `{"access_token":"github-oauth","scope":"read:user"}`)
		}
	}))
	defer server.Close()
	a := &CopilotSubscriptionAdapter{CredentialRefreshClient: server.Client(), DeviceURL: server.URL + "/device", DeviceTokenURL: server.URL + "/poll"}
	auth, err := a.StartDeviceAuthorization(context.Background())
	if err != nil || auth.Interval != 5 {
		t.Fatalf("auth=%+v err=%v", auth, err)
	}
	for _, want := range []string{"pending", "slow_down", "complete"} {
		creds, state, err := a.PollDeviceAuthorization(context.Background(), auth.DeviceCode)
		if err != nil || state != want {
			t.Fatalf("state=%s err=%v", state, err)
		}
		if state == "complete" && (creds.AccessToken != "github-oauth" || creds.TokenType != "github") {
			t.Fatal("device token was not preserved")
		}
	}
}
