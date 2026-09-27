package server

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestNotificationEmailUsesSavedCredentials(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		passwordKey string
		password    string
		encrypted   bool
	}{
		{"literal_prefix", "smtp_password", "enc:v1:literal-password", false},
		{"literal_prefix_alias", "password", "enc:v1:literal-password", false},
		{"encrypted", "smtp_password", "encrypted-smtp-password", true},
		{"encrypted_alias", "password", "encrypted-smtp-password", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := NewMemoryStore()
			_, adminToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "notification-admin", Email: "admin@example.test", Role: "admin", Status: StatusActive})
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
			app := server.Handler()
			host, port, messages := notificationCredentialSMTPServer(t, scenario.password, false)
			storedPassword := scenario.password
			if scenario.encrypted {
				var err error
				storedPassword, err = store.protectAdminResourceSecret(storedPassword)
				if err != nil {
					t.Fatal(err)
				}
			}
			created := doJSON(t, app, http.MethodPost, "/api/admin/resources/notification-channels", map[string]any{
				"name": "SMTP credential regression", "status": StatusActive,
				"fields": map[string]any{
					"type": "email", "smtp_host": host, "smtp_port": port,
					"smtp_username": "notification-user", scenario.passwordKey: storedPassword,
					"smtp_from": "sender@example.test", "email_to": "ops@example.test",
				},
			}, adminToken)
			if created.Code != http.StatusCreated {
				t.Fatalf("create channel: %d %s", created.Code, created.Body)
			}
			channelID := responseResourceID(t, created.Body)
			alert := AlertEvent{ID: "alt_credential_test", Code: "credential_test", Severity: "info", Message: "Credential test", CreatedAt: time.Now().UTC()}
			if err := store.db.Create(&alert).Error; err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{notificationTestPath(channelID), "/api/admin/alerts/" + alert.ID + "/deliver"} {
				response := doJSON(t, app, http.MethodPost, path, map[string]any{"channel_id": channelID}, adminToken)
				if response.Code != http.StatusOK || !strings.Contains(response.Body, `"status":"success"`) {
					t.Fatalf("delivery via %s failed: %d %s", path, response.Code, response.Body)
				}
				assertAlertDeliverySurfacesHideSecrets(t, app, store, response.Body, adminToken, scenario.password, storedPassword)
				select {
				case message := <-messages:
					if !strings.Contains(message, "To: ops@example.test") {
						t.Fatalf("notification recipient missing: %s", message)
					}
				case <-time.After(time.Second):
					t.Fatal("notification was not accepted by SMTP")
				}
			}
			user, err := store.CreateAdminUser(AdminUser{Username: "reset-recipient", Email: "reset@example.test", Role: "user", Status: StatusActive}, "reset-test-password")
			if err != nil {
				t.Fatal(err)
			}
			reset := doJSON(t, app, http.MethodPost, "/api/admin/users/"+user.ID+"/reset-password-email", nil, adminToken)
			if reset.Code != http.StatusOK {
				t.Fatalf("password reset email failed: %d %s", reset.Code, reset.Body)
			}
			assertPasswordResetEmail(t, messages, user.Email)
			stored, err := server.findResource("notification-channels", channelID)
			if err != nil || stringField(stored.Fields, scenario.passwordKey) != storedPassword {
				t.Fatalf("delivery changed the stored credential: %v", err)
			}
		})
	}
}

func notificationCredentialSMTPServer(t *testing.T, password string, reject bool) (string, string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 3)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				peer := textproto.NewConn(conn)
				if err := peer.PrintfLine("220 localhost ESMTP"); err != nil {
					return
				}
				authenticated := false
				for {
					command, err := peer.ReadLine()
					if err != nil {
						return
					}
					response := "250 OK"
					switch {
					case strings.HasPrefix(command, "EHLO "):
						response = "250-localhost\r\n250 AUTH PLAIN"
					case strings.HasPrefix(command, "AUTH PLAIN "):
						credentials, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(command, "AUTH PLAIN "))
						authenticated = err == nil && string(credentials) == "\x00notification-user\x00"+password
						response = "535 Authentication failed"
						if authenticated {
							response = "235 Authenticated"
							if reject {
								authenticated = false
								response = "535 Rejected password " + password
							}
						}
					case command == "QUIT":
						_ = peer.PrintfLine("221 Bye")
						return
					case !authenticated:
						response = "530 Authentication required"
					case command == "DATA":
						if err := peer.PrintfLine("354 Send message"); err != nil {
							return
						}
						body, err := peer.ReadDotBytes()
						if err != nil {
							return
						}
						messages <- string(body)
					}
					if err := peer.PrintfLine("%s", response); err != nil {
						return
					}
				}
			}()
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return host, port, messages
}
