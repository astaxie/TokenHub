package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/metering"
)

type meteringRequestSnapshot struct {
	RequestID   string                `json:"request_id"`
	ProjectID   string                `json:"project_id"`
	ProjectName string                `json:"project_name"`
	APIKeyID    string                `json:"api_key_id"`
	APIKeyName  string                `json:"api_key_name"`
	UserID      string                `json:"user_id"`
	TeamID      string                `json:"team_id,omitempty"`
	CostCenter  string                `json:"cost_center,omitempty"`
	BudgetDay   string                `json:"budget_day_utc"`
	BudgetMonth string                `json:"budget_month_utc"`
	Price       meteringPriceSnapshot `json:"price"`
}

type meteringAttemptSnapshot struct {
	ID            string                 `json:"id"`
	RequestID     string                 `json:"request_id"`
	Number        int                    `json:"number"`
	ProviderID    string                 `json:"provider_id"`
	ProviderName  string                 `json:"provider_name"`
	ResourceID    string                 `json:"resource_id,omitempty"`
	ResourceName  string                 `json:"resource_name,omitempty"`
	UpstreamModel string                 `json:"upstream_model"`
	State         string                 `json:"state"`
	At            time.Time              `json:"at"`
	Price         *meteringPriceSnapshot `json:"price,omitempty"`
	LegacyModel   *Model                 `json:"-"`
}

type meteringShadowCharge struct {
	Status      string                 `json:"status"`
	Reason      string                 `json:"reason,omitempty"`
	UsageSource string                 `json:"usage_source"`
	Price       *meteringPriceSnapshot `json:"price,omitempty"`
	Units       metering.Units         `json:"units"`
	Charge      *metering.Charge       `json:"charge,omitempty"`
	LegacyUSD   string                 `json:"legacy_usd"`
}

func legacyMeteringPrice(model Model, at time.Time, provider bool) meteringPriceSnapshot {
	resolved := modelPriceAt(model, at)
	read := effectiveCacheReadPriceUSDPer1M(resolved)
	if provider {
		read = resolved.CacheReadPriceUSDPer1M
	}
	dec := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	rates := metering.Rates{Input: dec(resolved.InputPriceUSDPer1M), CacheRead: dec(read), CacheWrite: dec(effectiveCacheWritePriceUSDPer1M(resolved)), CacheWrite5m: dec(effectiveCacheWrite5mPriceUSDPer1M(resolved)), CacheWrite1h: dec(effectiveCacheWrite1hPriceUSDPer1M(resolved)), Output: dec(resolved.OutputPriceUSDPer1M)}
	if model.Modality == "embedding" {
		rates.Input = dec(resolved.EmbeddingPriceUSDPer1M)
		if provider {
			rates.Input = dec(resolved.InputPriceUSDPer1M)
		}
	}
	snapshot := meteringPriceSnapshot{Currency: "USD", Source: "legacy_float_configuration", At: at, Rates: rates}
	for _, period := range model.PricingPeriods {
		if pricingPeriodMatches(period, at) {
			snapshot.Period = period.Name
			break
		}
	}
	if provider {
		snapshot.Rates = providerLegacyMeteringRates(model, resolved, snapshot.Rates, at)
	}
	return snapshot
}

func (s *GormStore) captureMeteringRequest(tx *gorm.DB, call CallContext) error {
	if call.RequestID == "" {
		return nil
	}
	price := legacyMeteringPrice(call.Model, call.StartedAt, false)
	card, err := loadMeteringCard(tx, "tenant", call.Model.Name, call.StartedAt)
	if err != nil {
		return err
	}
	if card != nil {
		price = card.at(call.StartedAt)
	}
	snapshot := meteringRequestSnapshot{RequestID: call.RequestID, ProjectID: call.Project.ID, ProjectName: call.Project.Name, APIKeyID: call.Key.ID, APIKeyName: call.Key.Name, UserID: call.AttributedUserID, TeamID: call.Project.TeamID, CostCenter: call.Project.CostCenter, BudgetDay: call.StartedAt.UTC().Format("2006-01-02"), BudgetMonth: call.StartedAt.UTC().Format("2006-01"), Price: price}
	return saveMeteringEntry(tx, call.RequestID+":admission", "admission", call.RequestID, snapshot, call.StartedAt)
}

