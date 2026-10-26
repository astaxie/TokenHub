package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCodexQuotaRejectsLateAccountReplacementSnapshot(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	provider := store.AddProvider(Provider{Name: "Codex quota replacement", Type: ProviderOpenAICodex, Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex account", ResourceType: ProviderResourceOpenAISubscription,
		Credentials: &ProviderResourceCredentials{AccessToken: "previous-access", AccountID: "previous-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer previous-access" || r.Header.Get("Chatgpt-Account-Id") != "previous-account" {
			t.Error("quota request used unexpected account credentials")
		}
		close(started)
		<-release
		_, _ = fmt.Fprint(w, `{"plan_type":"previous-plan","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100}}}`)
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(unblock)
	adapter := mustCodexSubscriptionAdapterForTest(t, server)
	adapter.Client, adapter.QuotaURL = upstream.Client(), upstream.URL
	result := make(chan responseBody, 1)
	go func() {
		result <- doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+ProviderOpenAICodex+"/quota.read",
			map[string]any{"resource_id": resource.ID, "resource_type": resource.ResourceType, "refresh": true}, "dev_admin_token")
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("quota request did not start")
	}
	replaced := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID,
		map[string]any{"credentials": map[string]any{"replace": true, "access_token": "replacement-access", "account_id": "replacement-account"}}, "dev_admin_token")
	if replaced.Code != http.StatusOK {
		t.Fatalf("credential replacement failed: %s", replaced.Body)
	}
	unblock()
	select {
	case response := <-result:
		if quota, cached := server.cachedOpenAIAccountQuota(resource.ID, 0); cached {
			t.Fatalf("late native response cached the previous account quota: status=%d quota=%+v", response.Code, quota)
		}
		if response.Code != http.StatusConflict {
			t.Fatalf("late quota request status = %d, want 409: %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quota request did not finish")
	}
}
