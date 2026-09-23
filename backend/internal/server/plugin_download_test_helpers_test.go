package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newPluginDownloadTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	address := server.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	server.URL = "https://93.184.216.34:" + port
	transport := server.Client().Transport.(*http.Transport)
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != net.JoinHostPort("93.184.216.34", port) {
			t.Errorf("unexpected download target %s", target)
			return nil, errProviderUpstreamDialDisallowed
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return server
}
