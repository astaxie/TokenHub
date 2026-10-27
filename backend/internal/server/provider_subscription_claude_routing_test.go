package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func claudeSubscriptionRoutingFixture(t *testing.T) (*Server, *ClaudeSubscriptionAdapter, []RouteSelection, CallContext) {
	t.Helper()
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	adapter, ok := resolveTypedAdapter[*ClaudeSubscriptionAdapter](server.adapterRegistry, providerClaudeSubscription)
	if !ok {
		t.Fatal("Claude adapter is unavailable")
	}
	adapter.Executable = os.Args[0]
	adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSubscriptionHelperProcess$", "--", "tokenhub-claude-helper"}
	adapter.StreamIdleTimeout, adapter.ParkTimeout = 3*time.Second, 3*time.Second
	store.AddModel(Model{Name: "public-model", Modality: "chat", Status: StatusActive})
	var routes []RouteSelection
	for _, suffix := range []string{"a", "b"} {
		provider := store.AddProvider(Provider{ID: "provider-" + suffix, Name: "Account " + suffix, Type: providerClaudeSubscription, Status: StatusActive, Healthy: true})
		resource, err := store.AddProviderResource(ProviderResource{ID: "account-" + suffix, ProviderID: provider.ID, Name: "Account " + suffix, ResourceType: providerResourceClaudeSubscription, Status: StatusActive, Healthy: true, Credentials: &ProviderResourceCredentials{AccessToken: "fixture-token"}})
		if err != nil {
			t.Fatal(err)
		}
		provider.Options = map[string]string{"resource_id": resource.ID}
		route := store.AddRoute(ModelRoute{ID: "route-" + suffix, ProviderID: provider.ID, ProviderResourceID: resource.ID, ModelName: "public-model", ProviderModel: "tools", Weight: 100, Status: StatusActive})
		routes = append(routes, RouteSelection{Provider: provider, Resource: &resource, ProviderModel: "tools", Route: route})
	}
	call := CallContext{RequestID: "request-0", Project: Project{ID: "project-a"}, Key: APIKey{ID: "key-a"}, Model: Model{Name: "public-model"}, StartedAt: time.Now()}
	return server, adapter, routes, call
}

func TestClaudeSubscriptionContinuationFollowsParkedAccount(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			server, adapter, routes, call := claudeSubscriptionRoutingFixture(t)
			firstOrder := server.planRouteOrder(call, routes)
			nextOrder := firstOrder
			for i := 1; i < 100 && nextOrder[0].Resource.ID == firstOrder[0].Resource.ID; i++ {
				call.RequestID = fmt.Sprintf("request-%d", i)
				nextOrder = server.planRouteOrder(call, routes)
			}
			if nextOrder[0].Resource.ID == firstOrder[0].Resource.ID {
				t.Fatal("fixture did not select a different weighted route")
			}
			request := claudeSubscriptionToolRequest()
			request.Model = call.Model.Name
			ctx := withSubscriptionCaller(context.Background(), call.Project.ID, call.Key.ID)
			first, _, err := adapter.Chat(ctx, firstOrder[0].Provider, "tools", request)
			if err != nil {
				t.Fatal(err)
			}
			next := claudeSubscriptionContinue(t, request, first)
			next.Stream = stream
			routed, err := server.compatibleChatRoutes(RoutedCall{Call: call, Routes: nextOrder}, next)
			if err != nil || len(routed.Routes) != 1 || routeResourceID(routed.Routes[0]) != routeResourceID(firstOrder[0]) {
				t.Fatalf("continuation did not bind its parked account: routes=%+v error=%v", routed.Routes, err)
			}
			_, _, usage, attempts, err := executeRoutedWithStore(context.Background(), server.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
				if stream {
					usage, err := adapter.ChatStream(ctx, route.Provider, route.ProviderModel, next, io.Discard)
					return nil, usage, err
				}
				return adapter.Chat(ctx, route.Provider, route.ProviderModel, next)
			})
			if err != nil || len(attempts) != 1 || usage.CompletionTokens != 2 {
				t.Fatalf("valid continuation failed: attempts=%d usage=%+v error=%v", len(attempts), usage, err)
			}
		})
	}
}

