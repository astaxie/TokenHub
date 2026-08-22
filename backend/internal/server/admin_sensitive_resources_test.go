package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSensitiveAdminResourceResponsesAndAuditSnapshotsAreRedacted(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "sensitive-resource-admin", SecretKey: "sensitive-resource-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	identitySecrets := map[string]string{
		"client_secret": "identity-client-secret-value",
	}
	identityCreate := doJSON(t, app, http.MethodPost, "/api/admin/resources/identity-providers", map[string]any{
		"name":   "Corporate identity",
		"status": StatusActive,
		"fields": map[string]any{
			"provider_type": "oauth2",
			"client_id":     "identity-client-id",
			"client_secret": identitySecrets["client_secret"],
		},
	}, "sensitive-resource-admin")
	if identityCreate.Code != http.StatusCreated {
		t.Fatalf("create identity provider = %d: %s", identityCreate.Code, identityCreate.Body)
	}
	assertSensitiveResourceResponse(t, identityCreate.Body, identitySecrets)
	identityID := responseResourceID(t, identityCreate.Body)

	notificationSecrets := map[string]string{
		"webhook_url":        "https://hooks.example.test/services/webhook-secret-token",
		"url":                "https://hooks.example.test/services/legacy-alias-token",
		"secret":             "webhook-signing-secret",
		"smtp_password":      "smtp-password-value",
		"telegram_bot_token": "telegram-bot-token-value",
		"access_token":       "whatsapp-access-token-value",
	}
	notificationFields := map[string]any{"type": "webhook"}
	for key, value := range notificationSecrets {
		notificationFields[key] = value
	}
	notificationCreate := doJSON(t, app, http.MethodPost, "/api/admin/resources/notification-channels", map[string]any{
		"name": "Security notifications", "status": StatusActive, "fields": notificationFields,
	}, "sensitive-resource-admin")
	if notificationCreate.Code != http.StatusCreated {
		t.Fatalf("create notification channel = %d: %s", notificationCreate.Code, notificationCreate.Body)
	}
	assertSensitiveResourceResponse(t, notificationCreate.Body, notificationSecrets)
	notificationID := responseResourceID(t, notificationCreate.Body)

	for kind, secrets := range map[string]map[string]string{
		"identity-providers":    identitySecrets,
		"notification-channels": notificationSecrets,
	} {
		listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/"+kind, nil, "sensitive-resource-admin")
		if listed.Code != http.StatusOK {
			t.Fatalf("list %s = %d: %s", kind, listed.Code, listed.Body)
		}
		assertSensitiveResourceResponse(t, listed.Body, secrets)

		exported := doJSON(t, app, http.MethodGet, "/api/admin/export/"+kind, nil, "sensitive-resource-admin")
		if exported.Code != http.StatusOK {
			t.Fatalf("export %s = %d: %s", kind, exported.Code, exported.Body)
		}
		assertNoSecretValues(t, exported.Body, secrets)
	}

	identityPatch := doJSON(t, app, http.MethodPatch, "/api/admin/resources/identity-providers/"+identityID, map[string]any{
		"name": "Corporate identity updated",
		"fields": map[string]any{
			"provider_type": "oauth2",
			"client_id":     "identity-client-id",
			"client_secret": adminResourceSecretASCIIMask,
		},
	}, "sensitive-resource-admin")
	if identityPatch.Code != http.StatusOK {
		t.Fatalf("patch identity provider = %d: %s", identityPatch.Code, identityPatch.Body)
	}
	assertSensitiveResourceResponse(t, identityPatch.Body, identitySecrets)

	notificationPatchFields := map[string]any{"type": "webhook"}
	for key := range notificationSecrets {
		notificationPatchFields[key] = ""
	}
	notificationPatchFields["webhook_url"] = providerHeaderMask
	notificationPatch := doJSON(t, app, http.MethodPatch, "/api/admin/resources/notification-channels/"+notificationID, map[string]any{
		"name": "Security notifications updated", "fields": notificationPatchFields,
	}, "sensitive-resource-admin")
	if notificationPatch.Code != http.StatusOK {
		t.Fatalf("patch notification channel = %d: %s", notificationPatch.Code, notificationPatch.Body)
	}
	assertSensitiveResourceResponse(t, notificationPatch.Body, notificationSecrets)

	assertStoredAdminResourceSecrets(t, store, "identity-providers", identityID, identitySecrets)
	assertStoredAdminResourceSecrets(t, store, "notification-channels", notificationID, notificationSecrets)
	for _, event := range store.ListAuditEvents() {
		for key, value := range mergeSecretMaps(identitySecrets, notificationSecrets) {
			if strings.Contains(event.BeforeSnapshot+event.AfterSnapshot, value) {
				t.Fatalf("audit snapshot leaked %s: %+v", key, event)
			}
		}
	}

	auditResponse := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, "sensitive-resource-admin")
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("list audit events = %d: %s", auditResponse.Code, auditResponse.Body)
	}
	assertNoSecretValues(t, auditResponse.Body, mergeSecretMaps(identitySecrets, notificationSecrets))
}

