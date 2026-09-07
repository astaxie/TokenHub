package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type procurementPriceBasis struct {
	ProviderID    string           `json:"provider_id"`
	UpstreamModel string           `json:"upstream_model"`
	Modality      string           `json:"modality"`
	Missing       bool             `json:"missing"`
	Configuration meteringRateCard `json:"configuration"`
	Configured    map[string]bool  `json:"configured"`
	Fingerprint   string           `json:"fingerprint"`
}

func procurementKey(provider, model string) string {
	raw, _ := json.Marshal([]string{provider, model})
	return string(raw)
}
func procurementBasis(pm ProviderModel, missing bool) procurementPriceBasis {
	basis := procurementPriceBasis{ProviderID: pm.ProviderID, UpstreamModel: pm.UpstreamModel, Modality: pm.Modality, Missing: missing, Configuration: modelPricingCard(providerModelCostModel(pm)), Configured: map[string]bool{}}
	basis.Configuration.Kind = "provider"
	basis.Configuration.Target = procurementKey(pm.ProviderID, pm.UpstreamModel)
	for _, key := range []string{"input_price_configured", "output_price_configured", cacheReadConfiguredKey} {
		basis.Configured[key] = pm.Metadata[key] == "true"
	}
	raw, _ := json.Marshal(basis)
	sum := sha256.Sum256(raw)
	basis.Fingerprint = hex.EncodeToString(sum[:])
	return basis
}
func readProcurementModel(tx *gorm.DB, provider, model string) (ProviderModel, bool, error) {
	pm := ProviderModel{ProviderID: provider, UpstreamModel: model}
	err := tx.Where("provider_id = ? AND upstream_model = ?", provider, model).First(&pm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return pm, true, nil
	}
	return pm, false, err
}
func (s *GormStore) repriceProcurement(tx *gorm.DB, rows []statementRow) ([]procurementPriceBasis, error) {
	cache := map[string]ProviderModel{}
	bases := map[string]procurementPriceBasis{}
	for i := range rows {
		row := &rows[i]
		if row.Source != "provider_estimate" {
			continue
		}
		key := procurementKey(row.ProviderID, row.Model)
		if _, ok := bases[key]; !ok {
			pm, missing, err := readProcurementModel(tx, row.ProviderID, row.Model)
			if err != nil {
				return nil, err
			}
			cache[key] = pm
			bases[key] = procurementBasis(pm, missing)
		}
		pm := cache[key]
		row.USD = nil
		row.Amount = nil
		row.Status = "pending"
		row.Reason = "current_procurement_unavailable"
		if bases[key].Missing || !row.AttemptFinished {
			continue
		}
		usage, valid := replayUsage(row.Units)
		if !valid {
			continue
		}
		costModel := providerModelCostModel(pm)
		price := legacyMeteringPrice(costModel, row.At, true)
		if !replayEvidenceKnown(row.Evidence, price.Rates, row.Units) {
			continue
		}
		if costModel.Modality != "" && costModel.Modality != "chat" && costModel.Modality != "embedding" {
			continue
		}
		if costModel.Modality == "embedding" && (usage.CompletionTokens != 0 || usage.CachedInputTokens != 0 || usage.CacheWriteInputTokens != 0) {
			continue
		}
		usage.Evidence = row.Evidence
		if shadowPrice(&price, usage, 0).Charge == nil {
			continue
		}
		route := RouteSelection{Provider: Provider{ID: row.ProviderID}, ProviderModel: row.Model, MeteringSnapshot: &meteringAttemptSnapshot{At: row.At, LegacyModel: &costModel, Price: &price}}
		amount := billingAmount(s.providerCostUSDAt(route, usage, row.At))
		if value, ok := new(big.Rat).SetString(amount); !ok || value.Sign() < 0 {
			continue
		}
		row.Price = &price
		row.USD = statementString(amount)
		row.Amount = row.USD
		row.Status = "estimated"
		row.Reason = "current_procurement_scenario"
	}
	keys := make([]string, 0, len(bases))
	for key := range bases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]procurementPriceBasis, 0, len(keys))
	for _, key := range keys {
		result = append(result, bases[key])
	}
	return result, nil
}
func (s *GormStore) validateProcurementBasis(tx *gorm.DB, bases []procurementPriceBasis) error {
	// The signed order is sorted, and every writer uses the same natural-key lock.
	for _, basis := range bases {
		if err := s.lockScopeForUpdate(tx, "provider_pricing", procurementKey(basis.ProviderID, basis.UpstreamModel)); err != nil {
			return err
		}
		pm, missing, err := readProcurementModel(tx.Clauses(clause.Locking{Strength: "SHARE"}), basis.ProviderID, basis.UpstreamModel)
		if err != nil {
			return err
		}
		if procurementBasis(pm, missing).Fingerprint != basis.Fingerprint {
			return NewHTTPError(409, "pricing_basis_changed", "The pricing basis changed; reload and analyze again")
		}
	}
	return nil
}
