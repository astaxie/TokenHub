package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func stableProviderModelID(providerID string, upstreamModel string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(providerID) + ":" + strings.TrimSpace(upstreamModel)))
	return "pmdl_" + hex.EncodeToString(sum[:])[:20]
}

func (s *GormStore) AddProviderModel(model ProviderModel) ProviderModel {
	s.mu.Lock()
	defer s.mu.Unlock()

	model.ProviderID = strings.TrimSpace(model.ProviderID)
	model.UpstreamModel = strings.TrimSpace(model.UpstreamModel)
	if model.ID == "" {
		model.ID = stableProviderModelID(model.ProviderID, model.UpstreamModel)
	}
	if model.DisplayName == "" {
		model.DisplayName = model.UpstreamModel
	}
	if model.Status == "" {
		model.Status = StatusActive
	}
	normalizeProviderModelCacheWriteConfiguration(&model)
	now := time.Now().UTC()
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
	if model.LastSeenAt == nil {
		model.LastSeenAt = &now
	}
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_pricing", procurementKey(model.ProviderID, model.UpstreamModel)); err != nil {
			return err
		}
		// Discovery refreshes catalog details; saved prices and their presence flags
		// remain under the explicit inventory-edit workflow, including free prices.
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider_id"}, {Name: "upstream_model"}}, DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "canonical_name", "category", "family", "modality", "context_window",
			"input_modalities", "output_modalities", "capabilities", "supported_parameters", "last_seen_at", "updated_at",
		})}).Create(&model).Error; err != nil {
			return err
		}
		var saved ProviderModel
		if err := tx.Where("provider_id = ? AND upstream_model = ?", model.ProviderID, model.UpstreamModel).First(&saved).Error; err == nil {
			model = saved
		}
		return nil
	})
	return model
}

func (s *GormStore) ListProviderModels() []ProviderModel {
	var items []ProviderModel
	_ = s.db.Order("provider_id asc, upstream_model asc").Find(&items).Error
	return items
}

func (s *GormStore) UpdateProviderModel(id string, patch ProviderModel) (ProviderModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result ProviderModel
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var model ProviderModel
		if err := tx.First(&model, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_model_not_found", "Provider model not found")
		}
		if err := s.lockScopeForUpdate(tx, "provider_pricing", procurementKey(model.ProviderID, model.UpstreamModel)); err != nil {
			return err
		}
		if err := tx.First(&model, "id = ?", id).Error; err != nil {
			return err
		}
		if patch.DisplayName != "" {
			model.DisplayName = patch.DisplayName
		}
		if patch.CanonicalName != "" {
			model.CanonicalName = patch.CanonicalName
		}
		if patch.Category != "" {
			model.Category = patch.Category
		}
		if patch.Family != "" {
			model.Family = patch.Family
		}
		if patch.Modality != "" {
			model.Modality = patch.Modality
		}
		if patch.ContextWindow != 0 {
			model.ContextWindow = patch.ContextWindow
		}
		if patch.Capabilities != nil {
			model.Capabilities = patch.Capabilities
		}
		if patch.SupportedParameters != nil {
			model.SupportedParameters = patch.SupportedParameters
		}
		if patch.Metadata != nil {
			model.Metadata = patch.Metadata
		}
		if patch.Status != "" {
			model.Status = patch.Status
		}
		model.InputPriceUSDPer1M = patch.InputPriceUSDPer1M
		model.CacheReadPriceUSDPer1M = patch.CacheReadPriceUSDPer1M
		model.CacheWritePriceUSDPer1M = patch.CacheWritePriceUSDPer1M
		model.CacheWritePriceConfigured = patch.CacheWritePriceConfigured
		model.CacheWrite5mPriceUSDPer1M = patch.CacheWrite5mPriceUSDPer1M
		model.CacheWrite5mPriceConfigured = patch.CacheWrite5mPriceConfigured
		model.CacheWrite1hPriceUSDPer1M = patch.CacheWrite1hPriceUSDPer1M
		model.CacheWrite1hPriceConfigured = patch.CacheWrite1hPriceConfigured
		model.OutputPriceUSDPer1M = patch.OutputPriceUSDPer1M
		model.PricingPeriods = append([]ModelPricingPeriod(nil), patch.PricingPeriods...)
		normalizeProviderModelCacheWriteConfiguration(&model)
		model.UpdatedAt = time.Now().UTC()
		result = model
		return tx.Save(&model).Error
	})
	return result, err
}

func normalizeProviderModelCacheWriteConfiguration(model *ProviderModel) {
	if model.CacheWritePriceUSDPer1M != 0 {
		model.CacheWritePriceConfigured = true
	}
	if model.CacheWrite5mPriceUSDPer1M != 0 {
		model.CacheWrite5mPriceConfigured = true
	}
	if model.CacheWrite1hPriceUSDPer1M != 0 {
		model.CacheWrite1hPriceConfigured = true
	}
}

func (s *GormStore) DeleteProviderModel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var model ProviderModel
		if err := tx.First(&model, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_model_not_found", "Provider model not found")
		}
		if err := s.lockScopeForUpdate(tx, "provider_pricing", procurementKey(model.ProviderID, model.UpstreamModel)); err != nil {
			return err
		}
		return tx.Delete(&model).Error
	})
}
