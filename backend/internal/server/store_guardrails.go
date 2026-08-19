package server

import (
	"net/http"
	"sort"
	"time"

	"tokenhub/backend/internal/guardrails"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) CreateGuardrailPolicy(policy guardrails.Policy) (guardrails.Policy, error) {
	normalized, err := guardrails.NormalizePolicy(policy)
	if err != nil {
		return guardrails.Policy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if normalized.ID == "" {
		normalized.ID = NewID("grp")
	}
	normalized.CreatedAt = now
	normalized.UpdatedAt = now
	var created guardrails.Policy
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := validateGuardrailBindingProjects(tx, normalized.Bindings); err != nil {
			return err
		}
		if err := tx.Omit("DetectionItems", "Bindings").Create(&normalized).Error; err != nil {
			return writeConflict(err, "guardrail_policy_conflict", "Guardrail policy already exists")
		}
		prepareNewGuardrailChildren(&normalized, now)
		if err := tx.Create(&normalized.DetectionItems).Error; err != nil {
			return writeConflict(err, "guardrail_detection_item_conflict", "Detection item already exists")
		}
		if err := tx.Create(&normalized.Bindings).Error; err != nil {
			return writeConflict(err, "guardrail_binding_conflict", "Policy binding already exists")
		}
		var err error
		created, err = loadGuardrailPolicy(tx, normalized.ID)
		return err
	})
	if err != nil {
		return guardrails.Policy{}, err
	}
	return created, nil
}

func (s *GormStore) ListGuardrailPolicies() ([]guardrails.Policy, error) {
	policies := []guardrails.Policy{}
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		return preloadGuardrailPolicy(tx).Order("created_at ASC, id ASC").Find(&policies).Error
	})
	if err != nil {
		return nil, err
	}
	return policies, nil
}

func (s *GormStore) GetGuardrailPolicy(id string) (guardrails.Policy, error) {
	var policy guardrails.Policy
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		var err error
		policy, err = loadGuardrailPolicy(tx, id)
		return err
	})
	return policy, err
}

