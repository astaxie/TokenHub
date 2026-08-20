package server

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testTLSCertificate builds a throwaway self-signed certificate for the
// in-process implicit-TLS SMTP server. It is only ever used over loopback by
// tests that dial with ServerName "localhost" or "127.0.0.1".
func testTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// configureTestTLSSMTPChannel registers an active email notification channel
// backed by an in-process SMTP server that requires implicit TLS on the wire
// (like the 465/994 ports). It reuses serveDirectTLSSMTPConnection after the
// TLS handshake. Returns the channel of received message bodies and the
// certificate pool the test must trust for the sendEmail dial.
func configureTestTLSSMTPChannel(t *testing.T, store *GormStore) (<-chan string, *x509.CertPool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	cert := testTLSCertificate(t)
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}})
	messages := make(chan string, 10)
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go serveDirectTLSSMTPConnection(conn, messages)
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	rootCAs := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	rootCAs.AddCert(leaf)
	store.CreateResource("notification-channels", AdminResource{
		Name:   "Implicit TLS SMTP",
		Status: StatusActive,
		Fields: map[string]any{
			"type":            "email",
			"smtp_host":       host,
			"smtp_port":       port,
			"smtp_encryption": "ssl",
			"smtp_username":   "tokenhub",
			"smtp_password":   "secret",
			"smtp_from":       "tokenhub@example.com",
			"email_to":        "ops@example.com",
			"display_name":    "TokenHub",
		},
	})
	return messages, rootCAs
}

func deliverEmailAlert(t *testing.T, store *GormStore, channelID string, rootCAs *x509.CertPool) {
	t.Helper()
	alert := AlertEvent{
		ID:        "alt_email_tls",
		ScopeType: "provider",
		ScopeID:   "prv_test",
		Severity:  "warning",
		Code:      "monitor_check_failed",
		Message:   "Provider failed",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	srv := New(store)
	srv.smtpRootCAs = rootCAs
	app := srv.Handler()
	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{"channel_id": channelID}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"success"`) {
		t.Fatalf("expected successful email delivery, got %d: %s", resp.Code, resp.Body)
	}
}

func TestEmailDeliveryViaImplicitTLSSMTP(t *testing.T) {
	store := NewMemoryStore()
	messages, rootCAs := configureTestTLSSMTPChannel(t, store)
	channels := store.ListResources("notification-channels")
	if len(channels) != 1 {
		t.Fatalf("expected one notification channel, got %d", len(channels))
	}
	deliverEmailAlert(t, store, channels[0].ID, rootCAs)

	select {
	case message := <-messages:
		if !strings.Contains(message, "To: ops@example.com") || !strings.Contains(message, "monitor_check_failed") {
			t.Fatalf("unexpected alert email: %s", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for implicit-TLS alert email")
	}
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) != 1 || deliveries[0].Channel != "email" || deliveries[0].Status != "success" {
		t.Fatalf("expected successful email delivery record, got %+v", deliveries)
	}
}

func TestDirectSMTPTLSEnabled(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   bool
	}{
		{name: "ssl", fields: map[string]any{"smtp_encryption": "ssl"}, want: true},
		{name: "tls uppercase", fields: map[string]any{"smtp_encryption": "TLS"}, want: true},
		{name: "smtps", fields: map[string]any{"smtp_encryption": "smtps"}, want: true},
		{name: "implicit", fields: map[string]any{"smtp_encryption": "implicit"}, want: true},
		{name: "legacy encryption alias", fields: map[string]any{"encryption": "ssl"}, want: true},
		{name: "starttls", fields: map[string]any{"smtp_encryption": "starttls"}, want: false},
		{name: "empty", fields: map[string]any{}, want: false},
		{name: "unrelated", fields: map[string]any{"type": "email"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := directSMTPTLSEnabled(tc.fields); got != tc.want {
				t.Fatalf("directSMTPTLSEnabled(%v) = %v, want %v", tc.fields, got, tc.want)
			}
		})
	}
}

// serveDirectTLSSMTPConnection handles one implicit-TLS SMTP session on top of
// an already-established TLS connection. Unlike the plain serveTestSMTPConnection
// helper it answers AUTH with 235 so the full authenticated path is exercised.
func serveDirectTLSSMTPConnection(conn net.Conn, messages chan<- string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(response string) bool {
		if _, err := writer.WriteString(response + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !write("220 localhost ESMTP") {
		return
	}
	var message strings.Builder
	readingData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		if readingData {
			if command == "." {
				messages <- message.String()
				message.Reset()
				readingData = false
				if !write("250 queued") {
					return
				}
				continue
			}
			message.WriteString(strings.TrimPrefix(command, "."))
			message.WriteByte('\n')
			continue
		}
		switch {
		case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
			if !write("250-localhost") || !write("250 AUTH LOGIN PLAIN") {
				return
			}
		case strings.HasPrefix(command, "AUTH "):
			if !write("235 ok") {
				return
			}
		case command == "DATA":
			readingData = true
			if !write("354 end with <CRLF>.<CRLF>") {
				return
			}
		case command == "QUIT":
			_ = write("221 bye")
			return
		default:
			if !write("250 ok") {
				return
			}
		}
	}
}
