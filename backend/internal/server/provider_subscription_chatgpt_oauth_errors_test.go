package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func chatGPTSubscriptionRejectedGrant(t *testing.T, adapter *ChatGPTSubscriptionAdapter, grant string) error {
	t.Helper()
	if grant == "authorization_code" {
		_, err := adapter.ExchangeAuthorization(context.Background(), ChatGPTSubscriptionAuthorization{
			Code: "private-authorization-code", ClientID: "private-client", Verifier: strings.Repeat("v", 64),
			RedirectURI: "http://127.0.0.1:1455/auth/callback", Nonce: "private-nonce",
		})
		return err
	}
	_, err := adapter.Refresh(context.Background(), ProviderResourceCredentials{ClientID: "private-client", RefreshToken: "private-refresh"})
	return err
}

func TestChatGPTSubscriptionTokenRejectionDistinguishesAuthorizationAndRefresh(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		body      string
		reason    string
		authorize string
		refresh   string
	}{
		{"string_grant", `{"error":"invalid_grant","error_description":"private-authorization-code"}`, "invalid_grant", "subscription_oauth_code_rejected", "provider_resource_reauthorization_required"},
		{"object_grant", `{"error":{"code":"invalid_grant","message":"private-refresh"}}`, "invalid_grant", "subscription_oauth_code_rejected", "provider_resource_reauthorization_required"},
		{"string_client", `{"error":"invalid_client","error_description":"private-client"}`, "invalid_client", "subscription_oauth_client_rejected", "provider_resource_reauthorization_required"},
		{"object_client", `{"error":{"code":"invalid_client","message":"private-client"}}`, "invalid_client", "subscription_oauth_client_rejected", "provider_resource_reauthorization_required"},
		{"top_level_code", `{"code":"invalid_grant","error":{"code":"invalid_client","message":"private-client"}}`, "invalid_grant", "subscription_oauth_code_rejected", "provider_resource_reauthorization_required"},
		{"invalid_request", `{"error":"invalid_request","error_description":"private-nonce"}`, "invalid_request", "subscription_oauth_exchange_failed", "subscription_token_request_failed"},
		{"refresh_reused", `{"error":{"code":"refresh_token_reused","message":"private-refresh"}}`, "refresh_token_reused", "subscription_oauth_exchange_failed", "provider_resource_reauthorization_required"},
		{"unknown_reason", `{"error":"private-authorization-code","error_description":"private-refresh"}`, "unknown", "subscription_oauth_exchange_failed", "subscription_token_request_failed"},
		{"malformed", `{"error":"invalid_grant","private-refresh":`, "unknown", "subscription_oauth_exchange_failed", "subscription_token_request_failed"},
		{"array_error", `{"error":["invalid_grant","private-refresh"]}`, "unknown", "subscription_oauth_exchange_failed", "subscription_token_request_failed"},
	} {
		for _, grant := range []string{"authorization_code", "refresh_token"} {
			t.Run(scenario.name+"/"+grant, func(t *testing.T) {
				adapter, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != grant || r.Form.Get("client_id") != "private-client" {
						t.Error("unexpected token request")
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, scenario.body)
				})
				err := chatGPTSubscriptionRejectedGrant(t, adapter, grant)
				wantCode := scenario.authorize
				if grant == "refresh_token" {
					wantCode = scenario.refresh
				}
				failure := AsHTTPError(err)
				if failure == nil || failure.Code != wantCode || failure.UpstreamStatus != http.StatusBadRequest {
					t.Fatalf("unexpected grant rejection: %+v", failure)
				}
				wantStatus := http.StatusBadGateway
				if wantCode == "subscription_oauth_code_rejected" || wantCode == "subscription_oauth_client_rejected" || wantCode == "provider_resource_reauthorization_required" {
					wantStatus = http.StatusConflict
				}
				if failure.Status != wantStatus {
					t.Errorf("unexpected public status: %d", failure.Status)
				}
				details, _ := failure.Details.(map[string]any)
				if details["stage"] != grant || details["reason"] != scenario.reason {
					t.Errorf("missing safe stage/reason: %v", details)
				}
				encoded, _ := json.Marshal(map[string]any{"message": failure.Message, "details": failure.Details})
				if strings.Contains(string(encoded), "private-") {
					t.Fatal("token rejection exposed upstream details")
				}
				if grant == "refresh_token" && wantCode == "provider_resource_reauthorization_required" && providerErrorDisposition(err) != ProviderErrorAuthBroken {
					t.Error("refresh rejection lost account failure disposition")
				}
				if grant == "authorization_code" && providerErrorDisposition(err) == ProviderErrorAuthBroken {
					t.Error("new authorization was classified as an expired saved account")
				}
			})
		}
	}
}

func TestChatGPTSubscriptionTokenTemporaryFailurePreservesBothGrantRetryPolicies(t *testing.T) {
	for _, grant := range []string{"authorization_code", "refresh_token"} {
		for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
			t.Run(fmt.Sprintf("%s/%d", grant, status), func(t *testing.T) {
				adapter, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Retry-After", "90")
					w.Header().Set("X-Private-Token", "private-header")
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, `{"error":"invalid_grant","error_description":"private-refresh"}`)
				})
				err := chatGPTSubscriptionRejectedGrant(t, adapter, grant)
				failure := AsHTTPError(err)
				if failure == nil || failure.Status != classifyProviderStatus(status).status || failure.UpstreamStatus != status || providerErrorDisposition(err) != ProviderErrorTransientSame {
					t.Fatalf("temporary token rejection lost classification: %+v", failure)
				}
				if providerErrorRetryAfter(err) != 90*time.Second || strings.Contains(err.Error(), "private-") || failure.Headers["X-Private-Token"] != "" {
					t.Fatal("temporary token rejection lost retry metadata or exposed secrets")
				}
			})
		}
	}
}

func TestChatGPTSubscriptionAuthorizationCodeFailureExposesOnlyKnownSubtype(t *testing.T) {
	for _, scenario := range []struct{ description, subtype string }{
		{"The authorization code has expired: private-authorization-code", "code_expired"},
		{"Authorization code has already been redeemed: private-authorization-code", "code_already_used"},
		{"The code_verifier does not match private-verifier", "pkce_verifier_mismatch"},
		{"redirect_uri mismatch: http://localhost/private-callback", "redirect_uri_mismatch"},
		{"Invalid authorization code private-authorization-code", ""},
		{"The private-session has expired", ""},
	} {
		for _, object := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/object=%t", firstNonEmpty(scenario.subtype, "unknown"), object), func(t *testing.T) {
				adapter, _ := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					body := map[string]any{"error": "invalid_grant", "error_description": scenario.description}
					if object {
						body = map[string]any{"error": map[string]any{"code": "invalid_grant", "message": scenario.description}}
					}
					_ = json.NewEncoder(w).Encode(body)
				})
				failure := AsHTTPError(chatGPTSubscriptionRejectedGrant(t, adapter, "authorization_code"))
				if failure == nil || failure.Code != "subscription_oauth_code_rejected" {
					t.Fatalf("unexpected grant failure: %+v", failure)
				}
				details, _ := failure.Details.(map[string]any)
				actual, _ := details["oauth_reason"].(string)
				if actual != scenario.subtype {
					t.Errorf("got subtype %q, want %q", actual, scenario.subtype)
				}
				encoded, _ := json.Marshal(map[string]any{"message": failure.Message, "details": failure.Details})
				if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "http://") {
					t.Fatal("grant subtype exposed raw upstream content")
				}
			})
		}
	}
}
