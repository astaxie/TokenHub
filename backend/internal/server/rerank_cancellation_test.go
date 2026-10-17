package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankCancellationSanitizationPreservesNeutralOutcome(t *testing.T) {
	for _, original := range []error{
		context.Canceled,
		&url.Error{Op: "Post", URL: "https://example.test/private-query", Err: context.Canceled},
		&ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient},
		&ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorEgress},
		&ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorStreamCommitted},
	} {
		sanitized := sanitizeRerankProviderError(fmt.Errorf("private-query: %w", original))
		// A routed call can sanitize the same error at both hook and adapter boundaries.
		sanitized = sanitizeRerankProviderError(sanitized)
		if !errors.Is(sanitized, context.Canceled) || providerErrorDisposition(sanitized) != ProviderErrorClient || providerAttemptOutcome(sanitized) != AttemptNeutral || shouldFailoverRoutedError(sanitized, false) {
			t.Errorf("cancellation lost its neutral classification: original=%T sanitized=%v disposition=%q outcome=%v", original, sanitized, providerErrorDisposition(sanitized), providerAttemptOutcome(sanitized))
		}
		if strings.Contains(sanitized.Error(), "private-query") {
			t.Errorf("cancellation retained private content: %v", sanitized)
		}
	}
}

func TestRerankProviderHookCancellationDoesNotPenalizeResource(t *testing.T) {
	for _, callerCanceled := range []bool{false, true} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("caller_canceled=%t/wrapped=%t", callerCanceled, wrapped), func(t *testing.T) {
				app, store, key := newRerankCompletionFixture(t, func(http.ResponseWriter, *http.Request) {
					t.Error("canceled provider hook reached the native adapter")
				})
				store.failureThreshold = 2
				for index := range 2 {
					_, err := store.AddProviderResource(ProviderResource{
						ID: fmt.Sprintf("rerank-cancel-%d", index), Name: fmt.Sprintf("Rerank cancellation %d", index),
						ProviderID: "rank-complete", Status: StatusActive, Healthy: true, Priority: index + 1, Weight: 100,
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				// Cancellation must neither clear an existing failure nor trip the breaker.
				store.FinishProviderResourceAttempt(context.Background(), "rerank-cancel-0", "", AttemptFailed, Usage{})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				hook := pluginmeta.GatewayHookDescriptor{
					PluginID: "test.rerank-cancel", HookID: "cancel", Stage: pluginmeta.StageProviderCall,
					Priority: 2000, FailurePolicy: pluginmeta.FailurePolicyFailClosed,
					Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolRerank}},
				}
				if err := app.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				calls := 0
				if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(hookCtx context.Context, _ pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					calls++
					if callerCanceled {
						cancel()
					}
					if errors.Is(hookCtx.Err(), context.Canceled) != callerCanceled {
						t.Fatal("fixture did not establish the requested caller cancellation state")
					}
					var failure error = context.Canceled
					if wrapped {
						failure = &ProviderInvocationError{Err: fmt.Errorf("private-query: %w", failure), Disposition: ProviderErrorClient}
					}
					return pluginmeta.GatewayHookResult{}, failure
				})); err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"rank-complete","query":"fixture","documents":["document"]}`)).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer "+key)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				app.Handler().ServeHTTP(response, request)
				if calls != 1 || response.Code < http.StatusBadRequest || strings.Contains(response.Body.String(), "private-query") {
					t.Fatalf("cancellation was retried, accepted, or exposed content: calls=%d status=%d body=%s", calls, response.Code, response.Body)
				}
				primary := findResource(t, store, "rerank-cancel-0")
				if primary.FailureCount != 1 || !primary.Healthy || primary.CooldownUntil != nil {
					t.Errorf("cancellation changed primary resource health: failures=%d healthy=%t cooldown=%v", primary.FailureCount, primary.Healthy, primary.CooldownUntil)
				}
				if failures := resourceFailureCount(t, store, "rerank-cancel-1"); failures != 0 {
					t.Errorf("cancellation penalized the fallback resource: failures=%d", failures)
				}
			})
		}
	}
}

func TestRerankCancellationDoesNotChangeHalfOpenResourceHealth(t *testing.T) {
	store, app, resourceID := newProbeRecoveryServer(t, nil, nil)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	tripBreaker(t, store, resourceID)
	expireCooldown(t, store, resourceID)
	leaseID, claimCtx, err := store.CheckProviderResourceCapacity(context.Background(), resourceID)
	if err != nil || !hasHalfOpenClaim(claimCtx) {
		t.Fatalf("half-open trial was not admitted: %v", err)
	}
	before := findResource(t, store, resourceID)
	err = sanitizeRerankProviderError(&ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient})
	finishProviderResourceAttempt(claimCtx, store, resourceID, leaseID, err, Usage{})
	after := findResource(t, store, resourceID)
	if after.Healthy || after.FailureCount != before.FailureCount || after.CooldownUntil == nil || !after.CooldownUntil.Equal(*before.CooldownUntil) {
		t.Fatalf("cancellation changed half-open health: failures=%d want=%d healthy=%t cooldown=%v want=%v", after.FailureCount, before.FailureCount, after.Healthy, after.CooldownUntil, before.CooldownUntil)
	}
}
