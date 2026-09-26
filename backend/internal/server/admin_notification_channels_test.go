package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotificationChannelTestUsesSavedWebhookWithoutCreatingAlert(t *testing.T) {
	store := NewMemoryStore()
	var received map[string]any
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/saved-secret" || r.URL.Query().Get("token") != "query-secret" {
			t.Errorf("unexpected webhook request: %s %s", r.Method, r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode notification: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(webhook.Close)
	channel := store.CreateResource("notification-channels", AdminResource{
		Name: "Saved webhook", Status: StatusDisabled,
		Fields: map[string]any{"type": "webhook", "webhook_url": webhook.URL + "/saved-secret?token=query-secret"},
	})
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()
	response := doJSON(t, app, http.MethodPost, notificationTestPath(channel.ID), nil, "")
	var delivery AlertDelivery
	if err := json.Unmarshal([]byte(response.Body), &delivery); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || delivery.Status != "success" || delivery.StatusCode != 202 || delivery.ChannelID != channel.ID {
		t.Fatalf("unexpected test result: %d %s", response.Code, response.Body)
	}
	if received["event_code"] != "notification_channel_test" || received["channel"] != channel.Name || received["severity"] != "info" {
		t.Fatalf("unexpected notification payload: %+v", received)
	}
	if !strings.Contains(delivery.AlertID, "notification_test_") || len(store.ListAlerts()) != 0 {
		t.Fatalf("test must not create an operational alert: %+v", store.ListAlerts())
	}
	stored, err := server.findResource("notification-channels", channel.ID)
	if err != nil || stored.Status != StatusDisabled {
		t.Fatalf("test changed channel status: %+v, %v", stored, err)
	}
	audits := store.ListAuditEvents()
	if len(audits) != 1 || audits[0].Action != "test" || audits[0].ResourceType != "notification_channel" || audits[0].ResourceID != channel.ID {
		t.Fatalf("unexpected audit trail: %+v", audits)
	}
	assertAlertDeliverySurfacesHideSecrets(t, app, store, response.Body, "dev_admin_token", "saved-secret", "query-secret")
}

func TestNotificationChannelTestEmailUsesSavedRecipients(t *testing.T) {
	store := NewMemoryStore()
	messages := configureTestSMTPChannel(t, store)
	channel := store.ListResources("notification-channels")[0]
	channel.Fields["email_to"] = "ops@example.test,admin@example.test"
	if _, err := store.UpdateResource("notification-channels", channel.ID, channel); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	response := doJSON(t, server.Handler(), http.MethodPost, notificationTestPath(channel.ID), nil, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"status":"success"`) {
		t.Fatalf("test email failed: %d %s", response.Code, response.Body)
	}
	select {
	case message := <-messages:
		for _, marker := range []string{"ops@example.test", "admin@example.test", "notification_channel_test", "This is a TokenHub test notification"} {
			if !strings.Contains(message, marker) {
				t.Errorf("email missing %q: %s", marker, message)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("test email was not received")
	}
	if len(store.ListAlerts()) != 0 || len(store.ListAlertDeliveries()) != 1 {
		t.Fatal("expected one delivery and no operational alerts")
	}
}

func TestNotificationChannelTestFailuresAreRecordedAndRedacted(t *testing.T) {
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errcode":310000,"errmsg":"invalid secret signing-secret at https://example.test/private-token"}`)
	}))
	t.Cleanup(webhook.Close)
	for _, tt := range []struct {
		name        string
		fields      map[string]any
		errorMarker string
	}{
		{"bot_rejection", map[string]any{"type": "dingtalk", "webhook_url": webhook.URL, "secret": "signing-secret"}, "310000"},
		{"missing_recipients", map[string]any{"type": "email"}, "email_to is required"},
		{"missing_host", map[string]any{"type": "email", "email_to": "ops@example.test"}, "smtp_host is required"},
		{"missing_webhook", map[string]any{"type": "webhook"}, "webhook_url is required"},
		{"unsupported_type", map[string]any{"type": "unknown"}, "unsupported notification channel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			channel := store.CreateResource("notification-channels", AdminResource{Name: tt.name, Status: StatusActive, Fields: tt.fields})
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
			app := server.Handler()
			response := doJSON(t, app, http.MethodPost, notificationTestPath(channel.ID), nil, "")
			if response.Code != http.StatusOK || !strings.Contains(response.Body, `"status":"failed"`) || !strings.Contains(response.Body, tt.errorMarker) {
				t.Fatalf("unexpected failed delivery: %d %s", response.Code, response.Body)
			}
			assertAlertDeliverySurfacesHideSecrets(t, app, store, response.Body, "dev_admin_token", "signing-secret", "private-token")
		})
	}
}

func TestNotificationChannelTestAuthorizationAndMethods(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "notification-test-password")
	channel := store.CreateResource("notification-channels", AdminResource{Name: "Missing target", Status: StatusActive, Fields: map[string]any{"type": "webhook"}})
	for _, role := range []string{"admin", "system_admin", "security_admin", "team_leader", "user"} {
		token := createAdminOperationMethodRoutingSession(t, store, "notification-test-"+role, role)
		for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead} {
			t.Run(role+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, notificationTestPath(channel.ID), token)
				if role == "team_leader" || role == "user" {
					assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
				} else if method != http.MethodPost {
					assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
					assertAllowHeader(t, response, http.MethodPost)
				} else if response.Code != http.StatusOK {
					t.Fatalf("authorized test failed: %d %s", response.Code, response.Body)
				}
			})
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		response := methodRoutingRequest(app, method, notificationTestPath(channel.ID), "")
		assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
	}
	response := methodRoutingRequest(app, http.MethodPost, notificationTestPath("missing"), "dev_admin_token")
	assertJSONError(t, response, http.StatusNotFound, "resource_not_found")
	if got := len(store.ListAlertDeliveries()); got != 3 {
		t.Fatalf("unauthorized or invalid requests created deliveries: got %d, want 3", got)
	}
}

func TestSMTPConversationHonorsContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		cancel()
		_, _ = io.Copy(io.Discard, conn)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- sendEmail(ctx, map[string]any{"smtp_host": host, "smtp_port": port, "smtp_from": "sender@example.test"}, []string{"ops@example.test"}, []byte("test"), nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled SMTP conversation unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP greeting did not honor cancellation")
	}
}

func notificationTestPath(channelID string) string {
	return "/api/admin/resources/notification-channels/" + channelID + "/test"
}
