package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTokenCostLimit         = 100
	maximumTokenCostLimit         = 1000
	maximumRawCostRange           = 31 * 24 * time.Hour
	maximumGroupedCostRange       = 366 * 24 * time.Hour
	maximumTokenCostQueryDuration = 10 * time.Second
	tokenCostCursorVersion        = 3
)

type tokenCostCursor struct {
	Version         int                  `json:"v"`
	AfterAt         time.Time            `json:"after_at"`
	AfterID         string               `json:"after_id"`
	From            time.Time            `json:"from"`
	Through         time.Time            `json:"through"`
	Kind            string               `json:"kind"`
	Offset          int                  `json:"offset,omitempty"`
	QueryKey        string               `json:"query_key,omitempty"`
	Query           tokenCostCursorQuery `json:"query"`
	Checkpoint      int64                `json:"checkpoint"`
	AfterCheckpoint int64                `json:"after_checkpoint,omitempty"`
	Incremental     bool                 `json:"incremental,omitempty"`
}

type tokenCostCursorQuery struct {
	ProjectID   string   `json:"project_id,omitempty"`
	UserID      string   `json:"user_id,omitempty"`
	APIKeyID    string   `json:"api_key_id,omitempty"`
	ProviderID  string   `json:"provider_id,omitempty"`
	Model       string   `json:"model,omitempty"`
	Status      string   `json:"status,omitempty"`
	Granularity string   `json:"granularity"`
	GroupBy     []string `json:"group_by"`
}

func (s *Server) registerAdminAnalyticsCredentialRoutes() {
	s.registerMethodRoutes("/api/admin/analytics/credentials", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("analytics_credential", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminAnalyticsCredentialsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminAnalyticsCredentialsPost},
	)
	s.registerSingleMethodRoute(
		http.MethodDelete,
		"/api/admin/analytics/credentials/{credential_id}",
		s.handleAdminAnalyticsCredentialDelete,
		s.adminMethodNotAllowed("analytics_credential", http.MethodDelete),
	)
	s.mux.HandleFunc("/api/admin/analytics/credentials/", s.handleAdminAnalyticsCredentialNested)
}

func (s *Server) handleAdminAnalyticsCredentialsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "analytics_credential", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAnalyticsCredentials()})
}