func TestAdminResourceSecretPlaceholderContract(t *testing.T) {
	for _, value := range []any{"", adminResourceSecretASCIIMask, providerHeaderMask, "[redacted]"} {
		if !isAdminResourceSecretPlaceholder(value) {
			t.Errorf("documented placeholder %q was rejected", value)
		}
	}
	for _, value := range []any{"replacement-secret", nil, 8} {
		if isAdminResourceSecretPlaceholder(value) {
			t.Errorf("credential value %v was treated as a placeholder", value)
		}
	}
}

func TestAdminAuditSnapshotRedactsExactCredentialKeysWithoutMaskingTokenMetrics(t *testing.T) {
	secrets := map[string]any{
		"url":             "https://hooks.example.test/services/audit-only-secret",
		"id_token":        "id-token-value",
		"oauth_token":     "oauth-token-value",
		"session_token":   "session-token-value",
		"credential_blob": "credential-blob-value",
		"private_key":     "private-key-value",
		"cookie":          "session=cookie-value",
		"set-cookie":      "session=set-cookie-value",
	}
	snapshot := auditSnapshotJSON(map[string]any{
		"credentials":     secrets,
		"input_tokens":    12,
		"token_limit_tpm": 5000,
	})
	for key, value := range secrets {
		if strings.Contains(snapshot, value.(string)) {
			t.Fatalf("audit snapshot leaked %s: %s", key, snapshot)
		}
	}
	if !strings.Contains(snapshot, `"input_tokens":12`) || !strings.Contains(snapshot, `"token_limit_tpm":5000`) {
		t.Fatalf("audit snapshot redacted non-secret token metrics: %s", snapshot)
	}
}

func TestAdminAuditEventsRedactLegacyStoredSnapshots(t *testing.T) {
	store := NewMemoryStore()
	const legacySecret = "https://hooks.example.test/services/legacy-stored-secret"
	store.RecordAuditEvent(AuditEvent{
		ID:           "audit_legacy_notification_secret",
		Action:       "update",
		ResourceType: "notification-channels",
		ResourceID:   "ntf_legacy",
		Status:       "success",
		AfterSnapshot: snapshotJSON(map[string]any{
			"fields": map[string]any{"url": legacySecret},
		}),
	})
	server := NewWithConfig(store, Config{AdminToken: "legacy-audit-admin", SecretKey: "legacy-audit-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/audit/events", nil, "legacy-audit-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("list legacy audit events = %d: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, legacySecret) || !strings.Contains(response.Body, "[redacted]") {
		t.Fatalf("legacy audit snapshot was not redacted on read: %s", response.Body)
	}
}

func TestNotificationChannelPatchPreservesLegacySecretAliases(t *testing.T) {
	tests := []struct {
		name         string
		channelType  string
		legacyKey    string
		canonicalKey string
	}{
		{name: "webhook URL", channelType: "webhook", legacyKey: "url", canonicalKey: "webhook_url"},
		{name: "Telegram bot token", channelType: "telegram", legacyKey: "bot_token", canonicalKey: "telegram_bot_token"},
		{name: "WhatsApp access token", channelType: "whatsapp", legacyKey: "whatsapp_access_token", canonicalKey: "access_token"},
		{name: "SMTP password", channelType: "email", legacyKey: "password", canonicalKey: "smtp_password"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			secret := "legacy-" + strings.ReplaceAll(test.legacyKey, "_", "-") + "-value"
			resource := store.CreateResource("notification-channels", AdminResource{
				Name:   test.name,
				Status: StatusActive,
				Fields: map[string]any{"type": test.channelType, test.legacyKey: secret},
			})
			server := NewWithConfig(store, Config{AdminToken: "legacy-alias-admin", SecretKey: "legacy-alias-secret-key"})
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

			response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/notification-channels/"+resource.ID, map[string]any{
				"name": test.name,
				"fields": map[string]any{
					"type":            test.channelType,
					test.canonicalKey: providerHeaderMask,
				},
			}, "legacy-alias-admin")
			if response.Code != http.StatusOK {
				t.Fatalf("patch legacy notification channel = %d: %s", response.Code, response.Body)
			}
			assertSensitiveResourceResponse(t, response.Body, map[string]string{test.canonicalKey: secret})

			stored, err := server.findResource("notification-channels", resource.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := stringField(stored.Fields, test.canonicalKey); got != secret {
				t.Fatalf("stored %s = %q, want legacy secret preserved", test.canonicalKey, got)
			}
			if test.legacyKey != test.canonicalKey {
				if _, retained := stored.Fields[test.legacyKey]; retained {
					t.Fatalf("legacy alias %s was not normalized: %+v", test.legacyKey, stored.Fields)
				}
			}
		})
	}
}

