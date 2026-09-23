package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProviderCatalogConnectionErrorsAreClassifiedAndRedacted(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		code  string
	}{
		{"blocked address", fmt.Errorf("host secret.example: %w", errProviderResolvedAddressDisallowed), "provider_models_address_blocked"},
		{"DNS failure", &net.DNSError{Err: "secret-token", Name: "secret.example", IsNotFound: true}, "provider_models_dns_failed"},
		{"DNS timeout", &net.DNSError{Err: "secret-token", Name: "secret.example", IsTimeout: true}, "provider_models_timeout"},
		{"deadline", context.DeadlineExceeded, "provider_models_timeout"},
		{"TLS trust", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "provider_models_tls_failed"},
		{"TLS hostname", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "secret.example"}, "provider_models_tls_failed"},
		{"TLS expiry", x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired}, "provider_models_tls_failed"},
		{"unknown transport", errors.New("secret-token at secret.example"), "provider_models_request_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, tt.cause })}
			_, err := CustomProviderCatalogFromUpstream(context.Background(), client, ProviderCreateRequest{Type: ProviderOpenAICompatible, BaseURL: "https://public.example/v1", APIKey: "secret-token"})
			got := AsHTTPError(err)
			if got.Code != tt.code {
				t.Fatalf("expected %s, got %v", tt.code, err)
			}
			if strings.Contains(fmt.Sprint(err), "secret") {
				t.Fatalf("transport details leaked: %v", err)
			}
		})
	}
}

func TestBlockedDNSAddressNeverDialsAndRetainsErrorClassification(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("198.18.0.82")}}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("blocked address must never be dialed")
		return nil, nil
	}
	_, err := dialGuardedUpstream(context.Background(), "tcp", "public.example:443", nil, nil, time.Second, lookup, dial)
	if !errors.Is(err, errProviderResolvedAddressDisallowed) {
		t.Fatalf("missing blocked-address classification: %v", err)
	}
	_, err = resolveProviderProxyTargetIPs(context.Background(), "public.example", nil, nil, lookup)
	if !errors.Is(err, errProviderResolvedAddressDisallowed) {
		t.Fatalf("missing proxy target classification: %v", err)
	}
}
