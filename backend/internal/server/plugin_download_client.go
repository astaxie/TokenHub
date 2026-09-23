package server

import (
	"context"
	"net"
	"net/http"
	"time"
)

type pluginDownloadTransport struct{ next http.RoundTripper }

func (t pluginDownloadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validatePluginInstallHTTPSURL("Plugin download", req.URL.String()); err != nil {
		return nil, err
	}
	if err := validateProviderUpstreamBaseURL(req.URL, nil, false); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

func newPluginDownloadClient(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: strictProviderUpstreamRedirect}
	var next http.RoundTripper = http.DefaultTransport
	if base != nil {
		if base.Timeout > 0 {
			client.Timeout = base.Timeout
		}
		if base.Transport != nil {
			next = base.Transport
		}
	}
	if transport, ok := next.(*http.Transport); ok {
		guarded := transport.Clone()
		guarded.Proxy = nil
		guarded.DialTLS = nil //nolint:staticcheck // Clear injected TLS dialers that bypass address validation.
		guarded.DialTLSContext = nil
		dial := guarded.DialContext
		if dial == nil {
			dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
		}
		guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, errProviderUpstreamDialDisallowed
			}
			for _, address := range addresses {
				if isDisallowedProviderUpstreamIP(address.IP) {
					return nil, errProviderUpstreamDialDisallowed
				}
			}
			return raceValidatedUpstreamCandidates(ctx, network, port, addresses, dial)
		}
		next = guarded
	}
	client.Transport = pluginDownloadTransport{next: next}
	return client
}
