package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionQuotaKeepsScopedAndDisplayOnlyWindowsSeparate(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	raw := []byte(`{"plan":"Plus","balance":"25 credits","windows":[{"name":"Included","used":25,"resetsAt":"2026-10-25T13:00:00Z"},{"name":"Core","used":100,"models":["core-model"]},{"name":"Extra","used":100,"aside":true,"display":"No extra credits"},{"name":"Standard","used":100,"notModels":["core-model"]}]}`)
	quota, err := normalizeSubscriptionQuota(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if quota["allowed"] != true || quota["limit_reached"] != false || quota["remaining_percent"] != float64(75) {
		t.Fatalf("scoped quota incorrectly exhausted every model: %+v", quota)
	}
	windows := quota["windows"].([]map[string]any)
	if len(windows) != 1 || windows[0]["reset_after_seconds"] != int64(3600) || len(quota["additional_rate_limits"].([]map[string]any)) != 3 {
		t.Fatalf("window scope or reset was lost: %+v", quota)
	}
	if len(quota["metrics"].([]map[string]string)) != 2 {
		t.Fatal("balance and display-only details were lost")
	}
	limited, err := normalizeSubscriptionQuota([]byte(`{"windows":[{"used":102}]}`), now)
	if err != nil || limited["limit_reached"] != true || limited["remaining_percent"] != float64(0) {
		t.Fatalf("exhausted quota not normalized: %+v %v", limited, err)
	}
	unknown, err := normalizeSubscriptionQuota([]byte(`{"balance":"25 credits","windows":[]}`), now)
	if err != nil || unknown["remaining_percent"] != nil || unknown["allowed"] != nil {
		t.Fatalf("balance-only result invented a quota percentage: %+v %v", unknown, err)
	}
}

func TestFactoryStandardOnlyChecksAllowanceBeforeInference(t *testing.T) {
	for _, raw := range []string{`{"windows":[{"name":"5 hours","used":100}]}`, `{"windows":[]}`, `{"error":"allowance unavailable"}`} {
		t.Run(raw, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(nil)
			defer pool.Close()
			calls := 0
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					calls++
					if command["operation"] != "usage" || command["options"].(map[string]any)["standard_only"] != true {
						t.Error("inference started before the Standard allowance was checked")
					}
					return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(raw)}}
				}), nil
			}
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
				RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
					return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"test-key"}`}, nil
				}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			_, _, err := adapter.Chat(context.Background(), Provider{Options: map[string]string{"resource_id": "account", "standard_only": "true"}}, "model", ChatCompletionRequest{})
			if err == nil || calls != 1 {
				t.Fatalf("Standard-only policy allowed unverified spending: calls=%d error=%v", calls, err)
			}
			if strings.Contains(raw, "100") && (AsHTTPError(err).Status != 429 || providerErrorDisposition(err) != ProviderErrorQuotaExhausted) {
				t.Fatalf("exhausted Standard quota did not trigger account failover: %v", err)
			}
		})
	}
}

func TestSubscriptionManagementRejectsErrorsWithoutExposingUpstreamText(t *testing.T) {
	for _, raw := range []string{`{"error":"upstream included secret-token"}`, `{"signIn":"expired"}`, `{"windows":[{"used":"invalid"}]}`} {
		if _, err := normalizeSubscriptionQuota([]byte(raw), time.Now()); err == nil || strings.Contains(err.Error(), "secret-token") {
			t.Fatalf("unsafe quota error: %v", err)
		}
	}
	for _, outcome := range []string{"claimed", "done", "inactive", "ineligible"} {
		result, err := normalizeSubscriptionCheckin([]byte(`{"outcome":"` + outcome + `","credit":5,"streak":2,"message":"secret-token"}`))
		if err != nil || result["outcome"] != outcome || result["message"] != nil {
			t.Fatalf("check-in outcome was not normalized: %+v %v", result, err)
		}
	}
	if _, err := normalizeSubscriptionCheckin([]byte(`{"outcome":"failed","message":"secret-token"}`)); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe check-in failure: %v", err)
	}
}

func TestSubscriptionManagementUsesAccountLeaseOptionsAndSecretRedaction(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	leased := false
	var received map[string]any
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if !leased {
				t.Error("management request ran outside the account lease")
			}
			received = command
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"plan":"Plan synthetic-secret","windows":[{"name":"Allowance","used":40}]}`)}}
		}), nil
	}
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-secret"}`}, nil
		}, SaveSession: func(context.Context, string, string, string, string) error { return nil },
		RunClusterOperation: func(ctx context.Context, key string, operation func(context.Context) error) error {
			if key != "subscription-account:account" {
				t.Fatal("quota lost its account lease key")
			}
			leased = true
			defer func() { leased = false }()
			return operation(ctx)
		},
	}
	provider := Provider{Type: adapter.Definition.providerType(), Options: map[string]string{"standard_only": "false", "zdr": "true", "unrelated": "secret"}}
	resource := ProviderResource{ID: "account", ResourceType: adapter.Definition.resourceType(), Options: map[string]string{"standard_only": "true"}}
	result, err := adapter.accountManagement(context.Background(), provider, resource, "usage")
	if err != nil {
		t.Fatal(err)
	}
	options := received["options"].(map[string]any)
	if len(options) != 1 || options["standard_only"] != true || received["operation"] != "usage" {
		t.Fatalf("declared options were not forwarded safely: %+v", options)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "synthetic-secret") {
		t.Fatal("quota response exposed account credentials")
	}
	provider.Options["standard_only"] = "invalid"
	resource.Options = nil
	if _, err := adapter.accountManagement(context.Background(), provider, resource, "usage"); AsHTTPError(err).Code != "subscription_option_invalid" {
		t.Fatalf("invalid setting silently changed behavior: %v", err)
	}
}

