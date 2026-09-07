package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/metering"
)

type modelPriceChange struct {
	RiskAcknowledged  bool                 `json:"risk_acknowledged"`
	AcknowledgedRisks []string             `json:"acknowledged_risks,omitempty"`
	Analysis          *pricingImpactReport `json:"analysis,omitempty"`
	AnalysisState     string               `json:"analysis_state"`
	BeforeEffective   metering.Rates       `json:"before_effective"`
	AfterEffective    metering.Rates       `json:"after_effective"`
	Replayed          bool                 `json:"-"`
	ID                string               `json:"id"`
	RequestID         string               `json:"request_id"`
	RequestHash       string               `json:"request_hash"`
	ModelName         string               `json:"model_name"`
	ActorID           string               `json:"actor_id"`
	ActorName         string               `json:"actor_name"`
	EffectiveAt       time.Time            `json:"effective_at"`
	Before            meteringRateCard     `json:"before"`
	After             meteringRateCard     `json:"after"`
}

type modelPricingDraft struct {
	ModelName   string           `json:"model_name"`
	Fingerprint string           `json:"fingerprint"`
	Card        meteringRateCard `json:"card"`
}

func pricingFingerprint(model Model) string {
	// Include inherited-price metadata so a concurrent change to the estimation
	// ratio cannot be overwritten by a preview based on different pricing rules.
	metadata := map[string]string{}
	for _, key := range []string{cacheReadEstimateRatioKey, "cached_input_price_usd_per_1m", "cache_read_price_usd_per_1m", "cached_read_price_usd_per_1m"} {
		if value, ok := model.Metadata[key]; ok {
			metadata[key] = value
		}
	}
	value := struct {
		ID, Modality string
		Card         meteringRateCard
		ReadRate     float64
		Metadata     map[string]string
	}{model.ID, model.Modality, modelPricingCard(model), effectiveCacheReadPriceUSDPer1M(model), metadata}
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func modelPricingCard(model Model) meteringRateCard {
	dec := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	rates := metering.Rates{Input: dec(model.InputPriceUSDPer1M), Output: dec(model.OutputPriceUSDPer1M)}
	if model.Modality == "embedding" {
		rates.Input = dec(model.EmbeddingPriceUSDPer1M)
		if model.EmbeddingPriceUSDPer1M == 0 {
			rates.Input = dec(model.InputPriceUSDPer1M)
		}
		rates.Output = "0"
	}
	if model.CacheReadPriceUSDPer1M != 0 || model.Metadata[cacheReadConfiguredKey] == "true" {
		rates.CacheRead = dec(model.CacheReadPriceUSDPer1M)
	}
	if model.CacheWritePriceConfigured {
		rates.CacheWrite = dec(model.CacheWritePriceUSDPer1M)
	}
	if model.CacheWrite5mPriceConfigured {
		rates.CacheWrite5m = dec(model.CacheWrite5mPriceUSDPer1M)
	}
	if model.CacheWrite1hPriceConfigured {
		rates.CacheWrite1h = dec(model.CacheWrite1hPriceUSDPer1M)
	}
	card := meteringRateCard{Kind: "tenant", Target: model.Name, Currency: "USD", Source: "model", Rates: rates}
	for _, p := range model.PricingPeriods {
		period := meteringPeriod{ModelPricingPeriod: p}
		period.Timezone = strings.TrimSpace(period.Timezone)
		if period.Timezone == "" {
			period.Timezone = "UTC"
		}
		period.Weekdays = append([]int(nil), period.Weekdays...)
		if len(period.Weekdays) == 0 {
			period.Weekdays = []int{0, 1, 2, 3, 4, 5, 6}
		}
		sort.Ints(period.Weekdays)
		prices := pricingPeriodPriceOverrides(p)
		fields := []*string{&period.Rates.Input, &period.Rates.Output, &period.Rates.CacheRead, &period.Rates.CacheWrite, &period.Rates.CacheWrite5m, &period.Rates.CacheWrite1h}
		for i, price := range prices {
			if price.value != nil {
				*fields[i] = dec(*price.value)
			}
		}
		period.InputPriceUSDPer1M = nil
		period.OutputPriceUSDPer1M = nil
		period.CacheReadPriceUSDPer1M = nil
		period.CacheWritePriceUSDPer1M = nil
		period.CacheWrite5mPriceUSDPer1M = nil
		period.CacheWrite1hPriceUSDPer1M = nil
		card.Periods = append(card.Periods, period)
	}
	return card
}

func candidateModelPricing(current Model, card meteringRateCard) (Model, error) {
	invalid := func(message string) (Model, error) { return Model{}, NewHTTPError(400, "invalid_model_price", message) }
	if card.Kind != "tenant" || card.Currency != "USD" || card.Target != current.Name {
		return invalid("A USD tenant price for this model is required")
	}
	if current.Modality != "" && current.Modality != "chat" && current.Modality != "embedding" {
		return invalid("Only token-priced chat and embedding models are supported")
	}
	if !card.EffectiveFrom.IsZero() {
		return invalid("Model price updates take effect immediately")
	}
	parse := func(value string) (float64, error) {
		if _, err := metering.Decimal(value); err != nil {
			return 0, err
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, fmt.Errorf("price must be finite")
		}
		return v, nil
	}
	candidate := current
	var err error
	if candidate.InputPriceUSDPer1M, err = parse(card.Rates.Input); err != nil {
		return invalid("Input price must be a non-negative decimal")
	}
	if candidate.OutputPriceUSDPer1M, err = parse(card.Rates.Output); err != nil {
		return invalid("Output price must be a non-negative decimal")
	}
	candidate.Metadata = withConfiguredCacheReadPrice(current.Metadata, card.Rates.CacheRead != "")
	prices := []struct {
		value      string
		target     *float64
		configured *bool
	}{
		{card.Rates.CacheRead, &candidate.CacheReadPriceUSDPer1M, nil},
		{card.Rates.CacheWrite, &candidate.CacheWritePriceUSDPer1M, &candidate.CacheWritePriceConfigured},
		{card.Rates.CacheWrite5m, &candidate.CacheWrite5mPriceUSDPer1M, &candidate.CacheWrite5mPriceConfigured},
		{card.Rates.CacheWrite1h, &candidate.CacheWrite1hPriceUSDPer1M, &candidate.CacheWrite1hPriceConfigured},
	}
	for _, p := range prices {
		*p.target = 0
		if p.configured != nil {
			*p.configured = p.value != ""
		}
		if p.value != "" {
			if *p.target, err = parse(p.value); err != nil {
				return invalid("Cache prices must be non-negative decimals or empty to inherit")
			}
		}
	}
	candidate.PricingPeriods = nil
	for _, p := range card.Periods {
		period := p.ModelPricingPeriod
		for _, v := range pricingPeriodPriceOverrides(period) {
			if v.value != nil {
				return invalid("Period overrides must use rates")
			}
		}
		fields := []**float64{&period.InputPriceUSDPer1M, &period.OutputPriceUSDPer1M, &period.CacheReadPriceUSDPer1M, &period.CacheWritePriceUSDPer1M, &period.CacheWrite5mPriceUSDPer1M, &period.CacheWrite1hPriceUSDPer1M}
		values := []string{p.Rates.Input, p.Rates.Output, p.Rates.CacheRead, p.Rates.CacheWrite, p.Rates.CacheWrite5m, p.Rates.CacheWrite1h}
		for i, value := range values {
			if value != "" {
				v, e := parse(value)
				if e != nil {
					return invalid("Period prices must be non-negative decimals")
				}
				*fields[i] = &v
			}
		}
		candidate.PricingPeriods = append(candidate.PricingPeriods, period)
	}
	if candidate.Modality == "embedding" {
		if len(card.Periods) > 0 {
			return invalid("Embedding models do not support time-window overrides")
		}
		candidate.EmbeddingPriceUSDPer1M = candidate.InputPriceUSDPer1M
		candidate.OutputPriceUSDPer1M = 0
		candidate.CacheReadPriceUSDPer1M = 0
		candidate.CacheWritePriceUSDPer1M = 0
		candidate.CacheWrite5mPriceUSDPer1M = 0
		candidate.CacheWrite1hPriceUSDPer1M = 0
		candidate.CacheWritePriceConfigured = false
		candidate.CacheWrite5mPriceConfigured = false
		candidate.CacheWrite1hPriceConfigured = false
	}
	if err := validateModelBasePrices(candidate); err != nil {
		return Model{}, err
	}
	if err := validateModelPricingPeriods(candidate.PricingPeriods); err != nil {
		return Model{}, err
	}
	return candidate, nil
}

func (s *GormStore) BillingModelPricing(name string) (modelPricingDraft, error) {
	var model Model
	if err := s.db.First(&model, "name = ?", name).Error; err != nil {
		return modelPricingDraft{}, notFound(err, "model_not_found", "Model not found")
	}
	return modelPricingDraft{ModelName: model.Name, Fingerprint: pricingFingerprint(model), Card: modelPricingCard(model)}, nil
}

func (s *GormStore) PreviewBillingModel(card meteringRateCard, usage Usage, at time.Time) (map[string]any, error) {
	var current Model
	if err := s.db.First(&current, "name = ?", card.Target).Error; err != nil {
		return nil, notFound(err, "model_not_found", "Model not found")
	}
	if current.Modality == "embedding" && (usage.CompletionTokens != 0 || usage.CachedInputTokens != 0 || usage.CacheWriteInputTokens != 0 || usage.CacheWrite5mInputTokens != 0 || usage.CacheWrite1hInputTokens != 0) {
		return nil, NewHTTPError(400, "invalid_usage", "Embedding previews accept input tokens only")
	}
	if _, err := meteringUnits(usage); err != nil {
		return nil, NewHTTPError(400, "invalid_usage", err.Error())
	}
	candidate, err := candidateModelPricing(current, card)
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	// Discard any client-supplied costs. Preview invokes the same billing function
	// and normalized numeric configuration as a real request after application.
	usage.CostUSD = 0
	usage.InputCostUSD = 0
	usage.CacheReadCostUSD = 0
	usage.CacheWriteCostUSD = 0
	usage.OutputCostUSD = 0
	usage.TotalTokens = saturatingAddNonNegative(usage.PromptTokens, usage.CompletionTokens)
	priced := priceUsageAt(candidate, usage, at)
	lines := []metering.Line{}
	for _, p := range []struct {
		kind   string
		units  int64
		amount float64
	}{
		{"input", maxInt64(priced.PromptTokens-priced.CachedInputTokens-priced.CacheWriteInputTokens, 0), priced.InputCostUSD},
		{"cache_read", priced.CachedInputTokens, priced.CacheReadCostUSD},
		{"cache_write", priced.CacheWriteInputTokens, priced.CacheWriteCostUSD},
		{"output", priced.CompletionTokens, priced.OutputCostUSD},
	} {
		if p.units > 0 {
			lines = append(lines, metering.Line{Kind: p.kind, Units: p.units, Amount: billingAmount(p.amount)})
		}
	}
	snapshot := legacyMeteringPrice(candidate, at, false)
	return map[string]any{"current": modelPricingCard(current), "proposed": modelPricingCard(candidate), "fingerprint": pricingFingerprint(current), "changed": pricingFingerprint(current) != pricingFingerprint(candidate), "snapshot": snapshot, "charge": metering.Charge{Currency: "USD", Amount: billingAmount(priced.CostUSD), USD: billingAmount(priced.CostUSD), Lines: lines}}, nil
}

func billingAmount(value float64) string { return strconv.FormatFloat(value, 'f', 12, 64) }

func (s *GormStore) ApplyBillingModel(card meteringRateCard, expected, requestID string, actor AdminUser) (modelPriceChange, error) {
	return s.applyBillingModelDecision(context.Background(), card, expected, requestID, actor, "", false)
}
func (s *GormStore) ApplyBillingModelAnalysis(ctx context.Context, card meteringRateCard, expected, requestID string, actor AdminUser, receipt string) (modelPriceChange, error) {
	return s.applyBillingModelDecision(ctx, card, expected, requestID, actor, receipt, true)
}
func (s *GormStore) applyBillingModelDecision(ctx context.Context, card meteringRateCard, expected, requestID string, actor AdminUser, receipt string, riskAcknowledged bool) (modelPriceChange, error) {
	var result modelPriceChange
	if expected == "" || len(requestID) < 8 || len(requestID) > 128 {
		return result, NewHTTPError(400, "pricing_confirmation_required", "A pricing fingerprint and request ID are required")
	}
	raw, _ := json.Marshal(struct {
		Card            meteringRateCard
		Expected, Actor string
	}{card, expected, actor.ID})
	if receipt != "" {
		raw, _ = json.Marshal(struct {
			Request json.RawMessage
			Receipt string
		}{raw, receipt})
	}
	digest := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(digest[:])
	id := "model-price:" + requestID
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "model_price_change", requestID); err != nil {
			return err
		}
		var existing meteringEntry
		err := tx.First(&existing, "id = ?", id).Error
		if err == nil {
			if e := json.Unmarshal([]byte(existing.Payload), &result); e != nil {
				return e
			}
			result.Replayed = true
			if result.RequestHash != requestHash {
				return NewHTTPError(409, "pricing_request_conflict", "This request ID was already used for a different price change")
			}
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		var current Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "name = ?", card.Target).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		if pricingFingerprint(current) != expected {
			return NewHTTPError(409, "model_price_changed", "Model prices changed after preview; preview and confirm again")
		}
		candidate, err := candidateModelPricing(current, card)
		if err != nil {
			return err
		}
		var analysis *pricingAnalysisDecision
		if receipt != "" {
			analysis, err = s.readPricingAnalysis(receipt, actor, current, candidate)
			if err != nil {
				return err
			}
		}
		if analysis != nil {
			if err := s.validateProcurementBasis(tx, analysis.Report.ProcurementBasis); err != nil {
				return err
			}
		}
		if pricingFingerprint(candidate) == expected {
			return NewHTTPError(409, "model_price_unchanged", "No model price changes to apply")
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		result = modelPriceChange{RiskAcknowledged: riskAcknowledged, AnalysisState: "not_performed", ID: id, RequestID: requestID, RequestHash: requestHash, ModelName: current.Name, ActorID: actor.ID, ActorName: actor.Username, EffectiveAt: now, Before: modelPricingCard(current), After: modelPricingCard(candidate), BeforeEffective: effectiveModelBaseRates(current), AfterEffective: effectiveModelBaseRates(candidate)}
		if analysis != nil {
			result.AnalysisState = "performed"
			result.Analysis = &analysis.Report
			result.AcknowledgedRisks = append([]string{}, analysis.Report.Risks...)
		}
		if analysis == nil && riskAcknowledged {
			result.AcknowledgedRisks = []string{"analysis_not_performed"}
		}
		if err := tx.Model(&candidate).Select("InputPriceUSDPer1M", "OutputPriceUSDPer1M", "EmbeddingPriceUSDPer1M", "CacheReadPriceUSDPer1M", "CacheWritePriceUSDPer1M", "CacheWrite5mPriceUSDPer1M", "CacheWrite1hPriceUSDPer1M", "CacheWritePriceConfigured", "CacheWrite5mPriceConfigured", "CacheWrite1hPriceConfigured", "PricingPeriods", "Metadata").Updates(&candidate).Error; err != nil {
			return err
		}
		return saveMeteringEntry(tx, id, "model_price_change", current.Name, result, now)
	})
	if err == nil {
		s.modelLabels.invalidate()
	}
	return result, err
}

