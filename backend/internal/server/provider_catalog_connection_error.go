package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
)

var errProviderResolvedAddressDisallowed = errors.New("provider hostname resolves only to disallowed addresses")

// Expose only structured IP diagnostics or fixed messages, never raw transport errors.
func providerCatalogConnectionError(err error) error {
	var blocked *providerBlockedAddressError
	if errors.As(err, &blocked) {
		return blocked.httpError("provider_models_address_blocked")
	}
	code, message := "provider_models_request_failed", "Failed to request upstream models"
	var dns *net.DNSError
	var network net.Error
	var certificate *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	switch {
	case errors.Is(err, errProviderResolvedAddressDisallowed):
		code, message = "provider_models_address_blocked", "Provider hostname resolves to an address blocked by the network security policy"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout():
		code, message = "provider_models_timeout", "Upstream model catalog connection timed out"
	case errors.As(err, &dns):
		code, message = "provider_models_dns_failed", "Provider hostname could not be resolved"
	case errors.As(err, &certificate), errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &invalidCertificate):
		code, message = "provider_models_tls_failed", "Provider TLS certificate verification failed"
	}
	return NewHTTPError(http.StatusBadGateway, code, message)
}
