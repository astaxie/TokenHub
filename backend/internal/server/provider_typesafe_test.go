package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTypeSafeAdapterNativeRequestAndUsage(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer synthetic-typesafe-key" {
			t.Errorf("unexpected method, path, or managed auth: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Tenant") != "test-tenant" {
			t.Error("custom header missing")
		}
		var req SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Model != "jev-latest" || !strings.Contains(string(req.State), "9007199254740993") || len(req.Questions) != 3 {
			t.Errorf("request model or payload changed: %+v", req)
		}
		w.Header().Set("X-Typesafe-Request-Id", "typesafe-request-123")
		w.Header().Set("X-Request-Id", "generic-request-456")
		writeFixture(t, w, systemOneFixtureResponse)
	}))
	defer upstream.Close()
	adapter := TypeSafeAdapter{Client: upstream.Client()}
	response, usage, err := adapter.SystemOne(context.Background(), Provider{BaseURL: upstream.URL + "/v1", APIKey: "synthetic-typesafe-key", Headers: map[string]string{"Authorization": "must-be-overridden", "X-Tenant": "test-tenant"}}, "jev-latest", systemOneTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "jev-1.13.0" || usage.TotalTokens != 491 || usage.UpstreamRequestID != "typesafe-request-123" || calls.Load() != 1 {
		t.Fatalf("response=%+v usage=%+v calls=%d", response, usage, calls.Load())
	}
}

func TestTypeSafeAdapterDiscoveryAndProbeUseNativeModels(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Errorf("probe must only fetch models: %s %s", r.Method, r.URL.Path)
		}
		writeFixture(t, w, `{"models":[{"name":"jev-latest","description":"Current stable model","release_date":"2026-08-12"},{"name":"jev-preview"}]}`)
	}))
	defer upstream.Close()
	adapter := TypeSafeAdapter{Client: upstream.Client()}
	provider := Provider{Type: providerTypeSafe, BaseURL: upstream.URL + "/v1", APIKey: "synthetic-key"}
	entry, err := adapter.DiscoverModels(context.Background(), ProviderCreateRequest{Type: providerTypeSafe, BaseURL: provider.BaseURL, APIKey: provider.APIKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Models) != 2 || entry.Models[0].Type != "decision" || strings.Join(entry.Models[0].Capabilities, ",") != "systemone" {
		t.Fatalf("entry=%+v", entry)
	}
	if _, err := adapter.ProbeProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Probe(context.Background(), provider, ProviderResource{}, adapter.DefaultProbeRequest()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestTypeSafeAdapterErrorsAndCancellation(t *testing.T) {
	tests := []struct {
		status       int
		disposition  ProviderErrorDisposition
		clientStatus int
	}{
		{422, ProviderErrorClient, 422}, {429, ProviderErrorTransientSame, 429}, {529, ProviderErrorTransientSame, 502}, {401, ProviderErrorAuthBroken, 502},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				writeFixture(t, w, `{"error":{"message":"rejected synthetic-typesafe-key"}}`)
			}))
			defer upstream.Close()
			_, _, err := (TypeSafeAdapter{Client: upstream.Client()}).SystemOne(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "synthetic-typesafe-key"}, "jev-latest", systemOneTestRequest(t))
			if err == nil || providerErrorDisposition(err) != tt.disposition || AsHTTPError(err).Status != tt.clientStatus || strings.Contains(err.Error(), "synthetic-typesafe-key") {
				t.Fatalf("error=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := (TypeSafeAdapter{Client: &http.Client{Timeout: time.Second}}).SystemOne(ctx, Provider{BaseURL: "https://api.typesafe.ai/v1", APIKey: "synthetic-key"}, "jev-latest", systemOneTestRequest(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestTypeSafeAdapterRejectsRedirectAndMalformedResponse(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, body := range []string{"redirect", `{"model":"jev-latest","answers":{},"usage":{}}`, strings.Repeat("x", (8<<20)+1)} {
		t.Run(body[:min(len(body), 16)], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "redirect" {
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
					return
				}
				writeFixture(t, w, body)
			}))
			defer upstream.Close()
			_, _, err := (TypeSafeAdapter{Client: upstream.Client()}).SystemOne(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "synthetic-key"}, "jev-latest", systemOneTestRequest(t))
			if err == nil {
				t.Fatal("invalid upstream response accepted")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("provider credential request followed a redirect")
	}
}
