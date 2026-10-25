package server

import (
	"context"
	"time"
)

// Factory's opt-in standard-only policy must stop before inference can consume
// paid extra usage. A failed or incomplete quota read cannot authorize spending.
func (a *SubscriptionWorkerAdapter) checkStandardAllowance(ctx context.Context, process *subscriptionWorkerProcess, id string) error {
	options, _ := ctx.Value(subscriptionWorkerOptionsKey{}).(map[string]bool)
	if a.Definition.ID != "factory" || !options["standard_only"] {
		return nil
	}
	raw, err := process.invoke(ctx, a.command(id, "usage"), nil)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	quota, err := normalizeSubscriptionQuota(raw, now)
	if err != nil {
		return err
	}
	windows, _ := quota["windows"].([]map[string]any)
	if len(windows) == 0 {
		return NewHTTPError(502, "subscription_standard_quota_unavailable", "Factory Standard allowance could not be verified")
	}
	if quota["limit_reached"] != true {
		return nil
	}
	retry := time.Minute
	for _, window := range windows {
		used, _ := window["used_percent"].(float64)
		reset, _ := window["reset_after_seconds"].(int64)
		if used >= 100 && reset > 0 && reset <= 31*24*60*60 {
			retry = max(retry, time.Duration(reset)*time.Second)
		}
	}
	return &ProviderInvocationError{Err: NewHTTPError(429, "subscription_standard_quota_exhausted", "Factory Standard allowance is exhausted"), Disposition: ProviderErrorQuotaExhausted, RetryAfter: retry}
}
