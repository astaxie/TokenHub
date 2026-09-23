package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAlertDeliveryCredentialsAreRedactedAcrossAdminSurfaces(t *testing.T) {
	store := NewMemoryStore()
	var received strings.Builder
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(&received, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(webhook.Close)

	const pathSecret = "webhook-path-secret"
	const querySecret = "webhook-query-secret"
	channel := store.CreateResource("notification-channels", AdminResource{
		Name:   "Secret webhook",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "webhook",
			"webhook_url": webhook.URL + "/services/" + pathSecret + "?access_token=" + querySecret,
		},
	})
	alert := AlertEvent{
		ID: "alt_delivery_redaction", ScopeType: "provider", ScopeID: "prv_delivery_redaction",
		Severity: "warning", Code: "delivery_redaction", Message: "Delivery redaction", CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "delivery-redaction-admin", SecretKey: "delivery-redaction-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	delivered := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{"channel_id": channel.ID}, "delivery-redaction-admin")
	if delivered.Code != http.StatusOK || !strings.Contains(delivered.Body, `"status":"success"`) {
		t.Fatalf("deliver alert = %d: %s", delivered.Code, delivered.Body)
	}
	if !strings.Contains(received.String(), alert.Code) {
		t.Fatalf("webhook did not receive alert payload: %s", received.String())
	}

	assertAlertDeliverySurfacesHideSecrets(t, app, store, delivered.Body, "delivery-redaction-admin", pathSecret, querySecret)
}

func TestAlertDeliveryRequestErrorsDoNotPersistTelegramBotToken(t *testing.T) {
	store := NewMemoryStore()
	const botToken = "telegram%invalid-secret"
	channel := store.CreateResource("notification-channels", AdminResource{
		Name:   "Telegram",
		Status: StatusActive,
		Fields: map[string]any{
			"type":               "telegram",
			"telegram_bot_token": botToken,
			"telegram_chat_id":   "chat-1",
		},
	})
	alert := AlertEvent{
		ID: "alt_telegram_error_redaction", ScopeType: "provider", ScopeID: "prv_telegram_error_redaction",
		Severity: "warning", Code: "telegram_error_redaction", Message: "Telegram redaction", CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "telegram-redaction-admin", SecretKey: "telegram-redaction-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	delivered := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{"channel_id": channel.ID}, "telegram-redaction-admin")
	if delivered.Code != http.StatusOK || !strings.Contains(delivered.Body, `"status":"failed"`) {
		t.Fatalf("deliver invalid Telegram alert = %d: %s", delivered.Code, delivered.Body)
	}
	assertAlertDeliverySurfacesHideSecrets(t, app, store, delivered.Body, "telegram-redaction-admin", botToken)
}

func TestLegacyAlertDeliveryAndAuditResponsesRedactCredentialURLs(t *testing.T) {
	store := NewMemoryStore()
	const pathSecret = "legacy-delivery-path-secret"
	const querySecret = "legacy-delivery-query-secret"
	legacyURL := "https://hooks.example.test/services/" + pathSecret + "?token=" + querySecret
	delivery := store.RecordAlertDelivery(AlertDelivery{
		ID: "dlv_legacy_secret", AlertID: "alt_legacy_secret", Channel: "webhook", Target: legacyURL,
		Status: "failed", Error: `Post "` + legacyURL + `": dial tcp: connection refused`,
	})
	store.RecordAuditEvent(AuditEvent{
		ID: "audit_legacy_delivery_secret", Action: "deliver", ResourceType: "alert", ResourceID: delivery.AlertID,
		Status: "success", AfterSnapshot: snapshotJSON(delivery),
	})
	server := NewWithConfig(store, Config{AdminToken: "legacy-delivery-admin", SecretKey: "legacy-delivery-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	listed := doJSON(t, app, http.MethodGet, "/api/admin/alert-deliveries", nil, "legacy-delivery-admin")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, delivery.ID) {
		t.Fatalf("list legacy alert deliveries = %d: %s", listed.Code, listed.Body)
	}
	exported := doJSON(t, app, http.MethodGet, "/api/admin/export/alert-deliveries", nil, "legacy-delivery-admin")
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body, delivery.AlertID) {
		t.Fatalf("export legacy alert deliveries = %d: %s", exported.Code, exported.Body)
	}
	audited := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, "legacy-delivery-admin")
	if audited.Code != http.StatusOK || !strings.Contains(audited.Body, "audit_legacy_delivery_secret") {
		t.Fatalf("list legacy delivery audit = %d: %s", audited.Code, audited.Body)
	}
	for name, body := range map[string]string{"list": listed.Body, "export": exported.Body, "audit": audited.Body} {
		assertNoSecretValues(t, body, map[string]string{"path": pathSecret, "query": querySecret})
		if !strings.Contains(body, "hooks.example.test") {
			t.Fatalf("%s removed the safe destination host: %s", name, body)
		}
	}
}

func assertAlertDeliverySurfacesHideSecrets(t *testing.T, app http.Handler, store *GormStore, deliveryBody, adminToken string, secrets ...string) {
	t.Helper()
	secretMap := make(map[string]string, len(secrets))
	for index, secret := range secrets {
		secretMap[string(rune('a'+index))] = secret
	}
	assertNoSecretValues(t, deliveryBody, secretMap)
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) == 0 {
		t.Fatal("alert delivery was not stored")
	}
	assertNoSecretValues(t, snapshotJSON(deliveries[0]), secretMap)

	for name, response := range map[string]responseBody{
		"list":   doJSON(t, app, http.MethodGet, "/api/admin/alert-deliveries", nil, adminToken),
		"export": doJSON(t, app, http.MethodGet, "/api/admin/export/alert-deliveries", nil, adminToken),
		"audit":  doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, adminToken),
	} {
		if response.Code != http.StatusOK {
			t.Fatalf("%s alert delivery surface = %d: %s", name, response.Code, response.Body)
		}
		assertNoSecretValues(t, response.Body, secretMap)
	}
}