func TestSubscriptionProbeHonorsResourceAllowanceOptions(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	calls := 0
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			calls++
			options, _ := command["options"].(map[string]any)
			if command["operation"] != "usage" || options["standard_only"] != true {
				t.Error("probe bypassed the resource Standard-only allowance policy")
			}
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"windows":[{"name":"5 hours","used":100}]}`)}}
		}), nil
	}
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"factory", "Factory"}, Pool: pool,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"test-key"}`}, nil
		}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	provider := Provider{Options: map[string]string{"standard_only": "false"}}
	resource := ProviderResource{ID: "account", Options: map[string]string{"standard_only": "true"}}
	_, err := adapter.Probe(context.Background(), provider, resource, ProviderProbeRequest{Model: "fixture-model", Prompt: "Hi"})
	if err == nil || providerErrorDisposition(err) != ProviderErrorQuotaExhausted || calls != 1 {
		t.Fatalf("probe allowed unverified Standard-only spending: calls=%d error=%v", calls, err)
	}
	if provider.Options["standard_only"] != "false" || provider.Options["resource_id"] != "" {
		t.Fatal("probe modified provider defaults")
	}
}

func TestSubscriptionManagementRedactsTextWithoutCorruptingQuotaSchema(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		var process *subscriptionWorkerProcess
		process = fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			secrets := process.invocationSecrets(command["id"].(string))
			secrets.addHeaders(http.Header{"Connect-Protocol-Version": {"1"}, "X-Synthetic-Token": {"upstream-secret"}})
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"plan":"Plan access-secret","balance":"10 credits","until":"2026-11-01T12:00:00Z","windows":[{"name":"Allowance","used":25,"resetsAt":"2026-11-01T10:00:00Z"},{"name":"Limited upstream-secret","used":80,"models":["upstream-secret"],"resetsAt":"2026-11-01T11:00:00Z","display":"10 requests access-secret"}]}`)}}
		})
		return process, nil
	}
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"access-secret","name":"windows"}`}, nil
		}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	result, err := adapter.accountManagement(context.Background(), Provider{}, ProviderResource{ID: "account"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	windows := result["windows"].([]map[string]any)
	reset := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Unix()
	if len(windows) != 1 || windows[0]["key"] != "window-0" || windows[0]["reset_at"] != reset || result["remaining_percent"] != float64(75) {
		t.Fatalf("redaction corrupted the primary quota schema: %+v", result)
	}
	additional := result["additional_rate_limits"].([]map[string]any)
	if len(additional) != 1 {
		t.Fatalf("redaction removed the scoped quota: %+v", result)
	}
	window := additional[0]["rate_limit"].(map[string]any)["primary_window"].(map[string]any)
	if window["key"] != "window-1" || window["reset_at"] != reset+3600 {
		t.Fatalf("redaction corrupted the scoped quota schema: %+v", window)
	}
	metrics := result["metrics"].([]map[string]string)
	if len(metrics) != 3 || !strings.HasPrefix(metrics[0]["value"], "10 requests ") || metrics[1]["value"] != "10 credits" || metrics[2]["value"] != "2026-11-01T12:00:00Z" {
		t.Fatalf("protocol metadata corrupted quota display values: %+v", metrics)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "access-secret") || strings.Contains(string(encoded), "upstream-secret") {
		t.Fatal("quota display text exposed account or upstream credentials")
	}
}
