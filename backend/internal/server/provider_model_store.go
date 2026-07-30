package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

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
	now := time.Now().UTC()
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
	if model.LastSeenAt == nil {
		model.LastSeenAt = &now
	}
	_ = s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model).Error
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

	var model ProviderModel
	if err := s.db.First(&model, "id = ?", id).Error; err != nil {
		return ProviderModel{}, notFound(err, "provider_model_not_found", "Provider model not found")
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
	model.OutputPriceUSDPer1M = patch.OutputPriceUSDPer1M
	model.UpdatedAt = time.Now().UTC()
	return model, s.db.Save(&model).Error
}

func (s *GormStore) DeleteProviderModel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var model ProviderModel
	if err := s.db.First(&model, "id = ?", id).Error; err != nil {
		return notFound(err, "provider_model_not_found", "Provider model not found")
	}
	return s.db.Delete(&model).Error
}
