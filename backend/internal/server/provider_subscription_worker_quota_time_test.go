package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionQuotaTimestampFormats(t *testing.T) {
	want := time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)
	for _, raw := range []string{`1793019600`, `"2026-10-26T13:00:00Z"`, `"2026-10-26T14:00:00+01:00"`} {
		got, ok := subscriptionQuotaTimestamp(json.RawMessage(raw))
		if !ok || !got.Equal(want) {
			t.Fatalf("timestamp was not normalized: %s => %v (%t)", raw, got, ok)
		}
	}
	for _, raw := range []string{"", `null`, `0`, `-1`, `253402300800`, `1e30`, `true`, `{}`, `"invalid"`} {
		if _, ok := subscriptionQuotaTimestamp(json.RawMessage(raw)); ok {
			t.Fatalf("invalid reset timestamp was accepted: %s", raw)
		}
	}
}

func TestDevinSubscriptionQuotaRetainsUnixResetTimes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "server.codeium.com" {
			t.Errorf("unexpected Devin request host: %s", request.URL.Host)
		}
		body := ""
		switch request.URL.Path {
		case "/exa.seat_management_pb.SeatManagementService/GetUserStatus":
			body = `{"userStatus":{"planStatus":{"planInfo":{"billingStrategy":"BILLING_STRATEGY_QUOTA"},"dailyQuotaRemainingPercent":75,"dailyQuotaResetAtUnix":1793019600,"weeklyQuotaResetAtUnix":"1793106000","planEnd":"2026-11-01T00:00:00Z","acuLimit":100,"acuConsumed":10}}}`
		case "/exa.api_server_pb.ApiServerService/GetCliModelConfigs":
			// An empty model catalog leaves these account-wide windows unscoped.
		default:
			t.Errorf("unexpected Devin request path: %s", request.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	pool := newSubscriptionWorkerPool(client)
	defer pool.Close()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"devin", "Devin"}, Pool: pool,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-devin-key"}`}, nil
		}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	quota, err := adapter.accountManagement(ctx, Provider{}, ProviderResource{ID: "synthetic-account"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	windows := quota["windows"].([]map[string]any)
	if len(windows) != 2 || windows[0]["used_percent"] != float64(25) || windows[1]["used_percent"] != float64(100) {
		t.Fatalf("Devin quota windows were lost: %+v", quota)
	}
	if windows[0]["reset_at"] != int64(1793019600) || windows[1]["reset_at"] != int64(1793106000) {
		t.Fatalf("Devin Unix reset times were lost: %+v", windows)
	}
	if quota["limit_reached"] != true || len(quota["additional_rate_limits"].([]map[string]any)) != 1 {
		t.Fatalf("Devin account and display-only limits were conflated: %+v", quota)
	}
	metrics := quota["metrics"].([]map[string]string)
	if len(metrics) != 2 || metrics[1]["value"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("Devin plan expiry was lost: %+v", metrics)
	}
}
