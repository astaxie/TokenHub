package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJevRoutesChatStoreToGeminiForExistingInventory(t *testing.T) {
	server, _, _, _ := jevFixture(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if value, ok := body["store"]; !ok || value != false {
			t.Errorf("store not forwarded: %v", body)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer upstream.Close()
	if err := server.store.(*GormStore).db.Model(&Provider{}).Where("id IN ?", []string{"provider_0", "provider_1", "provider_2"}).Updates(map[string]any{"type": ProviderGemini, "base_url": upstream.URL + "/v1beta"}).Error; err != nil {
		t.Fatal(err)
	}
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		return semanticDecision{Choice: "choice_0", Confidence: .9}, nil
	})
	body := jevHTTPBody(false, false)
	delete(body, "tools")
	body["store"] = false
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", body, "thk_semantic_test")
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("Gemini store request failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}
