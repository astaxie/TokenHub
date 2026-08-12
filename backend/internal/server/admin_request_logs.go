package server

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultRequestLogPageSize = 20
	maxRequestLogPageSize     = 100
)

func (s *Server) handleAdminRequestLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	query, err := s.requestLogQueryForUser(user, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.store.QueryRequestLogs(query)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !s.canViewGlobalOperations(user) {
		for index := range page.Data {
			page.Data[index].ProviderCostUSD = 0
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) requestLogQueryForUser(user AdminUser, r *http.Request) (RequestLogQuery, error) {
	page, err := positiveRequestLogQueryInt(r.URL.Query().Get("page"), 1)
	if err != nil {
		return RequestLogQuery{}, err
	}
	pageSize, err := positiveRequestLogQueryInt(r.URL.Query().Get("page_size"), defaultRequestLogPageSize)
	if err != nil {
		return RequestLogQuery{}, err
	}
	if pageSize > maxRequestLogPageSize {
		pageSize = maxRequestLogPageSize
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		return RequestLogQuery{}, NewHTTPError(http.StatusBadRequest, "invalid_request", "page offset is too large")
	}
	query := RequestLogQuery{
		Page:     page,
		PageSize: pageSize,
		Status:   strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status"))),
		Keyword:  strings.TrimSpace(r.URL.Query().Get("q")),
		Global:   s.canViewGlobalOperations(user),
	}
	if query.Status == "" {
		query.Status = "all"
	}
	if query.Status != "all" && query.Status != "ok" && query.Status != "error" {
		return RequestLogQuery{}, NewHTTPError(http.StatusBadRequest, "invalid_request", "status must be all, ok, or error")
	}
	if query.Global {
		return query, nil
	}
	if normalizeAdminRole(user.Role) == "team_leader" {
		query.ProjectIDs = trueMapKeys(s.visibleProjectIDSet(user))
	}
	query.APIKeyIDs = trueMapKeys(s.visibleAPIKeyIDSet(user))
	return query, nil
}

func positiveRequestLogQueryInt(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, NewHTTPError(http.StatusBadRequest, "invalid_request", "page and page_size must be positive integers")
	}
	return parsed, nil
}

func trueMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, include := range values {
		if include {
			keys = append(keys, key)
		}
	}
	return keys
}