// Preparation is durable before invoking a provider. A prepared entry without a
// completion means possibly sent; recovery must not retry or assume zero cost.
func (s *GormStore) PrepareMeteringAttempt(requestID string, number int, route RouteSelection, at time.Time) (RouteSelection, error) {
	snapshot := meteringAttemptSnapshot{ID: fmt.Sprintf("%s:attempt:%d", requestID, number), RequestID: requestID, Number: number, ProviderID: route.Provider.ID, ProviderName: route.Provider.Name, ResourceID: routeResourceID(route), UpstreamModel: route.ProviderModel, State: "possibly_sent", At: at}
	if route.Resource != nil {
		snapshot.ResourceName = route.Resource.Name
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var model ProviderModel
		err := tx.Where("provider_id = ? AND upstream_model = ?", route.Provider.ID, route.ProviderModel).First(&model).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			legacy := providerModelCostModel(model)
			snapshot.LegacyModel = &legacy
			price := legacyMeteringPrice(legacy, at, true)
			snapshot.Price = &price
		}
		card, err := loadMeteringCard(tx, "provider", route.Provider.ID+":"+route.ProviderModel, at)
		if err != nil {
			return err
		}
		if card != nil {
			price := card.at(at)
			snapshot.Price = &price
		}
		if snapshot.Price != nil && snapshot.Price.Currency != "USD" {
			fx, err := loadMeteringFX(tx, snapshot.Price.Currency, at)
			if err != nil {
				return err
			}
			if fx != nil {
				snapshot.Price.ExchangeRateVersion = fx.ID
				snapshot.Price.ExchangeRate = fx.Rate
			}
		}
		if requestID == "" {
			return nil
		}
		return saveMeteringEntry(tx, snapshot.ID, "attempt_prepared", requestID, snapshot, at)
	})
	if err != nil {
		return route, err
	}
	route.MeteringSnapshot = &snapshot
	return route, nil
}

func shadowPrice(price *meteringPriceSnapshot, usage Usage, legacy float64) meteringShadowCharge {
	result := meteringShadowCharge{Status: "pending", UsageSource: "legacy_adapter_unverified", Price: price, LegacyUSD: strconv.FormatFloat(legacy, 'f', -1, 64)}
	if price == nil {
		result.Reason = "missing_price"
		return result
	}
	units, err := meteringUnits(usage)
	if usage.MeteringInvalid {
		err = fmt.Errorf("invalid original usage")
	}
	if usage.MeteringRaw != nil {
		units = *usage.MeteringRaw
	}
	if err != nil {
		result.Reason = "inconsistent_usage"
		return result
	}
	result.Units = units
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		result.Reason = "usage_presence_unknown"
		return result
	}
	charge, err := metering.Price(price.Rates, units, price.Currency, price.ExchangeRate)
	if err != nil {
		result.Reason = "missing_or_invalid_price"
		return result
	}
	result.Status = "estimated"
	result.Reason = "usage_presence_and_provider_time_basis_unverified"
	result.Charge = &charge
	return result
}

func (s *GormStore) settleMeteringShadow(tx *gorm.DB, call CallContext, usage Usage, status int, at time.Time) error {
	var entry meteringEntry
	if err := tx.First(&entry, "id = ?", call.RequestID+":admission").Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var admission meteringRequestSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &admission); err != nil {
		return err
	}
	tenant := shadowPrice(&admission.Price, usage, usage.CostUSD)
	if status >= 400 {
		tenant.Status = "pending"
		tenant.Reason = "delivery_evidence_required"
		tenant.Charge = nil
	}
	attempts := make([]meteringAttemptCharge, 0, len(call.RouteAttempts))
	for _, attempt := range call.RouteAttempts {
		if !attempt.Invoked {
			continue
		}
		var price *meteringPriceSnapshot
		if attempt.Selection.MeteringSnapshot != nil {
			price = attempt.Selection.MeteringSnapshot.Price
		}
		legacy := 0.0
		knownLegacy := providerLegacyMeteringKnown(attempt.Selection.MeteringSnapshot, attempt.Usage)
		if knownLegacy {
			legacy = s.providerCostUSDAt(attempt.Selection, attempt.Usage, attempt.StartedAt)
		}
		charge := shadowPrice(price, attempt.Usage, legacy)
		if !knownLegacy {
			charge.LegacyUSD = ""
		}
		item := meteringAttemptCharge{Charge: charge, UpstreamRequestID: attempt.Usage.UpstreamRequestID, StartedAt: attempt.StartedAt, EndedAt: attempt.EndedAt, Status: attempt.Status, ErrorCode: attempt.ErrorCode}
		if snapshot := attempt.Selection.MeteringSnapshot; snapshot != nil {
			item.ID = snapshot.ID
			item.Number = snapshot.Number
		}
		attempts = append(attempts, item)
	}
	return saveMeteringEntry(tx, call.RequestID+":shadow", "shadow_settlement", call.RequestID, map[string]any{"tenant": tenant, "attempts": attempts, "mode": "shadow", "budget_day_utc": admission.BudgetDay, "budget_month_utc": admission.BudgetMonth}, at)
}

type meteringAttemptCharge struct {
	ID                string               `json:"attempt_id"`
	Number            int                  `json:"number"`
	UpstreamRequestID string               `json:"upstream_request_id,omitempty"`
	StartedAt         time.Time            `json:"started_at"`
	EndedAt           time.Time            `json:"ended_at"`
	Status            int                  `json:"status"`
	ErrorCode         string               `json:"error_code,omitempty"`
	Charge            meteringShadowCharge `json:"pricing"`
}