func TestNotificationChannelPatchDoesNotCarryMaskedSecretAcrossChannelTypes(t *testing.T) {
	store := NewMemoryStore()
	const telegramSecret = "legacy-telegram-secret"
	resource := store.CreateResource("notification-channels", AdminResource{
		Name:   "Legacy Telegram",
		Status: StatusActive,
		Fields: map[string]any{"type": "telegram", "bot_token": telegramSecret, "telegram_chat_id": "chat-1"},
	})
	server := NewWithConfig(store, Config{AdminToken: "channel-switch-admin", SecretKey: "channel-switch-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/notification-channels/"+resource.ID, map[string]any{
		"name": "WhatsApp",
		"fields": map[string]any{
			"type":                     "whatsapp",
			"access_token":             providerHeaderMask,
			"whatsapp_phone_number_id": "phone-1",
			"whatsapp_to":              "+15550001111",
		},
	}, "channel-switch-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("switch notification channel type = %d: %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, telegramSecret) {
		t.Fatalf("channel switch response leaked old token: %s", response.Body)
	}
	stored, err := server.findResource("notification-channels", resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := stored.Fields["access_token"]; retained {
		t.Fatalf("masked token was carried across channel types: %+v", stored.Fields)
	}
	if _, retained := stored.Fields["bot_token"]; retained {
		t.Fatalf("old channel token survived type switch: %+v", stored.Fields)
	}
}

func TestSensitiveResourcePatchUsesNullToClearStoredSecrets(t *testing.T) {
	store := NewMemoryStore()
	const (
		identitySecret = "identity-secret-to-clear"
		webhookSecret  = "https://hooks.example.test/services/secret-to-clear"
	)
	identity := store.CreateResource("identity-providers", AdminResource{
		Name: "Clearable IdP", Status: StatusActive,
		Fields: map[string]any{"client_id": "client", "client_secret": identitySecret},
	})
	channel := store.CreateResource("notification-channels", AdminResource{
		Name: "Clearable Webhook", Status: StatusActive,
		Fields: map[string]any{"type": "webhook", "url": webhookSecret},
	})
	server := NewWithConfig(store, Config{AdminToken: "clear-secret-admin", SecretKey: "clear-secret-key"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	identityResponse := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/identity-providers/"+identity.ID, map[string]any{
		"name":   identity.Name,
		"fields": map[string]any{"client_id": "client", "client_secret": nil},
	}, "clear-secret-admin")
	if identityResponse.Code != http.StatusOK || strings.Contains(identityResponse.Body, identitySecret) {
		t.Fatalf("clear identity secret = %d: %s", identityResponse.Code, identityResponse.Body)
	}
	storedIdentity, err := server.findResource("identity-providers", identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := storedIdentity.Fields["client_secret"]; retained {
		t.Fatalf("identity secret survived explicit null: %+v", storedIdentity.Fields)
	}

	channelResponse := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/notification-channels/"+channel.ID, map[string]any{
		"name":   channel.Name,
		"fields": map[string]any{"type": "webhook", "webhook_url": nil},
	}, "clear-secret-admin")
	if channelResponse.Code != http.StatusOK || strings.Contains(channelResponse.Body, webhookSecret) {
		t.Fatalf("clear notification secret = %d: %s", channelResponse.Code, channelResponse.Body)
	}
	storedChannel, err := server.findResource("notification-channels", channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"url", "webhook_url"} {
		if _, retained := storedChannel.Fields[key]; retained {
			t.Fatalf("notification secret alias %s survived explicit null: %+v", key, storedChannel.Fields)
		}
	}

	auditResponse := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/audit/events", nil, "clear-secret-admin")
	if auditResponse.Code != http.StatusOK || strings.Contains(auditResponse.Body, identitySecret) || strings.Contains(auditResponse.Body, webhookSecret) {
		t.Fatalf("cleared secrets leaked through audit response = %d: %s", auditResponse.Code, auditResponse.Body)
	}
}

func assertSensitiveResourceResponse(t *testing.T, body string, secrets map[string]string) {
	t.Helper()
	assertNoSecretValues(t, body, secrets)
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), providerHeaderMask) || !strings.Contains(string(encoded), `_configured":true`) {
		t.Fatalf("response did not expose only a mask and configuration state: %s", body)
	}
}

func assertNoSecretValues(t *testing.T, body string, secrets map[string]string) {
	t.Helper()
	for key, value := range secrets {
		if strings.Contains(body, value) {
			t.Fatalf("response leaked %s: %s", key, body)
		}
	}
}

func responseResourceID(t *testing.T, body string) string {
	t.Helper()
	var resource AdminResource
	if err := json.Unmarshal([]byte(body), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ID == "" {
		t.Fatalf("resource response has no id: %s", body)
	}
	return resource.ID
}

func assertStoredAdminResourceSecrets(t *testing.T, store *GormStore, kind, id string, secrets map[string]string) {
	t.Helper()
	for _, resource := range store.ListResources(kind) {
		if resource.ID != id {
			continue
		}
		for key, want := range secrets {
			if got := stringField(resource.Fields, key); got != want {
				t.Fatalf("stored %s %s = %q, want preserved secret", kind, key, got)
			}
		}
		return
	}
	t.Fatalf("stored %s resource %s not found", kind, id)
}

func mergeSecretMaps(maps ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, values := range maps {
		for key, value := range values {
			merged[key] = value
		}
	}
	return merged
}
