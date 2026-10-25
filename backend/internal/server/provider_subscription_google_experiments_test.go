package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleSubscriptionGeminiExperimentsControlModelsAndWireAliases(t *testing.T) {
	for _, tc := range []struct {
		name            string
		flags           []int64
		preview         bool
		present, absent []string
		flash, lite     string
	}{
		{"default", nil, false, []string{"gemini-3.5-flash", "gemini-3.1-flash-lite", "gemini-2.5-pro", "gemini-2.5-flash"}, []string{"gemini-3-pro-preview", "gemini-3.8-flash"}, "gemini-3-flash", "gemini-3.1-flash-lite"},
		{"preview", nil, true, []string{"gemini-3-pro-preview", "gemini-3-flash-preview"}, []string{"gemini-3.1-pro-preview"}, "gemini-3-flash", "gemini-3.1-flash-lite"},
		{"latest", []int64{45760185, 45842815, 45827489}, true, []string{"gemini-3.1-pro-preview", "gemini-3.8-flash", "gemini-3.5-flash-lite"}, []string{"gemini-3-pro-preview", "gemini-3.5-flash", "gemini-3.1-flash-lite", "gemini-2.5-flash"}, "gemini-3.8-flash", "gemini-3.5-flash-lite"},
		{"without pro", []int64{45760185, 45768879}, true, []string{"gemini-3-flash-preview"}, []string{"gemini-2.5-pro", "gemini-3-pro-preview", "gemini-3.1-pro-preview"}, "gemini-3-flash", "gemini-3.1-flash-lite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var experiments, projects atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					projects.Add(1)
					return
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/v1internal:retrieveUserQuota":
					model := "gemini-3-flash"
					if tc.preview {
						model = "gemini-3-pro-preview"
					}
					writeJSON(w, 200, map[string]any{"buckets": []any{map[string]any{"modelId": model}}})
				case "/v1internal:listExperiments":
					experiments.Add(1)
					metadata, _ := body["metadata"].(map[string]any)
					if body["project"] != "project" || metadata["ideVersion"] != "0.61.0" || metadata["duetProject"] != "project" || metadata["pluginType"] != "GEMINI" {
						t.Error("experiment request omitted CLI account context")
					}
					flags := []any{}
					for _, id := range tc.flags {
						flags = append(flags, map[string]any{"flagId": id, "boolValue": true})
					}
					writeJSON(w, 200, map[string]any{"flags": flags})
				case "/v1internal:streamGenerateContent":
					requested := "gemini-3.5-flash"
					want := tc.flash
					if strings.Contains(r.Header.Get("User-Agent"), "flash-lite") {
						requested, want = "gemini-3.5-flash-lite", tc.lite
					}
					if body["model"] != want || !strings.Contains(r.Header.Get("User-Agent"), "/"+requested+" ") {
						t.Errorf("wire model or requested-model user agent incorrect: %v, %s", body["model"], r.Header.Get("User-Agent"))
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			a := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
			catalog, err := a.ModelsWithCredentials(context.Background(), ProviderResourceCredentials{AccessToken: "account", OrganizationID: "project"})
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, model := range catalog.Models {
				ids = append(ids, model.ID)
			}
			for _, id := range tc.present {
				if !slices.Contains(ids, id) {
					t.Errorf("offered model %s missing: %v", id, ids)
				}
			}
			for _, id := range tc.absent {
				if slices.Contains(ids, id) {
					t.Errorf("unavailable model %s offered: %v", id, ids)
				}
			}
			p := Provider{APIKey: "account", Options: map[string]string{"organization_id": "project"}}
			for _, model := range []string{"gemini-3.5-flash", "gemini-3.5-flash-lite"} {
				if _, _, err = a.Chat(context.Background(), p, model, ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if experiments.Load() != 1 || projects.Load() != 1 {
				t.Fatalf("account metadata not cached: experiments=%d projects=%d", experiments.Load(), projects.Load())
			}
		})
	}
}

func TestGoogleSubscriptionExperimentsCacheIsolatedAndOptional(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer enrolled" {
			writeJSON(w, 200, map[string]any{"flags": []any{map[string]any{"flagId": 45842815, "boolValue": true}}})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	a := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
	for _, account := range []string{"enrolled", "regular"} {
		for range 2 {
			flags, err := a.flags(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: account}, "project")
			if err != nil || flags.latestFlash != (account == "enrolled") {
				t.Fatalf("experiments crossed accounts or did not default safely: %+v %v", flags, err)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("optional experiments were not cached: %d", calls.Load())
	}
	a.accountState.mu.Lock()
	for key, flags := range a.accountState.flags {
		flags.at = time.Now().Add(-2 * time.Hour)
		a.accountState.flags[key] = flags
	}
	a.accountState.mu.Unlock()
	if _, err := a.flags(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: "enrolled"}, "project"); err != nil || calls.Load() != 3 {
		t.Fatal("expired experiments were not refreshed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.flags(ctx, Provider{}, ProviderResourceCredentials{AccessToken: "uncached"}, "project"); err != context.Canceled {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestGoogleSubscriptionSuppliedProjectStillLoadsTiersAndOnboards(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			var onboarded atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/v1internal:loadCodeAssist":
					if providerType == providerAntigravitySubscription {
						writeJSON(w, 200, map[string]any{"currentTier": map[string]any{"id": "free-tier"}, "paidTier": map[string]any{"id": "paid-tier"}, "cloudaicompanionProject": "other"})
						return
					}
					metadata, _ := body["metadata"].(map[string]any)
					if body["cloudaicompanionProject"] != "supplied" || metadata["duetProject"] != "supplied" {
						t.Error("supplied project was not used in bootstrap")
					}
					writeJSON(w, 200, map[string]any{"allowedTiers": []any{map[string]any{"id": "standard-tier", "isDefault": true, "userDefinedCloudaicompanionProject": true}}})
				case "/v1internal:onboardUser":
					onboarded.Add(1)
					if body["tierId"] != "standard-tier" || body["cloudaicompanionProject"] != "supplied" {
						t.Error("supplied project was not onboarded to its eligible tier")
					}
					writeJSON(w, 200, map[string]any{"done": true, "response": map[string]any{}})
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
				}
			}))
			defer server.Close()
			a := googleSubscriptionTestAdapter(server, providerType)
			project, err := a.project(context.Background(), Provider{}, ProviderResourceCredentials{AccessToken: "account", OrganizationID: "supplied"})
			if err != nil || project.ID != "supplied" {
				t.Fatalf("supplied project lost: %+v %v", project, err)
			}
			if providerType == providerAntigravitySubscription && (project.PaidTier != "paid-tier" || onboarded.Load() != 0) {
				t.Fatal("Antigravity did not load subscription tiers")
			}
			if providerType == providerGeminiCLISubscription && (project.Tier != "standard-tier" || onboarded.Load() != 1) {
				t.Fatal("Gemini did not onboard its supplied project")
			}
		})
	}
}
