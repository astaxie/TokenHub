package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestPasswordResetSMTPFailuresRedactSavedCredentials(t *testing.T) {
	for _, field := range []string{"smtp_password", "password"} {
		for _, storage := range []string{"plaintext", "encrypted"} {
			t.Run(field+"/"+storage, func(t *testing.T) {
				store := NewMemoryStore()
				_, adminToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "notification-admin", Email: "admin@example.test", Role: "admin", Status: StatusActive})
				team := store.CreateResource("teams", AdminResource{Name: "Notification test team", Status: StatusActive})
				_, leaderToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "notification-leader", Email: "leader@example.test", Role: "team_leader", TeamID: team.ID, Status: StatusActive})
				user, err := store.CreateAdminUser(AdminUser{Username: "reset-recipient", Email: "reset@example.test", Role: "user", TeamID: team.ID, Status: StatusActive}, "reset-test-password")
				if err != nil {
					t.Fatal(err)
				}
				server := New(store)
				t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
				app := server.Handler()
				const password = "smtp-reset-private-password"
				host, port, _ := notificationCredentialSMTPServer(t, password, true)
				storedPassword := password
				if storage == "encrypted" {
					storedPassword, err = store.protectAdminResourceSecret(password)
					if err != nil {
						t.Fatal(err)
					}
				}
				store.CreateResource("notification-channels", AdminResource{
					Name: "Rejecting SMTP", Status: StatusActive,
					Fields: map[string]any{"type": "email", "smtp_host": host, "smtp_port": port, "smtp_from": "sender@example.test", "smtp_username": "notification-user", field: storedPassword},
				})
				secrets := map[string]string{"password": password, "stored_password": storedPassword}
				reset := doJSON(t, app, http.MethodPost, "/api/admin/users/"+user.ID+"/reset-password-email", nil, leaderToken)
				if reset.Code != http.StatusInternalServerError || !strings.Contains(reset.Body, "535") || !strings.Contains(reset.Body, "internal_error") {
					t.Fatalf("unexpected reset failure: %d %s", reset.Code, reset.Body)
				}
				if strings.Contains(reset.Body, password) {
					t.Error("password-reset response leaked the SMTP password")
				}
				for _, username := range []string{"new-import", user.Username} {
					imported := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{"users": []map[string]any{{
						"username": username, "email": username + "@example.test", "role": "user",
					}}}, leaderToken)
					if imported.Code != http.StatusOK || !strings.Contains(imported.Body, "535") || !strings.Contains(imported.Body, `"reset_emails_sent":0`) {
						t.Fatalf("unexpected import failure: %d %s", imported.Code, imported.Body)
					}
					if strings.Contains(imported.Body, password) {
						t.Error("user-import response leaked the SMTP password")
					}
				}
				for _, event := range store.ListAuditEvents() {
					if strings.Contains(event.AfterSnapshot, password) {
						t.Error("user-import audit persisted the SMTP password")
					}
				}
				audit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, adminToken)
				if audit.Code != http.StatusOK {
					t.Fatalf("list audit events: %d %s", audit.Code, audit.Body)
				}
				assertNoSecretValues(t, audit.Body, secrets)
			})
		}
	}
}
