package server

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"gorm.io/gorm"
)

type pricingImpactQuery struct {
	Card        meteringRateCard `json:"card"`
	Fingerprint string           `json:"fingerprint"`
	Basis       string           `json:"basis"`
	From        string           `json:"from"`
	To          string           `json:"to"`
	Timezone    string           `json:"timezone"`
	ProjectIDs  []string         `json:"project_ids"`
}
type pricingImpactAmounts struct {
	Current             string  `json:"current_usd"`
	Candidate           string  `json:"candidate_usd"`
	Cost                string  `json:"cost_usd"`
	Delta               string  `json:"charge_delta_usd"`
	CurrentMargin       string  `json:"current_margin_usd"`
	CandidateMargin     string  `json:"candidate_margin_usd"`
	CurrentMarginRate   *string `json:"current_margin_percent"`
	CandidateMarginRate *string `json:"candidate_margin_percent"`
}
type pricingImpactGroup struct {
	ID           string               `json:"id"`
	Names        []string             `json:"names"`
	Requests     int                  `json:"requests"`
	LossRequests int                  `json:"loss_requests"`
	Amounts      pricingImpactAmounts `json:"amounts"`
}
type pricingImpactReport struct {
	ProcurementBasis  []procurementPriceBasis `json:"procurement_basis,omitempty"`
	Model             string                  `json:"model"`
	Basis             string                  `json:"basis"`
	Timezone          string                  `json:"timezone"`
	ProjectIDs        []string                `json:"project_ids"`
	From              time.Time               `json:"from"`
	To                time.Time               `json:"to"`
	Cutoff            time.Time               `json:"cutoff"`
	Requests          int                     `json:"requests"`
	Computable        int                     `json:"computable"`
	UnknownCharges    int                     `json:"unknown_charges"`
	RecordedCharges   string                  `json:"recorded_charges_usd"`
	RequestCoverage   *string                 `json:"request_coverage_percent"`
	ChargeCoverage    *string                 `json:"known_charge_coverage_percent"`
	Overall           *pricingImpactAmounts   `json:"overall"`
	ComputableAmounts *pricingImpactAmounts   `json:"computable_amounts"`
	Excluded          map[string]int          `json:"excluded"`
	Groups            []pricingImpactGroup    `json:"groups"`
	UntestedPeriods   []string                `json:"untested_periods"`
	LossRequests      int                     `json:"loss_requests"`
	Risks             []string                `json:"risks"`
	Receipt           string                  `json:"receipt,omitempty"`
}

func (s *GormStore) AnalyzeModelPricing(ctx context.Context, q pricingImpactQuery, actor AdminUser) (pricingImpactReport, error) {
	report := pricingImpactReport{Model: q.Card.Target, Basis: q.Basis, ProjectIDs: append([]string{}, q.ProjectIDs...), Excluded: map[string]int{}, Groups: []pricingImpactGroup{}, UntestedPeriods: []string{}, Risks: []string{}}
	if q.Basis != "historical" && q.Basis != "current_procurement" {
		return report, NewHTTPError(400, "invalid_cost_basis", "Choose a supported cost basis")
	}
	query := statementQuery{Side: "margin", From: q.From, To: q.To, Timezone: q.Timezone, ProjectIDs: q.ProjectIDs, Model: q.Card.Target}
	from, to, err := query.window()
	if err != nil {
		return report, err
	}
	report.From = from
	report.To = to
	report.Timezone = query.Timezone
	var options *sql.TxOptions
	if s.dbDriver == "postgres" {
		options = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, candidate, _, err := checkedModelPricing(tx, q.Card, q.Fingerprint)
		if err != nil {
			return err
		}
		report.Cutoff = time.Now().UTC()
		ledger := statementResult{Query: query, From: from, To: to, GeneratedAt: report.Cutoff, Rows: []statementRow{}}
		if err := buildStatement(tx, &ledger); err != nil {
			return err
		}
		if q.Basis == "current_procurement" {
			bases, err := s.repriceProcurement(tx, ledger.Rows)
			if err != nil {
				return err
			}
			report.ProcurementBasis = bases
		}
		calculatePricingImpact(&report, current, candidate, ledger.Rows)
		sort.Strings(report.Risks)
		receipt, err := s.signPricingAnalysis(pricingAnalysisDecision{ActorID: actor.ID, CurrentFingerprint: q.Fingerprint, CandidateFingerprint: pricingFingerprint(candidate), Report: report})
		if err != nil {
			return err
		}
		report.Receipt = receipt
		return nil
	}, options)
	return report, err
}
func (s *Server) handlePricingImpact(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	store, ok := s.store.(interface {
		AnalyzeModelPricing(context.Context, pricingImpactQuery, AdminUser) (pricingImpactReport, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "pricing_unavailable", "Pricing analysis is unavailable"))
		return
	}
	var q pricingImpactQuery
	if err := s.decodeJSON(w, r, &q); err != nil {
		writeError(w, r, err)
		return
	}
	report, err := store.AnalyzeModelPricing(r.Context(), q, user)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, report)
}
