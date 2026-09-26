package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotificationChannelTestHTTPResponseOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name          string
		channelType   string
		body          string
		contentLength string
		wantStatus    string
		wantError     string
	}{
		{name: "wecom_business_error", channelType: "wecom", body: `{"errcode":93000,"errmsg":"invalid webhook secret-token"}`, wantStatus: "failed", wantError: "wecom response error: errcode=93000"},
		{name: "truncated_response", channelType: "dingtalk", body: `{"errcode":`, contentLength: "100", wantStatus: "failed", wantError: "unexpected EOF"},
		{name: "oversized_error_response", channelType: "dingtalk", body: `{"errcode":310000,"errmsg":"` + strings.Repeat("x", 4096) + `"}`, wantStatus: "failed", wantError: "exceeds 4096 bytes"},
		{name: "malformed_bot_json", channelType: "feishu", body: `{"code":`, wantStatus: "failed", wantError: "invalid JSON"},
		{name: "wecom_success", channelType: "wecom", body: `{"errcode":0,"errmsg":"ok"}`, wantStatus: "success"},
		{name: "webhook_at_size_limit", channelType: "webhook", body: strings.Repeat("x", 4096), wantStatus: "success"},
		{name: "large_webhook_success", channelType: "webhook", body: strings.Repeat("x", 8192), wantStatus: "success"},
		{name: "large_telegram_success", channelType: "telegram", body: `{"ok":true,"result":{"text":"` + strings.Repeat("x", 4096) + `"}}`, wantStatus: "success"},
		{name: "empty_webhook_success", channelType: "webhook", wantStatus: "success"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tt.contentLength != "" {
					w.Header().Set("Content-Length", tt.contentLength)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(webhook.Close)
			store := NewMemoryStore()
			channel := store.CreateResource("notification-channels", AdminResource{
				Name: "HTTP response review", Status: StatusActive,
				Fields: map[string]any{"type": tt.channelType, "webhook_url": webhook.URL, "secret": "secret-token", "telegram_chat_id": "test-chat"},
			})
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
			response := doJSON(t, server.Handler(), http.MethodPost, notificationTestPath(channel.ID), nil, "")
			var delivery AlertDelivery
			if err := json.Unmarshal([]byte(response.Body), &delivery); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || delivery.Status != tt.wantStatus || !strings.Contains(delivery.Error, tt.wantError) || (tt.wantStatus == "success" && delivery.Error != "") {
				t.Fatalf("expected status %q and error containing %q, got %d %s", tt.wantStatus, tt.wantError, response.Code, response.Body)
			}
			assertAlertDeliverySurfacesHideSecrets(t, server.Handler(), store, response.Body, "dev_admin_token", "secret-token")
		})
	}
}

func TestNotificationChannelTestDoesNotSucceedAfterResponseTimeout(t *testing.T) {
	headersWritten := make(chan struct{})
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersWritten)
		<-r.Context().Done()
	}))
	t.Cleanup(webhook.Close)
	store := NewMemoryStore()
	channel := store.CreateResource("notification-channels", AdminResource{
		Name: "Stalled response", Status: StatusActive,
		Fields: map[string]any{"type": "dingtalk", "webhook_url": webhook.URL},
	})
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, notificationTestPath(channel.ID), nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer dev_admin_token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	select {
	case <-headersWritten:
	default:
		t.Fatal("request timed out before receiving response headers")
	}
	var delivery AlertDelivery
	if err := json.Unmarshal(recorder.Body.Bytes(), &delivery); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || delivery.Status != "failed" || !strings.Contains(delivery.Error, "context deadline exceeded") {
		t.Fatalf("response timeout must fail delivery: %d %s", recorder.Code, recorder.Body)
	}
}
