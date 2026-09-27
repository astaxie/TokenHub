package server

import (
	"net/http"
	"testing"
)

func TestMediaDirectTimeoutDoesNotResubmit(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/audio/speech"} {
		for _, status := range []int{http.StatusRequestTimeout, http.StatusGatewayTimeout, http.StatusTooManyRequests, http.StatusUnauthorized} {
			t.Run(path+"/"+http.StatusText(status), func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.WriteHeader(status)
				}, "image")
				store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				response := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{"model": "public-media", "prompt": "fixture", "input": "fixture"}, key)
				wantCalls := 1
				if status == http.StatusTooManyRequests || status == http.StatusUnauthorized {
					wantCalls = 2
				}
				if response.Code < http.StatusBadRequest || calls != wantCalls {
					t.Fatalf("status=%d calls=%d, want failure after %d attempts: %s", response.Code, calls, wantCalls, response.Body)
				}
			})
		}
	}
}
