package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// The fake proxy terminates CONNECT locally, without forwarding test traffic.
func serveLocalHTTPReviewTunnel(t *testing.T, w http.ResponseWriter, r *http.Request, inspect func(*http.Request)) {
	t.Helper()
	conn, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()
	if _, err = fmt.Fprint(buffered, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		t.Error(err)
		return
	}
	if err = buffered.Flush(); err != nil {
		t.Error(err)
		return
	}
	inner, err := http.ReadRequest(buffered.Reader)
	if err != nil {
		t.Error(err)
		return
	}
	defer inner.Body.Close()
	if inspect != nil {
		inspect(inner)
	}
	if _, err = io.Copy(io.Discard, inner.Body); err != nil {
		t.Error(err)
		return
	}
	if _, err = fmt.Fprint(buffered, "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n"); err != nil {
		t.Error(err)
		return
	}
	if err = buffered.Flush(); err != nil {
		t.Error(err)
	}
}

func TestLocalHTTPProxyPinsTargetAndPreservesHost(t *testing.T) {
	useAutoLocalUpstreams(t)
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", "true")
	var tunnels, received atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tunnels.Add(1)
		if r.Method != http.MethodConnect || r.RequestURI != "192.168.2.4:8000" {
			t.Errorf("proxy target = %s %s", r.Method, r.RequestURI)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("provider credential leaked into CONNECT")
		}
		serveLocalHTTPReviewTunnel(t, w, r, func(inner *http.Request) {
			received.Add(1)
			if inner.Host != "models.corp.example:8000" || inner.URL.Path != "/v1/chat/completions" {
				t.Errorf("inner target = %s %s", inner.Host, inner.URL)
			}
			if inner.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("inner credential was lost")
			}
		})
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) { return proxyURL, nil }).(*providerEnvironmentProxyTransport)
	defer transport.CloseIdleConnections()
	var lookups atomic.Int32
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) > 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("192.168.2.4")}}, nil
	}
	req, _ := http.NewRequest(http.MethodPost, "http://models.corp.example:8000/v1/chat/completions", strings.NewReader("test body"))
	req.Header.Set("Authorization", "Bearer test-key")
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if received.Load() != 1 || tunnels.Load() != 1 || lookups.Load() != 1 {
		t.Fatalf("received=%d tunnels=%d lookups=%d", received.Load(), tunnels.Load(), lookups.Load())
	}
	if _, err = transport.RoundTrip(req); err == nil {
		t.Fatal("public DNS result bypassed request preflight")
	}
	if tunnels.Load() != 1 {
		t.Fatal("rejected request reached proxy")
	}
}

func TestLocalProviderPersistenceDoesNotResolveDNS(t *testing.T) {
	useAutoLocalUpstreams(t)
	var lookups atomic.Int32
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		lookups.Add(1)
		return nil, errors.New("DNS unavailable")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	provider := store.AddProvider(Provider{Name: "Local review", Type: ProviderOpenAICompatible})
	endpoint := "http://offline-model.invalid:8000/v1"
	if err := ValidateProviderUpstreamBaseURL(endpoint); err != nil {
		t.Fatal(err)
	}
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Local resource", ResourceType: ProviderResourceAPIKey, BaseURL: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	resource.BaseURL = "http://another-offline-model.invalid:8000/v1"
	if _, err = store.UpdateProviderResource(resource.ID, resource); err != nil {
		t.Fatal(err)
	}
	imported, err := store.ImportProviderResources([]ProviderResource{{ProviderID: provider.ID, Name: "Imported local", ResourceType: ProviderResourceAPIKey, BaseURL: endpoint}})
	if err != nil || imported.Failed != 0 {
		t.Fatalf("import=%+v err=%v", imported, err)
	}
	if lookups.Load() != 0 {
		t.Fatalf("persistence made %d DNS calls", lookups.Load())
	}
}

func TestLocalCredentialPreviewRespectsClearAndRetain(t *testing.T) {
	useAutoLocalUpstreams(t)
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprint(clear), func(t *testing.T) {
			var got string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "local-model"}}})
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			store.AddProvider(Provider{ID: "prv_clear", Name: "Local", Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/v1", APIKey: "saved-test-key", Status: StatusActive})
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{"provider_id": "prv_clear", "api_key": "", "clear_api_key": clear}, "")
			if response.Code != http.StatusOK {
				t.Fatalf("preview=%d %s", response.Code, response.Body)
			}
			want := "Bearer saved-test-key"
			if clear {
				want = ""
			}
			if got != want {
				t.Fatalf("authorization=%q want=%q", got, want)
			}
		})
	}
}