func (s *GormStore) UpdateGuardrailPolicy(id string, policy guardrails.Policy) (guardrails.Policy, guardrails.Policy, error) {
	policy.ID = id
	normalized, err := guardrails.NormalizePolicy(policy)
	if err != nil {
		return guardrails.Policy{}, guardrails.Policy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var before guardrails.Policy
	var updated guardrails.Policy
	err = s.db.Transaction(func(tx *gorm.DB) error {
		current, err := lockGuardrailPolicyForMutation(tx, id)
		if err != nil {
			return err
		}
		before = current
		if err := validateGuardrailBindingProjects(tx, normalized.Bindings); err != nil {
			return err
		}
		now := time.Now().UTC()
		normalized.CreatedAt = current.CreatedAt
		normalized.UpdatedAt = now
		if err := prepareUpdatedGuardrailChildren(tx, &normalized, now); err != nil {
			return err
		}
		updates := map[string]any{
			"name":           normalized.Name,
			"description":    normalized.Description,
			"status":         normalized.Status,
			"config_version": normalized.ConfigVersion,
			"updated_at":     normalized.UpdatedAt,
		}
		if err := tx.Model(&guardrails.Policy{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", id).Delete(&guardrails.DetectionItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", id).Delete(&guardrails.Binding{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&normalized.DetectionItems).Error; err != nil {
			return writeConflict(err, "guardrail_detection_item_conflict", "Detection item already exists")
		}
		if err := tx.Create(&normalized.Bindings).Error; err != nil {
			return writeConflict(err, "guardrail_binding_conflict", "Policy binding already exists")
		}
		updated, err = loadGuardrailPolicy(tx, id)
		return err
	})
	if err != nil {
		return guardrails.Policy{}, guardrails.Policy{}, err
	}
	return before, updated, nil
}

func (s *GormStore) DeleteGuardrailPolicy(id string) (guardrails.Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var before guardrails.Policy
	err := s.db.Transaction(func(tx *gorm.DB) error {
		policy, err := lockGuardrailPolicyForMutation(tx, id)
		if err != nil {
			return err
		}
		before = policy
		if err := tx.Where("policy_id = ?", id).Delete(&guardrails.DetectionItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", id).Delete(&guardrails.Binding{}).Error; err != nil {
			return err
		}
		return tx.Delete(&policy).Error
	})
	return before, err
}

func preloadGuardrailPolicy(db *gorm.DB) *gorm.DB {
	return db.
		Preload("DetectionItems", func(query *gorm.DB) *gorm.DB {
			return query.Order("created_at ASC, id ASC")
		}).
		Preload("Bindings", func(query *gorm.DB) *gorm.DB {
			return query.Order("created_at ASC, id ASC")
		})
}

func loadGuardrailPolicy(db *gorm.DB, id string) (guardrails.Policy, error) {
	var policy guardrails.Policy
	if err := preloadGuardrailPolicy(db).First(&policy, "id = ?", id).Error; err != nil {
		return guardrails.Policy{}, notFound(err, "guardrail_policy_not_found", "Guardrail policy not found")
	}
	return policy, nil
}

func lockGuardrailPolicyForMutation(tx *gorm.DB, id string) (guardrails.Policy, error) {
	result := tx.Model(&guardrails.Policy{}).
		Where("id = ?", id).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return guardrails.Policy{}, result.Error
	}
	if result.RowsAffected == 0 {
		return guardrails.Policy{}, NewHTTPError(http.StatusNotFound, "guardrail_policy_not_found", "Guardrail policy not found")
	}
	return loadGuardrailPolicy(tx, id)
}

func validateGuardrailBindingProjects(tx *gorm.DB, bindings []guardrails.Binding) error {
	projectIDs := make([]string, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		if binding.ScopeType != guardrails.ScopeProject || seen[binding.ScopeID] {
			continue
		}
		seen[binding.ScopeID] = true
		projectIDs = append(projectIDs, binding.ScopeID)
	}
	if len(projectIDs) == 0 {
		return nil
	}
	sort.Strings(projectIDs)
	var projects []Project
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id IN ?", projectIDs).Order("id ASC").Find(&projects).Error; err != nil {
		return err
	}
	if len(projects) != len(projectIDs) {
		return NewHTTPError(http.StatusNotFound, "guardrail_binding_project_not_found", "Guardrail binding project not found")
	}
	return nil
}

func prepareNewGuardrailChildren(policy *guardrails.Policy, now time.Time) {
	for index := range policy.DetectionItems {
		item := &policy.DetectionItems[index]
		if item.ID == "" {
			item.ID = NewID("gdi")
		}
		item.PolicyID = policy.ID
		item.CreatedAt = now
		item.UpdatedAt = now
	}
	for index := range policy.Bindings {
		binding := &policy.Bindings[index]
		if binding.ID == "" {
			binding.ID = NewID("gbd")
		}
		binding.PolicyID = policy.ID
		binding.CreatedAt = now
		binding.UpdatedAt = now
	}
}

func prepareUpdatedGuardrailChildren(tx *gorm.DB, policy *guardrails.Policy, now time.Time) error {
	var currentItems []guardrails.DetectionItem
	if err := tx.Where("policy_id = ?", policy.ID).Find(&currentItems).Error; err != nil {
		return err
	}
	itemsByID := make(map[string]guardrails.DetectionItem, len(currentItems))
	for _, item := range currentItems {
		itemsByID[item.ID] = item
	}
	for index := range policy.DetectionItems {
		item := &policy.DetectionItems[index]
		if item.ID == "" {
			item.ID = NewID("gdi")
			item.CreatedAt = now
		} else if current, ok := itemsByID[item.ID]; ok {
			item.CreatedAt = current.CreatedAt
		} else {
			return NewHTTPError(http.StatusNotFound, "guardrail_detection_item_not_found", "Detection item does not belong to this policy")
		}
		item.PolicyID = policy.ID
		item.UpdatedAt = now
	}

	var currentBindings []guardrails.Binding
	if err := tx.Where("policy_id = ?", policy.ID).Find(&currentBindings).Error; err != nil {
		return err
	}
	bindingsByScope := make(map[string]guardrails.Binding, len(currentBindings))
	for _, binding := range currentBindings {
		bindingsByScope[guardrailBindingKey(binding)] = binding
	}
	for index := range policy.Bindings {
		binding := &policy.Bindings[index]
		if current, ok := bindingsByScope[guardrailBindingKey(*binding)]; ok {
			binding.ID = current.ID
			binding.CreatedAt = current.CreatedAt
		} else {
			binding.ID = NewID("gbd")
			binding.CreatedAt = now
		}
		binding.PolicyID = policy.ID
		binding.UpdatedAt = now
	}
	return nil
}

func guardrailBindingKey(binding guardrails.Binding) string {
	return binding.ScopeType + "\x00" + binding.ScopeID + "\x00" + binding.Checkpoint + "\x00" + binding.Protocol
}
