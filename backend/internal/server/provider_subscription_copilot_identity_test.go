package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCopilotModelEndpointCacheDoesNotSurviveCredentialReplacement(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var replaced atomic.Bool
			var catalogs atomic.Int32
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					account := strings.TrimPrefix(r.Header.Get("Authorization"), "token ")
					_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%d}`, account+"-session", time.Now().Add(time.Hour).Unix())
				case "/models":
					catalogs.Add(1)
					endpoint := "/responses"
					if r.Header.Get("Authorization") == "Bearer new-account-session" {
						endpoint = "/chat/completions"
					}
					_, _ = fmt.Fprintf(w, `{"data":[{"id":"shared-model","supported_endpoints":[%q]}]}`, endpoint)
				case "/chat/completions":
					if r.Header.Get("Authorization") != "Bearer new-account-session" {
						t.Errorf("chat used the previous account")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"new account\"}}]}\n\ndata: [DONE]\n\n")
						return
					}
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"new account"}}]}`)
				default:
					http.Error(w, "endpoint is not supported by this account", http.StatusNotFound)
				}
			})
			adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
				token := "old-account"
				if replaced.Load() {
					token = "new-account"
				}
				return ProviderResourceCredentials{AccessToken: token}, nil
			}
			if _, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, ""); err != nil {
				t.Fatal(err)
			}
			replaced.Store(true)
			request := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}
			var err error
			if stream {
				var output bytes.Buffer
				_, err = adapter.ChatStream(context.Background(), provider, "shared-model", request, &output)
			} else {
				_, _, err = adapter.Chat(context.Background(), provider, "shared-model", request)
			}
			if err != nil {
				t.Fatalf("replacement account reused old protocol route: %v", err)
			}
			if catalogs.Load() != 2 {
				t.Fatalf("replacement account did not refresh model routes: catalogs=%d", catalogs.Load())
			}
		})
	}
}

func TestCopilotModelRoutesKeepDiscoveryCredentialIdentity(t *testing.T) {
	var replaced atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			account := strings.TrimPrefix(r.Header.Get("Authorization"), "token ")
			_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%d}`, account+"-session", time.Now().Add(time.Hour).Unix())
		case "/models":
			endpoint := "/chat/completions"
			if r.Header.Get("Authorization") == "Bearer old-account-session" {
				close(started)
				<-release
				endpoint = "/responses"
			}
			_, _ = fmt.Fprintf(w, `{"data":[{"id":"shared-model","supported_endpoints":[%q]}]}`, endpoint)
		case "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer new-account-session" {
				t.Errorf("chat used the previous account")
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"new account"}}]}`)
		default:
			http.Error(w, "endpoint is not supported by this account", http.StatusNotFound)
		}
	})
	adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		token := "old-account"
		if replaced.Load() {
			token = "new-account"
		}
		return ProviderResourceCredentials{AccessToken: token}, nil
	}
	oldResult := make(chan error, 1)
	go func() {
		_, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
		oldResult <- err
	}()
	<-started
	replaced.Store(true)
	_, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-oldResult; err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.Chat(context.Background(), provider, "shared-model", ChatCompletionRequest{}); err != nil {
		t.Fatalf("late discovery overwrote replacement account routes: %v", err)
	}
}

func TestCopilotInferenceKeepsItsDiscoveredAccount(t *testing.T) {
	var replaced atomic.Bool
	adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			account := strings.TrimPrefix(r.Header.Get("Authorization"), "token ")
			_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%d}`, account+"-session", time.Now().Add(time.Hour).Unix())
		case "/models":
			replaced.Store(true)
			_, _ = fmt.Fprint(w, `{"data":[{"id":"shared-model","supported_endpoints":["/chat/completions"]}]}`)
		case "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer old-account-session" {
				t.Error("inference switched accounts after model discovery")
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"old account"}}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		token := "old-account"
		if replaced.Load() {
			token = "new-account"
		}
		return ProviderResourceCredentials{AccessToken: token}, nil
	}
	if _, _, err := adapter.Chat(context.Background(), provider, "shared-model", ChatCompletionRequest{}); err != nil {
		t.Fatal(err)
	}
}
