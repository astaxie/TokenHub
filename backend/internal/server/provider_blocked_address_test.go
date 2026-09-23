package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func blockedAddressFixture() error {
	return newProviderBlockedAddressError([]net.IPAddr{
		{IP: net.ParseIP("198.18.0.81")}, {IP: net.ParseIP("::ffff:198.18.0.81")}, {IP: net.ParseIP("fd00::2")},
	})
}

func TestBlockedAddressWireErrorIncludesNormalizedIPsWithoutRequestSecrets(t *testing.T) {
	cause := &url.Error{Op: "Post", URL: "https://private.example/v1?key=secret-token", Err: blockedAddressFixture()}
	for _, catalog := range []bool{false, true} {
		var err error = cause
		code := "provider_address_blocked"
		if catalog {
			err = providerCatalogConnectionError(cause)
			code = "provider_models_address_blocked"
		}
		w := httptest.NewRecorder()
		writeError(w, httptest.NewRequest(http.MethodGet, "/", nil), err)
		var body struct {
			Error struct {
				Code    string
				Message string
				Details struct {
					IPs []string `json:"blocked_ips"`
				}
			}
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if w.Code != 502 || body.Error.Code != code || !reflect.DeepEqual(body.Error.Details.IPs, []string{"198.18.0.81", "fd00::2"}) {
			t.Fatalf("unexpected response: %s", w.Body)
		}
		if strings.Contains(w.Body.String(), "secret-token") || strings.Contains(w.Body.String(), "private.example") {
			t.Fatalf("request data leaked: %s", w.Body)
		}
	}
}

type blockedPlaygroundAdapter struct{ MockAdapter }

func (blockedPlaygroundAdapter) Chat(context.Context, Provider, string, ChatCompletionRequest) (any, Usage, error) {
	return nil, Usage{}, blockedAddressFixture()
}
func (blockedPlaygroundAdapter) ChatStream(context.Context, Provider, string, ChatCompletionRequest, io.Writer) (Usage, error) {
	return Usage{}, blockedAddressFixture()
}

func TestPlaygroundBlockedAddressIncludesConfigurationDiagnostics(t *testing.T) {
	server, _ := newPlaygroundTestServer(t)
	server.adapterRegistry.Register(ProviderMock, blockedPlaygroundAdapter{}, AdapterCapabilityChat)
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat/stream", map[string]any{
		"project_id": "prj_demo", "model": "gpt-4.1-mini", "messages": []map[string]any{{"role": "user", "content": "diagnostic test"}},
	}, "")
	failed := findPlaygroundSSEEvent(t, parsePlaygroundSSE(t, response.Body), "playground.failed")
	if failed.Data["code"] != "provider_address_blocked" {
		t.Fatalf("unexpected failure: %#v", failed.Data)
	}
	details, ok := failed.Data["error_details"].(map[string]any)
	if !ok || !reflect.DeepEqual(details["blocked_ips"], []any{"198.18.0.81", "fd00::2"}) {
		t.Fatalf("missing IP details: %#v", failed.Data)
	}
	if !strings.Contains(failed.Data["error"].(string), "System Settings") {
		t.Fatalf("missing configuration guidance: %#v", failed.Data)
	}
}
