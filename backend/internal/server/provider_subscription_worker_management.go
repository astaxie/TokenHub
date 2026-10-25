package server

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

type subscriptionWorkerOptionsKey struct{}

func withSubscriptionWorkerOptions(ctx context.Context, id string, provider Provider, resource *ProviderResource) (context.Context, error) {
	keys := map[string][]string{"grok": {"x_search"}, "factory": {"standard_only"}, "commandcode-plan": {"zdr"}}
	options := map[string]bool{}
	for _, key := range keys[id] {
		value := provider.Options[key]
		if resource != nil {
			if override, ok := resource.Options[key]; ok {
				value = override
			}
		}
		if value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, NewHTTPError(400, "subscription_option_invalid", "Subscription options must be boolean values")
			}
			options[key] = parsed
		}
	}
	return context.WithValue(ctx, subscriptionWorkerOptionsKey{}, options), nil
}

func (a *SubscriptionWorkerAdapter) accountManagement(ctx context.Context, provider Provider, resource ProviderResource, operation string) (map[string]any, error) {
	var result map[string]any
	err := a.withAccountLease(ctx, provider, &resource, func(leaseCtx context.Context) error {
		process, release, id, err := a.account(leaseCtx, provider, &resource)
		if err != nil {
			return err
		}
		defer release()
		secrets := &subscriptionWorkerSecrets{}
		leaseCtx = context.WithValue(leaseCtx, subscriptionWorkerSecretsKey{}, secrets)
		raw, err := process.invoke(leaseCtx, a.command(id, operation), nil)
		if err != nil {
			return err
		}
		if operation == "usage" {
			result, err = normalizeSubscriptionQuota(raw, time.Now().UTC())
			if err == nil {
				redactSubscriptionQuotaText(result, secrets.provider())
			}
		} else {
			result, err = normalizeSubscriptionCheckin(raw)
		}
		return err
	})
	return result, err
}

// Parse the trusted quota schema before redacting display text. Credential
// values can match structural keys or timestamp components; redacting them
// before parsing would corrupt otherwise valid quota information.
func redactSubscriptionQuotaText(quota map[string]any, provider Provider) {
	secrets := providerErrorSecretValues(provider)
	redact := func(value string) string {
		masked, _ := redactProviderJSONValue(value, secrets, nil)
		return masked.(string)
	}
	field := func(object map[string]any, key string) {
		if value, ok := object[key].(string); ok {
			object[key] = redact(value)
		}
	}
	field(quota, "plan_type")
	if windows, ok := quota["windows"].([]map[string]any); ok {
		for _, window := range windows {
			field(window, "label")
		}
	}
	if limits, ok := quota["additional_rate_limits"].([]map[string]any); ok {
		for _, limit := range limits {
			field(limit, "limit_name")
			field(limit, "metered_feature")
			if rate, ok := limit["rate_limit"].(map[string]any); ok {
				if window, ok := rate["primary_window"].(map[string]any); ok {
					field(window, "label")
				}
			}
		}
	}
	if metrics, ok := quota["metrics"].([]map[string]string); ok {
		for _, metric := range metrics {
			metric["label"], metric["value"] = redact(metric["label"]), redact(metric["value"])
		}
	}
}

type subscriptionQuotaResult struct {
	Plan, Balance, Until, Error, SignIn string
	Windows                             []struct {
		Name, Display, ResetsAt string
		Used                    *float64
		Span                    float64
		Aside                   bool
		Models                  []string
		NotModels               []string
	}
}

