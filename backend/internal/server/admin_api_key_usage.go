package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const defaultAPIKeyUsageDays = 30

type apiKeyUsageRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func (s *Server) handleAdminAPIKeyUsageGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	keyID := r.PathValue("key_id")
	if keyID == "" || strings.Contains(keyID, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if !s.canManageAPIKey(user, keyID) {
		writeError(w, r, NewHTTPError(http.StatusForbidden, "api_key_forbidden", "API key is not available for this user"))
		return
	}
	key, err := s.findAPIKey(keyID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	project, ok := s.store.GetProject(key.ProjectID)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
		return
	}
	usageRange, err := parseAPIKeyUsageRange(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	queryContext, cancel := context.WithTimeout(r.Context(), maximumTokenCostQueryDuration)
	defer cancel()
	usage, err := s.store.QueryAPIKeyUsage(queryContext, APIKeyUsageQuery{
		Key: key, Project: project, From: usageRange.From, To: usageRange.To,
		IncludeProviders: isPlatformAdminRole(user.Role),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, r, NewHTTPError(http.StatusServiceUnavailable, "api_key_usage_query_timeout", "API key usage query exceeded the 10 second execution limit"))
			return
		}
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "api_key_usage_query_failed", "Failed to query API key usage"))
		return
	}
	payload := map[string]any{
		"key":          publicKey(key),
		"range":        usageRange,
		"generated_at": time.Now().UTC(),
		"summary":      usage.Summary,
		"quota":        usage.Quota,
		"timeseries":   usage.Timeseries,
		"models":       usage.Models,
		"errors":       usage.Errors,
	}
	if isPlatformAdminRole(user.Role) {
		payload["providers"] = usage.Providers
	}
	writeJSON(w, http.StatusOK, payload)
}

func parseAPIKeyUsageRange(r *http.Request) (apiKeyUsageRange, error) {
	now := time.Now().UTC()
	to, err := parseOptionalAPIKeyUsageTime("to", r.URL.Query().Get("to"))
	if err != nil {
		return apiKeyUsageRange{}, err
	}
	if to.IsZero() {
		to = now
	}
	from, err := parseOptionalAPIKeyUsageTime("from", r.URL.Query().Get("from"))
	if err != nil {
		return apiKeyUsageRange{}, err
	}
	if from.IsZero() {
		from = utcDay(to).AddDate(0, 0, -(defaultAPIKeyUsageDays - 1))
	}
	if !from.Before(to) {
		return apiKeyUsageRange{}, NewHTTPError(http.StatusBadRequest, "invalid_request", "from must be before to")
	}
	if to.Sub(from) > maximumGroupedCostRange {
		return apiKeyUsageRange{}, NewHTTPError(http.StatusBadRequest, "invalid_request", "usage range cannot exceed 366 days")
	}
	return apiKeyUsageRange{From: from, To: to}, nil
}

func parseOptionalAPIKeyUsageTime(name, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, NewHTTPError(http.StatusBadRequest, "invalid_request", name+" must be an RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}
