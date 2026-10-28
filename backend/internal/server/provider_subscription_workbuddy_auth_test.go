package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkBuddyAuthorizationPollsAndHandsOffAccount(t *testing.T) {
	for _, provider := range []string{"workbuddy", "workbuddy-ai"} {
		for _, rejected := range []bool{false, true} {
			name := provider + "/success"
			if rejected {
				name = provider + "/account-rejected"
			}
			t.Run(name, func(t *testing.T) {
				var tokens, accounts atomic.Int32
				client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
					body := `{"code":"0","data":{}}`
					switch req.URL.Path {
					case "/v2/plugin/auth/state":
						body = `{"code":"0","data":{"state":"synthetic-state","authUrl":"https://copilot.tencent.com/login"}}`
					case "/v2/plugin/auth/token":
						body = `{"code":"0","data":{"accessToken":"synthetic-access","refreshToken":"synthetic-refresh","expiresIn":3600,"domain":"enterprise.accounts.example"}}`
						if tokens.Add(1) == 1 {
							body = `{"code":"11217","msg":"Pending"}`
						}
					case "/v2/plugin/login/account":
						headers := http.Header{}
						for name, values := range req.Header {
							headers[http.CanonicalHeaderKey(name)] = values
						}
						if headers.Get("X-Domain") != "enterprise.accounts.example" {
							t.Error("Account domain was not retained as WorkBuddy header metadata")
						}
						body = `{"code":"0","data":{"uid":"synthetic-user","type":"personal"}}`
						if rejected {
							body = `{"code":12199,"msg":"private-access-token-and-upstream-details"}`
						} else if accounts.Add(1) == 1 {
							body = `{"code":"12151","msg":"Pending"}`
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})}
				pool := newSubscriptionWorkerPool(client)
				defer pool.Close()
				adapter := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{provider, "WorkBuddy"}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				started, err := adapter.startAuthorization(ctx, "synthetic-admin", subscriptionWorkerAuthInput{MethodID: "0"})
				if err != nil {
					t.Fatal(err)
				}
				if started["flow"] != "auto" || started["callback_mode"] != "none" || started["session_json"] != nil {
					t.Fatal("Browser authorization did not declare automatic credential handoff")
				}
				input := subscriptionWorkerAuthInput{SessionID: started["session_id"].(string), State: started["state"].(string)}
				var result map[string]any
				for {
					result, err = adapter.continueAuthorization(ctx, "synthetic-admin", input, false)
					if err != nil || result["status"] != "pending" {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("Browser authorization never completed")
					case <-time.After(20 * time.Millisecond):
					}
				}
				if rejected {
					if err == nil || AsHTTPError(err).Code != "workbuddy_authorization_failed" ||
						!strings.Contains(err.Error(), "loading the signed-in account (HTTP 200, provider code 12199)") || strings.Contains(err.Error(), "private-") {
						t.Fatalf("Authorization lost safe stage diagnostics or leaked upstream details: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					var auth map[string]any
					if json.Unmarshal([]byte(result["session_json"].(string)), &auth) != nil || auth["access"] != "synthetic-access" || auth["uid"] != "synthetic-user" || auth["domain"] != "enterprise.accounts.example" {
						t.Fatal("Completed browser authorization did not return the account for saving")
					}
				}
				pool.mu.Lock()
				remaining := len(pool.sessions)
				pool.mu.Unlock()
				if remaining != 0 {
					t.Fatal("Finished authorization retained its session")
				}
			})
		}
	}
}

func TestSubscriptionAuthorizationFailureDoesNotExposeVendorData(t *testing.T) {
	for _, raw := range []string{
		`{"error":{"code":"workbuddy_authorization_failed","stage":"private-stage","message":"private-message","upstream_status":900,"upstream_code":9007199254740992}}`,
		`{"error":{"code":"private-code","message":"private-message","stage":"token"}}`,
		`{"error":{"code":"workbuddy_authorization_failed","stage":"token","upstream_code":"private-code"}}`,
		`{"error":null}`,
	} {
		err := subscriptionAuthorizationFailure(json.RawMessage(raw))
		if AsHTTPError(err).Status != 400 || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "900") {
			t.Fatalf("Unsafe authorization diagnostics: %v", err)
		}
	}
}