func normalizeSubscriptionQuota(raw []byte, now time.Time) (map[string]any, error) {
	var source subscriptionQuotaResult
	if len(raw) > 1<<20 || json.Unmarshal(raw, &source) != nil || len(source.Windows) > 256 {
		return nil, NewHTTPError(502, "subscription_quota_invalid", "Subscription runtime returned invalid quota information")
	}
	if source.SignIn == "expired" {
		return nil, &ProviderInvocationError{Err: NewHTTPError(409, "provider_resource_reauthorization_required", "Subscription account needs authorization again"), Disposition: ProviderErrorAuthBroken}
	}
	if source.Error != "" {
		return nil, NewHTTPError(502, "subscription_quota_failed", "The subscription provider could not read this account's quota")
	}
	windows := []map[string]any{}
	additional := []map[string]any{}
	metrics := []map[string]string{}
	maxUsed := 0.0
	for index, window := range source.Windows {
		name := boundedSubscriptionText(window.Name)
		if name == "" {
			name = "Allowance " + strconv.Itoa(index+1)
		}
		if window.Display != "" {
			metrics = append(metrics, map[string]string{"label": name, "value": boundedSubscriptionText(window.Display)})
		}
		if window.Used == nil || math.IsNaN(*window.Used) || math.IsInf(*window.Used, 0) {
			continue
		}
		used := math.Max(0, math.Min(100, *window.Used))
		item := map[string]any{"key": "window-" + strconv.Itoa(index), "label": name, "used_percent": used}
		if reset, err := time.Parse(time.RFC3339Nano, window.ResetsAt); err == nil {
			item["reset_at"] = reset.Unix()
			item["reset_after_seconds"] = max(int64(0), reset.Unix()-now.Unix())
		}
		if window.Aside || len(window.Models) > 0 || len(window.NotModels) > 0 {
			scope := strings.Join(window.Models, ", ")
			if len(window.NotModels) > 0 {
				scope = "Excludes: " + strings.Join(window.NotModels, ", ")
			}
			additional = append(additional, map[string]any{"limit_name": name, "metered_feature": boundedSubscriptionText(scope), "rate_limit": map[string]any{"allowed": used < 100, "limit_reached": used >= 100, "primary_window": item}})
			continue
		}
		windows = append(windows, item)
		maxUsed = math.Max(maxUsed, used)
	}
	if source.Balance != "" {
		metrics = append(metrics, map[string]string{"label": "Balance", "value": boundedSubscriptionText(source.Balance)})
	}
	if until, err := time.Parse(time.RFC3339Nano, source.Until); err == nil {
		metrics = append(metrics, map[string]string{"label": "Plan expires", "value": until.UTC().Format(time.RFC3339)})
	}
	result := map[string]any{"plan_type": boundedSubscriptionText(source.Plan), "fetched_at": now.Unix(), "windows": windows, "additional_rate_limits": additional, "metrics": metrics}
	if len(windows) > 0 {
		result["used_percent"], result["remaining_percent"] = maxUsed, 100-maxUsed
		result["allowed"], result["limit_reached"] = maxUsed < 100, maxUsed >= 100
	} else {
		result["status"] = "reported"
		result["status_label"] = "Quota reported"
	}
	return result, nil
}

func normalizeSubscriptionCheckin(raw []byte) (map[string]any, error) {
	var source struct {
		Outcome, SignIn string
		Credit          *float64
		Streak          *int
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &source) != nil {
		return nil, NewHTTPError(502, "subscription_checkin_invalid", "Subscription runtime returned an invalid check-in result")
	}
	if source.SignIn == "expired" {
		return nil, &ProviderInvocationError{Err: NewHTTPError(409, "provider_resource_reauthorization_required", "Subscription account needs authorization again"), Disposition: ProviderErrorAuthBroken}
	}
	switch source.Outcome {
	case "claimed", "done", "inactive", "ineligible":
	default:
		return nil, NewHTTPError(502, "subscription_checkin_failed", "The subscription provider could not complete this account's check-in")
	}
	result := map[string]any{"outcome": source.Outcome}
	if source.Credit != nil && !math.IsNaN(*source.Credit) && !math.IsInf(*source.Credit, 0) && *source.Credit >= 0 {
		result["credit"] = *source.Credit
	}
	if source.Streak != nil && *source.Streak >= 0 {
		result["streak"] = *source.Streak
	}
	return result, nil
}

func boundedSubscriptionText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return ""
	}
	return value
}
