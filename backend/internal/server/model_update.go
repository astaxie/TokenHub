package server

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (s *GormStore) UpdateModel(name string, patch Model) (Model, error) {
	return s.UpdateModelWithActor(name, patch, AdminUser{})
}
func (s *GormStore) UpdateModelWithActor(name string, patch Model, actor AdminUser) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var updated Model
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var model Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "name = ?", name).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		beforePricing := model
		originalID := model.ID
		originalName := model.Name
		renamed := patch.Name != "" && patch.Name != name
		if renamed {
			model.Name = patch.Name
			model.ID = patch.Name
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
		if err := applyModelPricingPatch(&model, patch); err != nil {
			return err
		}
		if patch.InputModalities != nil {
			model.InputModalities = patch.InputModalities
		}
		if patch.OutputModalities != nil {
			model.OutputModalities = patch.OutputModalities
		}
		if patch.Capabilities != nil {
			model.Capabilities = patch.Capabilities
		}
		if patch.SupportedParameters != nil {
			model.SupportedParameters = patch.SupportedParameters
		}
		model.Metadata = modelPricingMetadata(model.Metadata, patch)
		if patch.Status != "" {
			model.Status = patch.Status
		}
		if renamed {
			if err := tx.Delete(&Model{}, "id = ?", originalID).Error; err != nil {
				return err
			}
			if err := tx.Create(&model).Error; err != nil {
				return writeConflict(err, "model_conflict", "Model already exists")
			}
			if err := tx.Model(&ModelRoute{}).Where("model_name = ?", originalName).Update("model_name", model.Name).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&model).Error; err != nil {
			return err
		}
		if err := recordModelPriceChange(tx, beforePricing, model, actor, time.Now().UTC()); err != nil {
			return err
		}
		updated = model
		return nil
	})
	if err == nil {
		// An update can rename the model, so both the old and the new name are
		// wrong in the snapshot until it reloads.
		s.modelLabels.invalidate()
	}
	return updated, err
}
