package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestChatGPTSubscriptionAutomaticCallbackRequiresConfiguredLoopbackAndOptIn(t *testing.T) {
	for _, scenario := range []struct {
		name, publicBase, redirect string
		automatic                  bool
	}{
		{"ipv4", "http://127.0.0.1:8080", "http://127.0.0.1:8080/auth/callback", true},
		{"localhost", "http://localhost:8080/", "http://localhost:8080/auth/callback", true},
		{"ipv6", "http://[::1]:8080", "http://[::1]:8080/auth/callback", true},
		{"opt_out", "http://127.0.0.1:8080", "", false},
		{"remote", "https://tokenhub.example", "", true},
		{"remote_http", "http://tokenhub.example:8080", "", true},
		{"https_loopback", "https://127.0.0.1:8080", "", true},
		{"missing_configuration", "", "", true},
		{"credentials", "http://user@127.0.0.1:8080", "", true},
		{"query", "http://127.0.0.1:8080?target=elsewhere", "", true},
		{"fragment", "http://127.0.0.1:8080#fragment", "", true},
		{"path_prefix", "http://127.0.0.1:8080/gateway", "", true},
		{"port_zero", "http://127.0.0.1:0", "", true},
		{"port_large", "http://127.0.0.1:65536", "", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, _, data := chatGPTCallbackFixture(t, scenario.publicBase, scenario.automatic)
			authorize, err := url.Parse(data["auth_url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if scenario.redirect != "" {
				if data["flow"] != "auto" || data["redirect_uri"] != scenario.redirect || authorize.Query().Get("redirect_uri") != scenario.redirect {
					t.Fatal("automatic callback did not bind its configured loopback URL")
				}
			} else if data["flow"] != "authorization_code" || data["callback_mode"] != nil || authorize.Query().Get("redirect_uri") != "http://127.0.0.1:1455/auth/callback" {
				t.Fatal("manual callback fallback changed")
			}
		})
	}
}

func TestChatGPTSubscriptionAutomaticCallbackRejectsMalformedAndForeignSessions(t *testing.T) {
	server, store, data := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
	for _, query := range []url.Values{
		{"code": {"private-code"}},
		{"client_id": {"dynamic-client"}},
		{"code": {"private-code"}, "client_id": {"dynamic_agent_client"}},
		{"code": {"private-code", "second-code"}, "client_id": {"dynamic-client"}},
		{"code": {strings.Repeat("c", 8193)}, "client_id": {"dynamic-client"}},
		{"code": {"private-code"}, "client_id": {strings.Repeat("c", 1025)}},
		{"code": {"private-code"}, "client_id": {"dynamic-client"}, "error": {"access_denied"}},
		{"state": {"wrong-state"}, "code": {"private-code"}, "client_id": {"dynamic-client"}},
	} {
		if response := chatGPTCallbackRequest(server, data, query); response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "private-code") {
			t.Fatalf("malformed callback was accepted or reflected: %d", response.Code)
		}
	}
	for _, suffix := range []string{"%invalid", strings.Repeat("x", 32<<10+1)} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/callback?"+suffix, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid query was accepted: %d", response.Code)
		}
	}
	session, _, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
	if err != nil {
		t.Fatal(err)
	}
	session.ClientID = "tokenhub:subscription:" + providerGeminiCLISubscription
	if err := store.SaveProviderAccountOAuthSession(session); err != nil {
		t.Fatal(err)
	}
	if response := chatGPTCallbackRequest(server, data, url.Values{"code": {"private-code"}, "client_id": {"dynamic-client"}}); response.Code != http.StatusBadRequest {
		t.Fatal("callback accepted another provider's session")
	}
	started, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "administrator", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	manual := started.Data.(map[string]any)
	if response := chatGPTCallbackRequest(server, manual, url.Values{"code": {"private-code"}, "client_id": {"dynamic-client"}}); response.Code != http.StatusBadRequest {
		t.Fatal("manual session was accepted as an automatic callback")
	}
}

func TestChatGPTSubscriptionAutomaticCallbackDenialIsSafeAndSingleUse(t *testing.T) {
	server, store, data := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
	response := chatGPTCallbackRequest(server, data, url.Values{"error": {"access_denied"}, "error_description": {"private-token private-account@example.invalid"}})
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-") {
		t.Fatal("authorization denial was not safely acknowledged")
	}
	payload := map[string]any{"session_id": data["session_id"], "state": data["state"]}
	_, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload)
	if failure := AsHTTPError(err); failure.Code != "subscription_oauth_authorization_denied" || failure.Status != http.StatusConflict || strings.Contains(failure.Message, "private-") {
		t.Fatalf("incorrect denial diagnostic: %v", err)
	}
	if _, exists, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string)); err != nil || exists {
		t.Fatal("denied authorization remained usable")
	}
}