func (s *Server) handleAdminAnalyticsCredentialsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "analytics_credential", r.Method)
	if !ok {
		return
	}
	var request struct {
		Name      string     `json:"name"`
		ScopeType string     `json:"scope_type"`
		ProjectID string     `json:"project_id"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	credential, token, err := s.store.CreateAnalyticsCredential(AnalyticsCredential{
		Name: request.Name, ScopeType: request.ScopeType, ProjectID: request.ProjectID,
		ExpiresAt: request.ExpiresAt, CreatedBy: user.ID,
	}, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "analytics_credential", credential.ID, nil, credential)
	w.Header().Set("cache-control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"credential": credential, "token": token})
}

func (s *Server) handleAdminAnalyticsCredentialDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "analytics_credential", r.Method)
	if !ok {
		return
	}
	id := r.PathValue("credential_id")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "analytics_credential_not_found", "Analytics credential not found"))
		return
	}
	credential, err := s.store.RevokeAnalyticsCredential(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "revoke", "analytics_credential", credential.ID, nil, credential)
	writeJSON(w, http.StatusOK, credential)
}

func (s *Server) handleAdminAnalyticsCredentialNested(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "analytics_credential", r.Method); !ok {
		return
	}
	if r.Method != http.MethodDelete {
		jsonMethodNotAllowed(http.MethodDelete)(w, r)
		return
	}
	writeError(w, r, NewHTTPError(http.StatusNotFound, "analytics_credential_not_found", "Analytics credential not found"))
}

func (s *Server) handleTokenCostAnalytics(w http.ResponseWriter, r *http.Request) {
	credential, err := s.store.ValidateAnalyticsCredential(bearerToken(r))
	if err != nil {
		s.recordTokenCostAudit(r, AnalyticsCredential{}, "failed", AsHTTPError(err).Code, nil)
		writeError(w, r, err)
		return
	}
	query, metadata, err := s.parseTokenCostQuery(r, credential)
	if err != nil {
		s.recordTokenCostAudit(r, credential, "failed", AsHTTPError(err).Code, nil)
		writeError(w, r, err)
		return
	}
	queryContext, cancelQuery := context.WithTimeout(r.Context(), maximumTokenCostQueryDuration)
	defer cancelQuery()
	page, err := s.store.QueryTokenCostPage(queryContext, query)
	if err != nil {
		s.recordTokenCostAudit(r, credential, "failed", "analytics_query_failed", metadata)
		writeError(w, r, tokenCostStoreError(err))
		return
	}
	rows, hasMore := page.Rows, page.HasMore
	queryKey := tokenCostQueryKey(credential, query)
	watermark := encodeTokenCostCursor(tokenCostCursor{
		Version: tokenCostCursorVersion, Checkpoint: page.Checkpoint,
		From: query.From, Through: query.To, Kind: "watermark", QueryKey: queryKey,
		Query: tokenCostCursorQueryFrom(query), Incremental: query.Incremental,
	})
	nextCursor := ""
	if hasMore && query.Granularity == "request" && len(rows) > 0 {
		last := rows[len(rows)-1]
		lastTime, _ := time.Parse(time.RFC3339Nano, last.OccurredAt)
		nextCursor = encodeTokenCostCursor(tokenCostCursor{
			Version: tokenCostCursorVersion, AfterAt: lastTime, AfterID: last.RequestID,
			From: query.From, Through: query.To, Kind: "request", QueryKey: queryKey,
			Query: tokenCostCursorQueryFrom(query), Checkpoint: page.Checkpoint,
			AfterCheckpoint: query.AfterSequence, Incremental: query.Incremental,
		})
	} else if hasMore {
		nextCursor = encodeTokenCostCursor(tokenCostCursor{
			Version: tokenCostCursorVersion, From: query.From, Through: query.To,
			Kind: "aggregate", Offset: query.Offset + len(rows), QueryKey: queryKey,
			Query: tokenCostCursorQueryFrom(query), Checkpoint: page.Checkpoint,
			AfterCheckpoint: query.AfterSequence, Incremental: query.Incremental,
		})
	}
	payload := TokenCostResponse{
		SchemaVersion: TokenCostSchemaVersion,
		Object:        "token_cost.list",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Query:         metadata,
		Data:          rows,
		HasMore:       hasMore,
		NextCursor:    nextCursor,
		Watermark:     watermark,
	}
	s.recordTokenCostAudit(r, credential, "success", "", map[string]any{
		"query": metadata, "row_count": len(rows), "has_more": hasMore,
	})
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("x-tokenhub-schema-version", TokenCostSchemaVersion)
	w.Header().Set("x-tokenhub-has-more", strconv.FormatBool(hasMore))
	w.Header().Set("x-tokenhub-dedupe-by", "dedupe_key")
	w.Header().Set("x-tokenhub-checkpoint-by", "commit_sequence")
	w.Header().Set("x-tokenhub-incremental-mode", metadata.IncrementalMode)
	if watermark != "" {
		w.Header().Set("x-tokenhub-watermark", watermark)
	}
	if nextCursor != "" {
		w.Header().Set("x-tokenhub-next-cursor", nextCursor)
	}
	if metadata.Format == "csv" {
		if err := writeTokenCostCSV(w, rows); err != nil {
			writeError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func tokenCostStoreError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return NewHTTPError(http.StatusServiceUnavailable, "analytics_query_timeout", "Analytics query exceeded the 10 second execution limit")
	}
	return err
}

func (s *Server) parseTokenCostQuery(r *http.Request, credential AnalyticsCredential) (TokenCostQuery, TokenCostQueryMetadata, error) {
	values := r.URL.Query()
	now := time.Now().UTC()
	format, err := tokenCostResponseFormat(r)
	if err != nil {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, err
	}
	cursorValue := strings.TrimSpace(values.Get("cursor"))
	afterValue := strings.TrimSpace(values.Get("after"))
	if cursorValue != "" && afterValue != "" {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor and after cannot be combined")
	}
	var pageCursor tokenCostCursor
	var afterCursor tokenCostCursor
	var cursorErr error
	fromFallback := now.Add(-24 * time.Hour)
	toFallback := now
	if cursorValue != "" {
		pageCursor, cursorErr = decodeTokenCostCursor(cursorValue)
		if cursorErr != nil || pageCursor.Kind == "watermark" {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
		}
		fromFallback = pageCursor.From
		toFallback = pageCursor.Through
	} else if afterValue != "" {
		afterCursor, cursorErr = decodeTokenCostCursor(afterValue)
		if cursorErr != nil || afterCursor.Kind != "watermark" {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "after watermark is invalid")
		}
		if strings.TrimSpace(values.Get("from")) != "" {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "from cannot be combined with after")
		}
		fromFallback = afterCursor.From
	}
	if cursorValue != "" {
		applyTokenCostCursorQuery(values, pageCursor.Query)
	} else if afterValue != "" {
		applyTokenCostCursorQuery(values, afterCursor.Query)
	}
	from, err := parseTokenCostTime(values.Get("from"), fromFallback, "from")
	if err != nil {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, err
	}
	to, err := parseTokenCostTime(values.Get("to"), toFallback, "to")
	if err != nil {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, err
	}
	if afterValue != "" && to.Before(afterCursor.Through) {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "to cannot be earlier than the watermark snapshot")
	}
	if !from.Before(to) {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_time_range", "from must be earlier than to")
	}
	groupBy, err := parseTokenCostGroupBy(values["group_by"])
	if err != nil {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, err
	}
	granularity := strings.ToLower(strings.TrimSpace(values.Get("granularity")))
	if granularity == "" {
		if len(groupBy) == 0 {
			granularity = "request"
		} else {
			granularity = "none"
		}
	}
	incremental := afterValue != "" || pageCursor.Incremental
	switch granularity {
	case "request":
		if len(groupBy) > 0 {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_grouping", "request granularity cannot be combined with group_by")
		}
		if !incremental && to.Sub(from) > maximumRawCostRange {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "analytics_time_range_too_large", "Request-level analytics queries are limited to 31 days")
		}
	case "none", "hour", "day", "month":
		if !incremental && to.Sub(from) > maximumGroupedCostRange {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "analytics_time_range_too_large", "Aggregated analytics queries are limited to 366 days")
		}
	default:
		return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_granularity", "granularity must be request, none, hour, day, or month")
	}
	limit := defaultTokenCostLimit
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > maximumTokenCostLimit {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_limit", "limit must be between 1 and 1000")
		}
		limit = parsed
	}
	projectID := strings.TrimSpace(values.Get("project_id"))
	if credential.ScopeType == AnalyticsScopeProject {
		if projectID != "" && projectID != credential.ProjectID {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusForbidden, "analytics_scope_forbidden", "Analytics credential cannot access the requested project")
		}
		projectID = credential.ProjectID
	}
	status := strings.ToLower(strings.TrimSpace(values.Get("status")))
	if status != "" && status != TokenCostStatusSuccess && status != TokenCostStatusError {
		return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_status", "status must be success or error")
	}
	filters := map[string]string{}
	for key, value := range map[string]string{
		"project_id":  projectID,
		"user_id":     strings.TrimSpace(values.Get("user_id")),
		"api_key_id":  strings.TrimSpace(values.Get("api_key_id")),
		"provider_id": strings.TrimSpace(values.Get("provider_id")),
		"model":       strings.TrimSpace(values.Get("model")),
		"status":      status,
	} {
		if value != "" {
			filters[key] = value
		}
	}
	query := TokenCostQuery{
		From: from, To: to, ProjectID: projectID,
		UserID: filters["user_id"], APIKeyID: filters["api_key_id"],
		ProviderID: filters["provider_id"], Model: filters["model"], Status: status,
		Granularity: granularity, GroupBy: groupBy, Limit: limit, Incremental: incremental,
	}
	if cursorValue != "" {
		if !from.Equal(pageCursor.From) || !to.Equal(pageCursor.Through) {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor time range does not match the query")
		}
		expectedKind := "aggregate"
		if granularity == "request" {
			expectedKind = "request"
		}
		if pageCursor.Kind != expectedKind || (pageCursor.QueryKey != "" && pageCursor.QueryKey != tokenCostQueryKey(credential, query)) {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor does not match the query")
		}
		query.AfterAt = pageCursor.AfterAt
		query.AfterID = pageCursor.AfterID
		query.Offset = pageCursor.Offset
		query.AfterSequence = pageCursor.AfterCheckpoint
		query.ThroughSequence = pageCursor.Checkpoint
		query.ThroughSequenceSet = true
	} else if afterValue != "" {
		if afterCursor.QueryKey == "" || afterCursor.QueryKey != tokenCostQueryKey(credential, query) {
			return TokenCostQuery{}, TokenCostQueryMetadata{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "after watermark does not match the query")
		}
		query.AfterSequence = afterCursor.Checkpoint
	}
	incrementalMode := TokenCostIncrementalSnapshot
	if query.Incremental {
		incrementalMode = TokenCostIncrementalChanges
	}
	metadata := TokenCostQueryMetadata{
		From: from.Format(time.RFC3339Nano), To: to.Format(time.RFC3339Nano),
		Granularity: granularity, GroupBy: groupBy, Filters: filters, Format: format, Limit: limit,
		DedupeBy: "dedupe_key", CheckpointBy: "commit_sequence", IncrementalMode: incrementalMode,
	}
	return query, metadata, nil
}

func applyTokenCostCursorQuery(values map[string][]string, query tokenCostCursorQuery) {
	for key, value := range map[string]string{
		"project_id":  query.ProjectID,
		"user_id":     query.UserID,
		"api_key_id":  query.APIKeyID,
		"provider_id": query.ProviderID,
		"model":       query.Model,
		"status":      query.Status,
		"granularity": query.Granularity,
	} {
		if _, present := values[key]; !present && value != "" {
			values[key] = []string{value}
		}
	}
	if _, present := values["group_by"]; !present && len(query.GroupBy) > 0 {
		values["group_by"] = []string{strings.Join(query.GroupBy, ",")}
	}
}

func tokenCostResponseFormat(r *http.Request) (string, error) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		accept := strings.ToLower(r.Header.Get("accept"))
		if strings.Contains(accept, "text/csv") {
			format = "csv"
		} else {
			format = "json"
		}
	}
	if format != "json" && format != "csv" {
		return "", NewHTTPError(http.StatusBadRequest, "invalid_analytics_format", "format must be json or csv")
	}
	return format, nil
}

func parseTokenCostGroupBy(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		for _, dimension := range strings.Split(value, ",") {
			dimension = strings.ToLower(strings.TrimSpace(dimension))
			if dimension == "" || seen[dimension] {
				continue
			}
			switch dimension {
			case "project", "user", "api_key", "provider", "model", "status":
			default:
				return nil, NewHTTPError(http.StatusBadRequest, "invalid_analytics_group_by", "group_by supports project, user, api_key, provider, model, and status")
			}
			seen[dimension] = true
			result = append(result, dimension)
		}
	}
	return result, nil
}

func parseTokenCostTime(value string, fallback time.Time, field string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_time", field+" must be an RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}

func encodeTokenCostCursor(cursor tokenCostCursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeTokenCostCursor(value string) (tokenCostCursor, error) {
	if len(value) > 8192 {
		return tokenCostCursor{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return tokenCostCursor{}, err
	}
	var cursor tokenCostCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return tokenCostCursor{}, err
	}
	if cursor.Version != tokenCostCursorVersion || cursor.From.IsZero() || cursor.Through.IsZero() ||
		!cursor.From.Before(cursor.Through) || cursor.Query.Granularity == "" ||
		cursor.Checkpoint < 0 || cursor.AfterCheckpoint < 0 || cursor.AfterCheckpoint > cursor.Checkpoint {
		return tokenCostCursor{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
	}
	switch cursor.Kind {
	case "request":
		if cursor.AfterAt.IsZero() || cursor.AfterID == "" {
			return tokenCostCursor{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
		}
	case "aggregate":
		if cursor.Offset <= 0 {
			return tokenCostCursor{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
		}
	case "watermark":
	default:
		return tokenCostCursor{}, NewHTTPError(http.StatusBadRequest, "invalid_analytics_cursor", "cursor is invalid")
	}
	return cursor, nil
}

func tokenCostCursorQueryFrom(query TokenCostQuery) tokenCostCursorQuery {
	return tokenCostCursorQuery{
		ProjectID:   query.ProjectID,
		UserID:      query.UserID,
		APIKeyID:    query.APIKeyID,
		ProviderID:  query.ProviderID,
		Model:       query.Model,
		Status:      query.Status,
		Granularity: query.Granularity,
		GroupBy:     append([]string(nil), query.GroupBy...),
	}
}

func tokenCostQueryKey(credential AnalyticsCredential, query TokenCostQuery) string {
	data, _ := json.Marshal(struct {
		CredentialID string
		ProjectID    string
		UserID       string
		APIKeyID     string
		ProviderID   string
		Model        string
		Status       string
		Granularity  string
		GroupBy      []string
	}{
		CredentialID: credential.ID,
		ProjectID:    query.ProjectID,
		UserID:       query.UserID,
		APIKeyID:     query.APIKeyID,
		ProviderID:   query.ProviderID,
		Model:        query.Model,
		Status:       query.Status,
		Granularity:  query.Granularity,
		GroupBy:      query.GroupBy,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeTokenCostCSV(w http.ResponseWriter, rows []TokenCostRow) error {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"dedupe_key", "bucket", "request_id", "occurred_at", "project_id", "user_id", "api_key_id",
		"provider_id", "model", "status", "status_code", "request_count", "error_count",
		"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens",
		"reasoning_output_tokens", "total_tokens", "estimated_cost_usd",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writer.Write([]string{
			safeReconciliationCSVCell(row.DedupeKey), safeReconciliationCSVCell(row.Bucket),
			safeReconciliationCSVCell(row.RequestID), safeReconciliationCSVCell(row.OccurredAt),
			safeReconciliationCSVCell(row.ProjectID), safeReconciliationCSVCell(row.UserID),
			safeReconciliationCSVCell(row.APIKeyID), safeReconciliationCSVCell(row.ProviderID),
			safeReconciliationCSVCell(row.Model), safeReconciliationCSVCell(row.Status), strconv.Itoa(row.StatusCode),
			strconv.FormatInt(row.Metrics.RequestCount, 10), strconv.FormatInt(row.Metrics.ErrorCount, 10),
			strconv.FormatInt(row.Metrics.InputTokens, 10), strconv.FormatInt(row.Metrics.CachedInputTokens, 10),
			strconv.FormatInt(row.Metrics.CacheWriteTokens, 10), strconv.FormatInt(row.Metrics.OutputTokens, 10),
			strconv.FormatInt(row.Metrics.ReasoningTokens, 10), strconv.FormatInt(row.Metrics.TotalTokens, 10),
			strconv.FormatFloat(row.Metrics.EstimatedCostUSD, 'f', -1, 64),
		}); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	w.Header().Set("content-type", "text/csv; charset=utf-8")
	w.Header().Set("content-disposition", `attachment; filename="token-costs.csv"`)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(buffer.Bytes())
	return err
}

func (s *Server) recordTokenCostAudit(r *http.Request, credential AnalyticsCredential, status string, message string, snapshot any) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:   credential.ID,
		ActorName:     credential.Name,
		ActorRole:     "analytics_credential",
		Action:        "query",
		ResourceType:  "token_cost_analytics",
		ResourceID:    firstNonEmpty(credential.ProjectID, credential.ScopeType),
		Status:        status,
		Message:       message,
		AfterSnapshot: snapshotJSON(snapshot),
		IP:            s.clientIP(r),
		UserAgent:     r.UserAgent(),
	})
}
