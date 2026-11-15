package server

import (
	"context"
	"time"
)

func executeRoutedWithStore[T any](
	ctx context.Context,
	store Store,
	routed RoutedCall,
	allowReasoningEffortFallback bool,
	// call receives the 1-based attempt number, counted across every candidate
	// including ones that never ran because capacity acquisition failed. Callbacks
	// must not derive it locally: those failures are appended to attempts here
	// without invoking the callback, so a local counter would undercount them.
	call func(context.Context, RouteSelection, bool, int) (T, Usage, error),
) (T, RouteSelection, Usage, []RouteAttempt, error) {
	callerCtx := ctx
	ctx = withSubscriptionCaller(ctx, routed.Call.Project.ID, routed.Call.Key.ID)
	var zero T
	var lastErr error = ErrProviderMissing
	var cumulativeTokens int64
	var affinityBindings map[string]AdapterSessionBinding
	var err error
	routed, affinityBindings, err = applyAdapterSessionAffinity(ctx, store, routed)
	if err != nil {
		return zero, RouteSelection{}, Usage{}, nil, classifyCallerCancellation(callerCtx, ctx, err)
	}
	attempts := make([]RouteAttempt, 0, len(routed.Routes)+1)
	for _, route := range routed.Routes {
		if route.AllowanceHold != nil {
			return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, route.AllowanceHold
		}
		if leaseErr := coordinationLeaseError(ctx); leaseErr != nil {
			return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, leaseErr
		}
		resourceID := routeResourceID(route)
		binding, hasBinding := affinityBindings[route.Provider.ID]
		routeIsBound := hasBinding && binding.ResourceID == resourceID
		leaseID, leaseCtx, err := store.CheckProviderResourceCapacity(ctx, resourceID)
		if err != nil {
			err = classifyCallerCancellation(callerCtx, ctx, err)
			status, code := statusAndCode(err)
			attempts = append(attempts, RouteAttempt{
				Selection:      route,
				Status:         status,
				UpstreamStatus: AsHTTPError(err).upstreamStatusOrZero(),
				ErrorCode:      code,
				Error:          errorMessage(err),
			})
			lastErr = err
			if !shouldFailoverRoutedError(err, routeIsBound) {
				return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, err
			}
			continue
		}
		omitReasoningEffort := false
		recordAccountRoutingOutcome(leaseCtx, store, route, false)
		for {
			// The attempt window covers the whole routed callback, including stream
			// translation and writing to the client: a slow reader blocks the
			// gateway here, and that backpressure is part of the attempt duration,
			// not of the gateway overhead reported in overhead_seconds.
			attemptStartedAt := time.Now()
			if recorder, ok := store.(interface {
				PrepareMeteringAttempt(string, int, RouteSelection, time.Time) (RouteSelection, error)
			}); ok {
				prepared, prepareErr := recorder.PrepareMeteringAttempt(routed.Call.RequestID, len(attempts)+1, route, attemptStartedAt.UTC())
				if prepareErr != nil {
					store.ReleaseProviderResourceCapacity(resourceID, leaseID)
					return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, classifyCallerCancellation(callerCtx, leaseCtx, prepareErr)
				}
				route = prepared
			}
			resp, usage, err := call(leaseCtx, route, omitReasoningEffort, len(attempts)+1)
			usage, err = classifyMediaAttemptFailure(routed.Call, usage, err)
			cumulativeTokens = saturatingAddNonNegative(cumulativeTokens, retrievalAttemptQuotaTokens(routed.Call, usage))
			usage.RateLimitTokens = cumulativeTokens
			attemptEndedAt := time.Now()
			latencyMS := maxInt64(1, attemptEndedAt.Sub(attemptStartedAt).Milliseconds())
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			err = classifyCallerCancellation(callerCtx, leaseCtx, err)
			// Neither a committed stream nor a client disconnect may be retried,
			// not even via the effort fallback on the same route. These checks are
			// load-bearing: ProviderInvocationError implements Unwrap, so
			// isReasoningEffortRejection sees through the wrapper and would
			// otherwise return true for an error that must not be retried. The
			// disconnect is excluded directly rather than through the client
			// disposition, which an effort rejection now also carries: refusing a
			// bad parameter is a client error, and dropping the effort is exactly
			// how the gateway fixes it.
			disposition := providerErrorDisposition(err)
			if disposition == ProviderErrorQuotaExhausted {
				recordAccountRoutingOutcome(leaseCtx, store, route, true)
				recordAllowanceResetIntent(leaseCtx, store, routed.Call, route, routed.Routes)
			}
			retryWithoutEffort := allowReasoningEffortFallback &&
				!omitReasoningEffort &&
				disposition != ProviderErrorStreamCommitted &&
				disposition != ProviderErrorOutcomeUnknown &&
				!clientDisconnected(leaseCtx, err) &&
				isReasoningEffortRejection(err)
			if !retryWithoutEffort {
				finishProviderResourceAttempt(leaseCtx, store, resourceID, leaseID, err, usage)
			}
			status, code := routeAttemptStatusAndCode(err, retryWithoutEffort)
			attempts = append(attempts, RouteAttempt{
				Selection:      route,
				Status:         status,
				UpstreamStatus: AsHTTPError(err).upstreamStatusOrZero(),
				ErrorCode:      code,
				Error:          errorMessage(err),
				Invoked:        true,
				LatencyMS:      latencyMS,
				// Priced per attempt so a failover reports what each candidate cost
				// rather than attributing the whole request to the winner. Tokens
				// burned by an attempt that later failed were still billed.
				Usage:     priceUsageAt(routed.Call.Model, usage, routed.Call.StartedAt),
				StartedAt: attemptStartedAt.UTC(),
				EndedAt:   attemptEndedAt.UTC(),
			})
			if err == nil {
				rebindReason := ""
				if binding, ok := affinityBindings[route.Provider.ID]; ok && binding.ResourceID != resourceID {
					rebindReason = "resource_failover"
				}
				if bindErr := commitAdapterSessionAffinity(ctx, store, routed, affinityBindings, route, rebindReason); bindErr != nil {
					return zero, route, usage, attempts, bindErr
				}
				return resp, route, usage, attempts, nil
			}
			lastErr = err
			if retryWithoutEffort {
				if retryErr := store.CheckProviderResourceRetryCapacity(leaseCtx, resourceID, leaseID); retryErr != nil {
					retryErr = classifyCallerCancellation(callerCtx, leaseCtx, retryErr)
					store.ReleaseProviderResourceCapacity(resourceID, leaseID)
					status, code = statusAndCode(retryErr)
					attempts = append(attempts, RouteAttempt{
						Selection:      route,
						Status:         status,
						UpstreamStatus: AsHTTPError(retryErr).upstreamStatusOrZero(),
						ErrorCode:      code,
						Error:          errorMessage(retryErr),
					})
					lastErr = retryErr
					if !shouldFailoverRoutedError(retryErr, routeIsBound) {
						return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, retryErr
					}
					break
				}
				omitReasoningEffort = true
				continue
			}
			if !shouldFailoverRoutedError(err, routeIsBound) {
				return zero, route, usage, attempts, err
			}
			break
		}
	}
	return zero, RouteSelection{}, Usage{RateLimitTokens: cumulativeTokens}, attempts, lastErr
}

// clientDisconnected reports that there is no longer anyone to answer. The
// context is consulted as well as the error because classifyStreamError marks a
// mid-stream disconnect by its disposition without the error itself carrying a
// context.Canceled chain.
