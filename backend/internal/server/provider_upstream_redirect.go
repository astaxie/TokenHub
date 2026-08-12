package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// providerUpstreamPolicyTransport validates the actual request URL immediately
// before the underlying transport can send credentials. This protects records
// created before the current persistence guard or through a direct store path.
type providerUpstreamPolicyTransport struct {
	next           http.RoundTripper
	allowedPrivate []*net.IPNet
}

func guardProviderUpstreamRequests(next http.RoundTripper, allowedPrivate []*net.IPNet) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &providerUpstreamPolicyTransport{next: next, allowedPrivate: allowedPrivate}
}

func (transport *providerUpstreamPolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	if err := validateProviderUpstreamBaseURL(req.URL, transport.allowedPrivate, providerUpstreamLoopbackAllowed()); err != nil {
		return nil, err
	}
	return transport.next.RoundTrip(req)
}

// strictProviderUpstreamRedirect permits only same-origin redirects. Provider
// requests can carry credentials in non-standard headers that net/http may
// copy across hosts, so validating only the destination IP class is not
// sufficient.
func strictProviderUpstreamRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if err := validateProviderUpstreamBaseURL(req.URL, nil, false); err != nil {
		return err
	}
	if len(via) == 0 || via[0] == nil || via[0].URL == nil {
		return fmt.Errorf("redirect has no original request")
	}
	original := via[0].URL
	if !strings.EqualFold(req.URL.Scheme, original.Scheme) || !strings.EqualFold(req.URL.Host, original.Host) {
		return fmt.Errorf("provider upstream redirect changed origin")
	}
	return nil
}