func (s *GormStore) BillingPriceChanges() ([]modelPriceChange, error) {
	var rows []meteringEntry
	if err := s.db.Where("kind = ?", "model_price_change").Order("created_at DESC, id DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]modelPriceChange, 0, len(rows))
	for _, row := range rows {
		var item modelPriceChange
		if err := json.Unmarshal([]byte(row.Payload), &item); err != nil {
			return nil, err
		}
		item.RequestHash = ""
		result = append(result, item)
	}
	return result, nil
}

func updateModelAsActor(store Store, name string, patch Model, actor AdminUser) (Model, error) {
	if writer, ok := store.(interface {
		UpdateModelWithActor(string, Model, AdminUser) (Model, error)
	}); ok {
		return writer.UpdateModelWithActor(name, patch, actor)
	}
	return store.UpdateModel(name, patch)
}
func recordModelPriceChange(tx *gorm.DB, before, after Model, actor AdminUser, now time.Time) error {
	before.ID = after.ID
	before.Name = after.Name
	if pricingFingerprint(before) == pricingFingerprint(after) {
		return nil
	}
	name := actor.Username
	if name == "" {
		name = "system"
	}
	change := modelPriceChange{AnalysisState: "not_performed", ID: NewID("price_change"), ModelName: after.Name, ActorID: actor.ID, ActorName: name, EffectiveAt: now, Before: modelPricingCard(before), After: modelPricingCard(after), BeforeEffective: effectiveModelBaseRates(before), AfterEffective: effectiveModelBaseRates(after)}
	return saveMeteringEntry(tx, change.ID, "model_price_change", after.Name, change, now)
}

func effectiveModelBaseRates(model Model) metering.Rates {
	model.PricingPeriods = nil
	return legacyMeteringPrice(model, time.Now().UTC(), false).Rates
}
