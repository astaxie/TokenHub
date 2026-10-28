package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionGatewayBindsAuthenticatedCaller(t *testing.T) {
	server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	project, apiKey, err := server.authenticate(request)
	if err != nil {
		t.Fatal(err)
	}
	call, err := server.admitRoutedCall(httptest.NewRecorder(), request, project, apiKey, "classified-model", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	routed, err := server.prepareAdmittedRoutedCall(request.Context(), call, call.Model.Name)
	if err != nil {
		t.Fatal(err)
	}
	invocations := 0
	_, _, _, _, err = executeRoutedWithStore(request.Context(), store, routed, false, func(ctx context.Context, _ RouteSelection, _ bool, _ int) (bool, Usage, error) {
		invocations++
		if caller, _ := ctx.Value(subscriptionCallerKey{}).(string); caller != project.ID+"\x00"+apiKey.ID {
			t.Fatal("gateway did not bind the authenticated project and API key")
		}
		return true, Usage{}, nil
	})
	if err != nil || invocations != 1 {
		t.Fatalf("gateway invocation failed: calls=%d error=%v", invocations, err)
	}
	if caller := withSubscriptionCaller(context.Background(), "", "").Value(subscriptionCallerKey{}); caller != nil {
		t.Fatal("empty authentication produced a caller identity")
	}
}
