package server

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type billingStatementsStore interface {
	PlatformBillingStatement(context.Context, billingStatementQuery) (billingStatement, error)
	scanBillingStatements(context.Context, billingStatementQuery, func(billingStatementRow) error) error
}

func (s *Server) handleBillingStatements(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	store, ok := s.store.(billingStatementsStore)
	if !ok {
		writeError(w, r, NewHTTPError(503, "billing_unavailable", "Billing statements are unavailable"))
		return
	}
	values := r.URL.Query()
	q := billingStatementQuery{Kind: values.Get("kind"), Provider: values.Get("provider_id"), Model: values.Get("model"), Project: values.Get("project_id"), GroupBy: values.Get("group_by"), Limit: 100}
	if q.Kind == "" {
		q.Kind = "provider"
	}
	if q.GroupBy == "" {
		q.GroupBy = "provider"
		if q.Kind == "tenant" {
			q.GroupBy = "project"
		}
	}
	var err error
	q.From, err = time.Parse(time.RFC3339, values.Get("from"))
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_statement_period", "from must be RFC3339"))
		return
	}
	q.To, err = time.Parse(time.RFC3339, values.Get("to"))
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_statement_period", "to must be RFC3339"))
		return
	}
	if value := values.Get("offset"); value != "" {
		q.Offset, err = strconv.Atoi(value)
		if err != nil || q.Offset < 0 {
			writeError(w, r, NewHTTPError(400, "invalid_pagination", "offset must be non-negative"))
			return
		}
	}
	if value := values.Get("limit"); value != "" {
		q.Limit, err = strconv.Atoi(value)
		if err != nil || q.Limit < 1 || q.Limit > 500 {
			writeError(w, r, NewHTTPError(400, "invalid_pagination", "limit must be between 1 and 500"))
			return
		}
	}
	q.From = q.From.UTC()
	q.To = q.To.UTC()
	if err := validateBillingStatementQuery(q); err != nil {
		writeError(w, r, err)
		return
	}
	if values.Get("format") == "csv" {
		s.exportPlatformBillingStatement(w, r, store, q)
		return
	}
	result, err := store.PlatformBillingStatement(r.Context(), q)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
func billingCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") || trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
func (s *Server) exportPlatformBillingStatement(w http.ResponseWriter, r *http.Request, store billingStatementsStore, q billingStatementQuery) {
	file, err := os.CreateTemp("", "tokenhub-statement-*.csv")
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	writer := csv.NewWriter(file)
	err = writer.Write([]string{"kind", "occurred_at", "request_id", "upstream_request_id", "provider_id", "provider_name", "resource_id", "resource_name", "model", "project_id", "project_name", "user_id", "api_key_id", "status", "reason", "currency", "amount", "amount_usd", "source", "evidence_incomplete"})
	if err == nil {
		err = store.scanBillingStatements(r.Context(), q, func(row billingStatementRow) error {
			fields := []string{q.Kind, row.OccurredAt.Format(time.RFC3339Nano), row.RequestID, row.UpstreamRequestID, row.ProviderID, row.ProviderName, row.ResourceID, row.ResourceName, row.Model, row.ProjectID, row.ProjectName, row.UserID, row.APIKeyID, row.Status, row.Reason, row.Currency, row.Amount, row.AmountUSD, row.Source, strconv.FormatBool(row.EvidenceIncomplete)}
			for i := range fields {
				fields[i] = billingCSVCell(fields[i])
			}
			return writer.Write(fields)
		})
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tokenhub-`+q.Kind+`-statement.csv"`)
	_, _ = io.Copy(w, file)
}