func TestChatGPTSubscriptionAutomaticCallbackDuplicatesCannotReplaceCapturedCode(t *testing.T) {
	server, store, data := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
	const attempts = 8
	var accepted atomic.Int32
	var finished sync.WaitGroup
	finished.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer finished.Done()
			response := chatGPTCallbackRequest(server, data, url.Values{"code": {fmt.Sprintf("private-code-%d", i)}, "client_id": {"dynamic-client"}})
			switch response.Code {
			case http.StatusOK:
				accepted.Add(1)
			case http.StatusConflict:
			default:
				t.Errorf("unexpected concurrent callback status: %d", response.Code)
			}
		}()
	}
	finished.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d concurrent callbacks", accepted.Load())
	}
	session, _, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var secret subscriptionOAuthSecret
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.Callback == nil || !strings.HasPrefix(secret.Callback.Code, "private-code-") {
		t.Fatal("successful callback was not captured")
	}
	if response := chatGPTCallbackRequest(server, data, url.Values{"error": {"access_denied"}}); response.Code != http.StatusConflict {
		t.Fatal("later denial replaced the captured code")
	}
}

func TestChatGPTSubscriptionAutomaticCallbackCannotRestoreCancelledOrExpiredSession(t *testing.T) {
	for _, scenario := range []string{"cancelled", "expired_secret", "expired_record", "cancelled_after_lookup"} {
		t.Run(scenario, func(t *testing.T) {
			server, store, data := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
			payload := map[string]any{"session_id": data["session_id"], "state": data["state"]}
			if scenario == "cancelled" {
				if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.cancel", "administrator", payload); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "expired_secret" || scenario == "expired_record" {
				session, _, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
				if err != nil {
					t.Fatal(err)
				}
				var secret subscriptionOAuthSecret
				if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
					t.Fatal(err)
				}
				secret.ExpiresAt = time.Now().Add(-time.Minute)
				encoded, _ := json.Marshal(secret)
				session.CodeVerifier = string(encoded)
				if scenario == "expired_record" {
					session.CreatedAt = time.Now().Add(-time.Hour)
				}
				if err := store.SaveProviderAccountOAuthSession(session); err != nil {
					t.Fatal(err)
				}
			}
			lookedUp, release := make(chan struct{}), make(chan struct{})
			if scenario == "cancelled_after_lookup" {
				var blockOnce sync.Once
				if err := store.db.Callback().Query().After("gorm:query").Register("test:pause_callback_lookup", func(tx *gorm.DB) {
					_, sessionQuery := tx.Statement.Dest.(*providerAccountOAuthSessionRecord)
					if sessionQuery && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "id" {
						blockOnce.Do(func() { close(lookedUp); <-release })
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = store.db.Callback().Query().Remove("test:pause_callback_lookup") }()
			}
			done := make(chan int, 1)
			go func() {
				response := chatGPTCallbackRequest(server, data, url.Values{"code": {"private-code"}, "client_id": {"dynamic-client"}})
				done <- response.Code
			}()
			if scenario == "cancelled_after_lookup" {
				select {
				case <-lookedUp:
				case <-time.After(3 * time.Second):
					close(release)
					t.Fatal("callback lookup did not reach the race barrier")
				}
				_, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.cancel", "administrator", payload)
				close(release)
				if err != nil {
					t.Fatal(err)
				}
			}
			if status := <-done; status != http.StatusBadRequest {
				t.Fatalf("stale callback was accepted: %d", status)
			}
			if strings.HasPrefix(scenario, "cancelled") {
				if _, exists, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string)); err != nil || exists {
					t.Fatal("callback resurrected a cancelled session")
				}
			}
		})
	}
}

func TestChatGPTSubscriptionAutomaticCallbackIgnoresUntrustedHostHeaders(t *testing.T) {
	server, _, data := chatGPTCallbackFixture(t, "https://remote.example", true)
	request := httptest.NewRequest(http.MethodGet, "/auth/callback?state="+data["state"].(string)+"&code=private-code&client_id=dynamic-client", nil)
	request.Host = "localhost:8080"
	request.Header.Set("Forwarded", "host=127.0.0.1:8080;proto=http")
	request.Header.Set("X-Forwarded-Host", "localhost:8080")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatal("untrusted request host enabled loopback callback")
	}
}
