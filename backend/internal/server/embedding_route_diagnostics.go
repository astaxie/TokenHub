package server

import (
	"net/http"
	"sort"
	"strings"
)

type embeddingRouteRejection struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RouteCount int    `json:"route_count"`
}

// Expose actionable categories without disclosing provider identities, URLs,
// credentials, model inventory names, or procurement prices to API callers.
func embeddingRouteSelectionError(rejected map[string]int) *HTTPError {
	messages := map[string]string{
		"provider_capability_or_protocol_unsupported": "The provider adapter or configured protocol does not support embeddings; ask an administrator to check the provider type and embedding protocol.",
		"tenant_price_not_configured":                 "The public model embedding price is not configured; ask an administrator to configure the tenant price or explicitly confirm that it is free.",
		"upstream_model_inventory_missing":            "The route has no matching upstream model inventory entry; ask an administrator to import or add the model and match the route's upstream model name.",
		"upstream_model_modality_mismatch":            "The upstream inventory model type is not embedding; ask an administrator to correct its model type.",
		"upstream_text_input_unsupported":             "The upstream inventory model does not declare text input support; ask an administrator to verify its input capabilities.",
	}
	codes := make([]string, 0, len(rejected))
	for code := range rejected {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	reasons := make([]embeddingRouteRejection, 0, len(codes))
	summary := []string{"No eligible embedding route remains. No upstream request was sent."}
	for _, code := range codes {
		message := messages[code]
		reasons = append(reasons, embeddingRouteRejection{Code: code, Message: message, RouteCount: rejected[code]})
		summary = append(summary, message)
	}
	if len(codes) == 0 {
		summary = append(summary, "Ask an administrator to check route availability and provider eligibility.")
	}
	// Preserve the public status/code contract; details refine the diagnosis.
	err := NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", strings.Join(summary, " "))
	err.Details = map[string]any{"stage": "route_selection", "upstream_attempted": false, "reasons": reasons}
	return err
}
