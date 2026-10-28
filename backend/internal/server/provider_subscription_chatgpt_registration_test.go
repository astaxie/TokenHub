package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func chatGPTRegistrationStart(t *testing.T, server *Server, payload map[string]any) map[string]any {
	t.Helper()
	result, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "administrator", payload)
	if err != nil {
		t.Fatal(err)
	}
	return result.Data.(map[string]any)
}

func chatGPTRegistrationSecret(t *testing.T, store *GormStore, data map[string]any) subscriptionOAuthSecret {
	t.Helper()
	session, ok, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
	if err != nil || !ok {
		t.Fatal("authorization session missing")
	}
	var secret subscriptionOAuthSecret
	if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestChatGPTSubscriptionRegistrationRetryRetainsClientAndHost(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprintf("automatic_%t", automatic), func(t *testing.T) {
			server, store, initial := chatGPTCallbackFixture(t, "http://localhost:8080", automatic)
			first := chatGPTRegistrationSecret(t, store, initial)
			var reject atomic.Bool
			reject.Store(true)
			var exchanges atomic.Int32
			var active subscriptionOAuthSecret
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					_, _ = fmt.Fprint(w, `{"models":[{"slug":"gpt-live","visibility":"list"}]}`)
					return
				}
				exchanges.Add(1)
				if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "issued-client" || r.Form.Get("code_verifier") != active.Verifier {
					t.Error("exchange lost its registered client or new verifier")
				}
				if reject.Load() {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"private-code"}`)
					return
				}
				claims, _ := json.Marshal(map[string]any{"iss": "https://auth.openai.com", "aud": "issued-client", "nonce": active.Nonce, "exp": time.Now().Add(time.Hour).Unix(), "sub": "verified-subject"})
				idToken := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-access", "refresh_token": "private-refresh", "id_token": idToken, "scope": chatGPTSubscriptionDirectScope, "expires_in": 3600})
			}))
			defer upstream.Close()
			adapter, _ := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](server.adapterRegistry, ProviderChatGPTSubscription)
			adapter.TokenURL, adapter.CredentialRefreshClient = upstream.URL, upstream.Client()
			adapter.APIBaseURL, adapter.Client = upstream.URL, upstream.Client()
			exchange := func(data map[string]any, clientID string) (subscriptionOAuthExchangeResult, error) {
				active = chatGPTRegistrationSecret(t, store, data)
				payload := map[string]any{"session_id": data["session_id"], "state": data["state"]}
				if automatic {
					query := url.Values{"code": {"private-code"}}
					if clientID != "" {
						query.Set("client_id", clientID)
					}
					if response := chatGPTCallbackRequest(server, data, query); response.Code != http.StatusOK {
						t.Fatalf("callback was rejected: %d", response.Code)
					}
				} else {
					payload["code"], payload["client_id"] = "private-code", clientID
				}
				result, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload)
				if err != nil {
					return subscriptionOAuthExchangeResult{}, err
				}
				return result.Data.(subscriptionOAuthExchangeResult), nil
			}
			if _, err := exchange(initial, "issued-client"); AsHTTPError(err).Code != "subscription_oauth_registration_retry" {
				t.Fatalf("initial rejection did not retain registration: %v", err)
			}
			if _, exists, _ := store.GetProviderAccountOAuthSessionByState(initial["state"].(string)); exists {
				t.Fatal("rejected authorization code remained reusable")
			}
			retry := chatGPTRegistrationStart(t, server, map[string]any{"automatic_callback": automatic, "registration_id": initial["registration_id"]})
			second := chatGPTRegistrationSecret(t, store, retry)
			authorize, _ := url.Parse(retry["auth_url"].(string))
			if authorize.Query().Get("client_id") != "issued-client" || authorize.Query().Has("agent_name_hint") || second.HostID != first.HostID || second.Verifier == first.Verifier || second.Nonce == first.Nonce || retry["state"] == initial["state"] {
				t.Fatal("returning authorization did not reuse registration with fresh attempt bindings")
			}
			reject.Store(false)
			result, err := exchange(retry, "")
			if err != nil || result.ClientID != "issued-client" || result.UserID != "verified-subject" {
				t.Fatalf("returning callback without client ID failed: %v", err)
			}
			var persisted subscriptionOAuthRegistrationRecord
			if err := store.db.First(&persisted, "id = ?", initial["registration_id"]).Error; err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(persisted.SecretEncrypted, "enc:v1:") || strings.Contains(persisted.SecretEncrypted, "issued-client") || strings.Contains(store.decryptSecret(persisted.SecretEncrypted), "private-") {
				t.Fatal("registration was unprotected or retained a code or token")
			}
			reject.Store(true)
			third := chatGPTRegistrationStart(t, server, map[string]any{"automatic_callback": automatic, "registration_id": initial["registration_id"]})
			if _, err := exchange(third, "issued-client"); AsHTTPError(err).Code != "subscription_oauth_code_rejected" || exchanges.Load() != 3 {
				t.Fatalf("returning invalid_grant was reported as first registration: %v", err)
			}
		})
	}
}

func TestChatGPTSubscriptionRegistrationPersistsAcrossStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registration.db")
	config := Config{SecretKey: "registration-store-secret"}
	store, err := OpenStoreForMaintenance(path, config)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, "issued-client", "verified-subject"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStoreForMaintenance(path, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retained, err := reopened.chatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID)
	if err != nil || retained.HostID != registration.HostID || retained.ClientID != "issued-client" || retained.Subject != "verified-subject" {
		t.Fatal("store restart lost the registered client or host")
	}
	other, err := reopened.chatGPTSubscriptionRegistration(context.Background(), "other-administrator", "")
	if err != nil || other.ID == registration.ID || other.HostID != registration.HostID || other.ClientID != "" {
		t.Fatal("another administrator did not receive a separate account on the same host")
	}
	if _, err := reopened.chatGPTSubscriptionRegistration(context.Background(), "other-administrator", registration.ID); AsHTTPError(err).Code != "subscription_oauth_registration_invalid" {
		t.Fatal("another administrator resumed a private registration")
	}
}

func TestChatGPTSubscriptionRegistrationConcurrentBindingCannotReplaceClient(t *testing.T) {
	store := NewMemoryStore()
	registration, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func(clientID string) {
			defer wait.Done()
			if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, clientID, ""); err == nil {
				successes.Add(1)
			} else if AsHTTPError(err).Code != "subscription_oauth_client_mismatch" {
				t.Errorf("unexpected concurrent binding error: %v", err)
			}
		}(fmt.Sprintf("issued-client-%d", i))
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent binding accepted %d client IDs", successes.Load())
	}
	stored, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, stored.ClientID, "first-subject"); err != nil {
		t.Fatal(err)
	}
	if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, stored.ClientID, "other-subject"); AsHTTPError(err).Code != "subscription_oauth_identity_mismatch" {
		t.Fatal("registration identity was replaced")
	}
}