func TestClaudeSubscriptionContinuationBindingPreservesAuthorization(t *testing.T) {
	server, adapter, routes, call := claudeSubscriptionRoutingFixture(t)
	ctx := withSubscriptionCaller(context.Background(), call.Project.ID, call.Key.ID)
	request := claudeSubscriptionToolRequest()
	request.Model = call.Model.Name
	first, _, err := adapter.Chat(ctx, routes[0].Provider, "tools", request)
	if err != nil {
		t.Fatal(err)
	}
	next := claudeSubscriptionContinue(t, request, first)
	ids := claudeSubscriptionChatResultIDs(next)
	for _, scenario := range []string{"other project", "other key", "missing identity", "excluded account", "different model", "different provider", "mixed tool results"} {
		t.Run(scenario, func(t *testing.T) {
			routed := RoutedCall{Call: call, Routes: append([]RouteSelection(nil), routes...)}
			results := append([]string(nil), ids...)
			switch scenario {
			case "other project":
				routed.Call.Project.ID = "other-project"
			case "other key":
				routed.Call.Key.ID = "other-key"
			case "missing identity":
				routed.Call.Key.ID = ""
			case "excluded account":
				routed.Routes = routed.Routes[1:]
			case "different model":
				routed.Routes[0].ProviderModel = "other-model"
			case "different provider":
				routed.Routes[0].Provider.ID = "other-provider"
			case "mixed tool results":
				results = append(results, "call_foreign")
			}
			_, err := server.bindClaudeSubscriptionContinuation(routed, results)
			if failure := AsHTTPError(err); failure == nil || failure.Status != http.StatusBadRequest || failure.Code != "claude_subscription_continuation_invalid" {
				t.Fatalf("invalid continuation changed routing: %v", err)
			}
		})
	}
	for _, ids := range [][]string{nil, {"unrelated-provider-call"}} {
		routed, err := server.bindClaudeSubscriptionContinuation(RoutedCall{Call: call, Routes: routes}, ids)
		if err != nil || len(routed.Routes) != len(routes) {
			t.Fatalf("ordinary request routing changed: %v", err)
		}
	}
	// Failed routing checks must not consume a valid result or broaden access.
	if _, _, err := adapter.Chat(ctx, routes[0].Provider, "tools", next); err != nil {
		t.Fatalf("routing checks consumed the continuation: %v", err)
	}
}

func TestClaudeSubscriptionAnthropicContinuationBindsParkedAccount(t *testing.T) {
	server, adapter, routes, call := claudeSubscriptionRoutingFixture(t)
	ctx := withSubscriptionCaller(context.Background(), call.Project.ID, call.Key.ID)
	request := claudeSubscriptionToolRequest()
	first, _, err := adapter.Chat(ctx, routes[1].Provider, "tools", request)
	if err != nil {
		t.Fatal(err)
	}
	ids := claudeSubscriptionChatResultIDs(claudeSubscriptionContinue(t, request, first))
	input := anthropicMessagesRequest{Model: call.Model.Name, Raw: map[string]any{}, Messages: []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": ids[0], "content": "sunny"}}}}}
	routed, err := server.compatibleAnthropicRoutes(RoutedCall{Call: call, Routes: routes}, input)
	if err != nil || len(routed.Routes) != 1 || routeResourceID(routed.Routes[0]) != routeResourceID(routes[1]) {
		t.Fatalf("Messages continuation did not bind its account: routes=%+v error=%v", routed.Routes, err)
	}
}

func TestClaudeSubscriptionContinuationCannotRestoreDisabledAccount(t *testing.T) {
	server, adapter, routes, call := claudeSubscriptionRoutingFixture(t)
	ctx := withSubscriptionCaller(context.Background(), call.Project.ID, call.Key.ID)
	request := claudeSubscriptionToolRequest()
	request.Model = call.Model.Name
	first, _, err := adapter.Chat(ctx, routes[0].Provider, "tools", request)
	if err != nil {
		t.Fatal(err)
	}
	next := claudeSubscriptionContinue(t, request, first)
	if _, err := server.store.UpdateProviderResource(routeResourceID(routes[0]), ProviderResource{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	routed, err := server.prepareAdmittedRoutedCall(context.Background(), call, call.Model.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(routed.Routes) != 1 || routeResourceID(routed.Routes[0]) != routeResourceID(routes[1]) {
		t.Fatal("disabled account was not removed from eligible routes")
	}
	_, err = server.compatibleChatRoutes(routed, next)
	if failure := AsHTTPError(err); failure == nil || failure.Code != "claude_subscription_continuation_invalid" {
		t.Fatalf("continuation restored a disabled account: %v", err)
	}
}
