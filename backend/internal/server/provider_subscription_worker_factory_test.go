package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFactoryStandardOnlyRejectsMalformedUpstreamBeforeInference(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	for _, testCase := range []struct {
		window   string
		redirect bool
	}{{`{}`, false}, {`{"usedPercent":null}`, false}, {`{"usedPercent":"0"}`, false}, {`{}`, true}} {
		for _, stream := range []bool{false, true} {
			name := testCase.window + "/collected"
			if stream {
				name = testCase.window + "/streamed"
			}
			if testCase.redirect {
				name += "/redirected"
			}
			t.Run(name, func(t *testing.T) {
				var allowance, inference atomic.Int32
				client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
					body := `{}`
					status := http.StatusOK
					headers := http.Header{"Content-Type": {"application/json"}}
					if request.URL.Path == "/api/billing/limits" || testCase.redirect && request.URL.Path == "/api/billing/limits/" {
						allowance.Add(1)
						body = `{"limits":{"standard":{"fiveHour":` + testCase.window + `}},"extraUsageAllowed":true,"extraUsageBalanceCents":10000}`
						if testCase.redirect && request.URL.Path == "/api/billing/limits" {
							status = http.StatusFound
							headers.Set("Location", "/api/billing/limits/")
						}
					} else {
						inference.Add(1)
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}
				pool := newSubscriptionWorkerPool(client)
				defer pool.Close()
				auth, err := json.Marshal(map[string]any{"type": "oauth", "access": "synthetic-factory", "activeOrganizationId": "fixture-org", "expires": time.Now().Add(time.Hour).UnixMilli()})
				if err != nil {
					t.Fatal(err)
				}
				adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
					RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
						return ProviderResourceCredentials{SessionJSON: string(auth)}, nil
					}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
				provider := Provider{Options: map[string]string{"resource_id": "account", "standard_only": "true"}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if stream {
					_, err = adapter.ChatStream(ctx, provider, "glm-5.3", ChatCompletionRequest{}, io.Discard)
				} else {
					_, _, err = adapter.Chat(ctx, provider, "glm-5.3", ChatCompletionRequest{})
				}
				expectedAllowance := int32(1)
				if testCase.redirect {
					expectedAllowance = 2
				}
				if err == nil || AsHTTPError(err).Status != http.StatusBadGateway || allowance.Load() != expectedAllowance || inference.Load() != 0 {
					t.Fatalf("incomplete Standard quota authorized spending: allowance=%d inference=%d error=%v", allowance.Load(), inference.Load(), err)
				}
			})
		}
	}
}
