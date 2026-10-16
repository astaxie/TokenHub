package server

import (
	"strconv"
	"strings"
)

// Error codes and headers can contain provider or plugin payloads just as error
// messages can. Retain only the gateway's known classifications and numeric
// rate-limit metadata when reporting failures for privacy-sensitive endpoints.
func copySanitizedHTTPErrorMetadata(target, original *HTTPError) {
	target.Status, target.UpstreamStatus = original.Status, original.UpstreamStatus
	if sensitiveEndpointErrorCodeAllowed(original.Code) {
		target.Code = original.Code
	}
	for name, value := range original.Headers {
		switch strings.ToLower(name) {
		case "retry-after", "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests",
			"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens":
		default:
			continue
		}
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			continue
		}
		if target.Headers == nil {
			target.Headers = make(map[string]string)
		}
		target.Headers[name] = strconv.FormatUint(number, 10)
	}
}

func sensitiveEndpointErrorCodeAllowed(code string) bool {
	switch code {
	case "provider_error", "provider_invalid_request", "provider_auth_error", "provider_payment_required",
		"provider_model_not_found", "provider_upstream_timeout", "provider_rate_limited", "provider_upstream_unavailable",
		"provider_upstream_error", "provider_resource_exhausted", "provider_request_failed", "provider_stream_idle_timeout",
		"provider_stream_error", "provider_not_configured", "provider_capability_not_supported", "provider_unavailable",
		"provider_egress_blocked", "provider_egress_unavailable", "provider_base_url_not_allowed", "provider_base_url_insecure_scheme",
		"provider_proxy_connect_failed", "provider_proxy_auth_failed", "provider_proxy_timeout", "provider_proxy_config_error",
		"provider_base_url_invalid", "provider_base_url_invalid_scheme", "provider_credentials_unavailable",
		"provider_resource_missing", "provider_resource_disabled", "provider_resource_unhealthy", "provider_resource_cooling_down",
		"provider_resource_rpm_exceeded", "provider_resource_tpm_exceeded", "provider_resource_concurrency_exceeded",
		"gateway_hook_auth_context_invalid", "gateway_hook_cache_hit_invalid", "gateway_hook_denied", "gateway_hook_failed",
		"gateway_hook_input_invalid", "gateway_hook_patch_invalid", "gateway_hook_response_invalid", "gateway_hook_route_candidates_invalid",
		"gateway_hook_route_skipped", "gateway_hook_stream_event_invalid", "gateway_hook_usage_invalid",
		"invalid_media_response", "invalid_media_field", "invalid_provider_usage", "invalid_rerank_response", "invalid_rerank_request",
		"rerank_parameter_unsupported", "rerank_protocol_required", "invalid_rerank_path", "provider_adapter_missing",
		"media_stream_incomplete", "multipart_transcription_hook_unsupported", "content_filter", "request_too_large", "coordination_lease_lost",
		"guardrail_blocked", "guardrail_policy_load_failed", "guardrail_evaluation_failed", "guardrail_evaluation_budget_exceeded",
		"model_not_allowed", "quota_exceeded", "budget_exceeded", "rate_limit_exceeded", "token_rate_limit_exceeded":
		return true
	default:
		return false
	}
}
