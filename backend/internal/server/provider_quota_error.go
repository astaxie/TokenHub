package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Only structured, explicit quota codes override a 4xx classification. Free
// text, generic throttling and server failures remain on their existing paths.
func classifyProviderResponse(status int, body []byte) providerErrorClass {
	class := classifyProviderStatus(status)
	if status == http.StatusPaymentRequired || status < 400 || status >= 500 {
		return class
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return class
	}
	fields := []map[string]any{payload}
	if nested, ok := payload["error"].(map[string]any); ok {
		fields = append(fields, nested)
	}
	for _, values := range fields {
		for _, name := range []string{"code", "type"} {
			code, _ := values[name].(string)
			switch strings.ToLower(strings.TrimSpace(code)) {
			case "insufficient_quota", "quota_exhausted", "usage_limit_reached", "billing_hard_limit_reached", "insufficient_balance", "balance_not_enough", "weekly_quota_exceeded", "monthly_quota_exceeded":
				return providerErrorClass{http.StatusTooManyRequests, "provider_quota_exhausted", ProviderErrorQuotaExhausted}
			}
		}
	}
	return class
}

func providerQuotaExhaustedCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "provider_payment_required", "provider_quota_exhausted", "provider_usage_cap_reached", "codex_quota_exhausted", "subscription_standard_quota_exhausted":
		return true
	}
	return false
}

// A quota denial proves that the provider responded, while the inference
// request still failed. Keep liveness separate from spendable allowance.
func providerProbeHealthy(err error) bool {
	return err == nil || providerErrorDisposition(err) == ProviderErrorQuotaExhausted
}
