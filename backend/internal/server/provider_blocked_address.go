package server

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

// Only normalized DNS addresses are exposed, never a request URL or credentials.
type providerBlockedAddressError struct{ addresses []string }

func newProviderBlockedAddressError(resolved []net.IPAddr) error {
	unique := map[string]bool{}
	for _, address := range resolved {
		if address.IP != nil {
			unique[address.IP.String()] = true
		}
	}
	addresses := make([]string, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if len(addresses) > 16 {
		addresses = addresses[:16]
	}
	return &providerBlockedAddressError{addresses: addresses}
}

func (e *providerBlockedAddressError) Error() string {
	return "Provider hostname resolves only to disallowed addresses: " + strings.Join(e.addresses, ", ") + ". If the backend uses a Fake-IP proxy, verify its address pool in System Settings > General Settings > Edit Settings > Synthetic DNS / Fake-IP Ranges."
}

func (e *providerBlockedAddressError) Unwrap() error { return errProviderResolvedAddressDisallowed }

func (e *providerBlockedAddressError) httpError(code string) *HTTPError {
	result := NewHTTPError(http.StatusBadGateway, code, e.Error())
	result.Details = map[string]any{"blocked_ips": append([]string(nil), e.addresses...)}
	return result
}

func (e *providerBlockedAddressError) As(target any) bool {
	if result, ok := target.(**HTTPError); ok {
		*result = e.httpError("provider_address_blocked")
		return true
	}
	return false
}

func providerBlockedAddressDetails(err error) any {
	value := AsHTTPError(err)
	if value != nil && (value.Code == "provider_address_blocked" || value.Code == "provider_models_address_blocked") {
		return value.Details
	}
	return nil
}
