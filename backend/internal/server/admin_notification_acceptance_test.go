package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationChannelTestDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var redirected atomic.Bool
			login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirected.Store(true)
				_, _ = io.WriteString(w, "<html>Please sign in</html>")
			}))
			t.Cleanup(login.Close)
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, login.URL+"/private-token", status)
			}))
			t.Cleanup(webhook.Close)
			store := NewMemoryStore()
			channel := store.CreateResource("notification-channels", AdminResource{
				Name: "Redirecting webhook", Status: StatusActive,
				Fields: map[string]any{"type": "webhook", "webhook_url": webhook.URL},
			})
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
			response := doJSON(t, server.Handler(), http.MethodPost, notificationTestPath(channel.ID), nil, "")
			var delivery AlertDelivery
			if err := json.Unmarshal([]byte(response.Body), &delivery); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || delivery.Status != "failed" || delivery.StatusCode != status || redirected.Load() {
				t.Fatalf("redirect must fail without contacting its target: redirected=%t response=%d %s", redirected.Load(), response.Code, response.Body)
			}
			assertAlertDeliverySurfacesHideSecrets(t, server.Handler(), store, response.Body, "dev_admin_token", "private-token")
		})
	}
}

func TestSMTPDeliveryOutcomeDependsOnDataAcceptance(t *testing.T) {
	for _, scenario := range []string{"quit_eof", "quit_rejected", "quit_timeout", "data_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			var accepted atomic.Bool
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				peer := textproto.NewConn(conn)
				if err := peer.PrintfLine("220 localhost ESMTP"); err != nil {
					return
				}
				for {
					command, err := peer.ReadLine()
					if err != nil {
						return
					}
					switch {
					case command == "DATA":
						if err := peer.PrintfLine("354 Send message"); err != nil {
							return
						}
						if _, err := peer.ReadDotBytes(); err != nil {
							return
						}
						if scenario == "data_rejected" {
							_ = peer.PrintfLine("550 Message rejected")
							return
						}
						accepted.Store(true)
						if err := peer.PrintfLine("250 Message accepted"); err != nil {
							return
						}
					case command == "QUIT":
						if scenario == "quit_rejected" {
							_ = peer.PrintfLine("500 Quit failed")
						}
						if scenario == "quit_timeout" {
							_, _ = io.Copy(io.Discard, conn)
						}
						return
					default:
						if err := peer.PrintfLine("250 localhost"); err != nil {
							return
						}
					}
				}
			}()
			host, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err = sendEmail(ctx, map[string]any{"smtp_host": host, "smtp_port": port, "smtp_from": "sender@example.test"}, []string{"ops@example.test"}, []byte("Subject: Test\r\n\r\nNotification\r\n"), nil)
			if scenario == "data_rejected" {
				if accepted.Load() || err == nil || !strings.Contains(err.Error(), "550") {
					t.Fatalf("DATA rejection must fail delivery: %v", err)
				}
			} else if !accepted.Load() || err != nil {
				t.Fatalf("accepted DATA must remain successful despite QUIT failure: accepted=%t error=%v", accepted.Load(), err)
			}
		})
	}
}
