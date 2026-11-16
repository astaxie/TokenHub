package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
)

type allowanceResetIntent struct {
	KeyID      string            `json:"key_id"`
	Model      string            `json:"model"`
	ProjectID  string            `json:"project_id"`
	Candidates map[string]string `json:"candidates"`
	Revision   string            `json:"revision"`
}

// Intents are recorded only after an actual exhausted invocation. Simulation
// never creates one, and its candidate IDs already passed project/key filters.
func recordAllowanceResetIntent(ctx context.Context, store Store, call CallContext, route RouteSelection, routes []RouteSelection) {
	persistence, ok := store.(*GormStore)
	if !ok || route.Resource == nil || call.Key.ID == "" || call.Model.Name == "" {
		return
	}
	cfg, err := persistence.providerAllowanceConfig(ctx, route.Resource.ID)
	if err != nil || !cfg.Policy.Reset.OnExhausted {
		return
	}
	candidates := map[string]string{}
	for _, r := range routes {
		if r.Resource == nil {
			return
		}
		if previous, exists := candidates[r.Resource.ID]; exists && previous != r.ProviderModel {
			return
		}
		candidates[r.Resource.ID] = r.ProviderModel
	}
	if len(candidates) == 0 {
		return
	}
	intent := allowanceResetIntent{KeyID: call.Key.ID, Model: call.Model.Name, ProjectID: call.Project.ID, Candidates: candidates, Revision: cfg.Revision}
	details, err := json.Marshal(intent)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	row := providerAllowanceEvent{ID: "reset-intent:" + route.Resource.ID, ResourceID: route.Resource.ID, Kind: "reset_intent", Cycle: "", State: "queued", CreatedAt: now, UpdatedAt: now, Details: string(details)}
	_ = persistence.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"state", "updated_at", "details"})}).Create(&row).Error
}

func (s *Server) allowanceResetIntentCurrent(ctx context.Context, id string, now time.Time) bool {
	store, ok := s.store.(*GormStore)
	if !ok {
		return false
	}
	var row providerAllowanceEvent
	if store.db.WithContext(ctx).First(&row, "id = ? AND state = ? AND updated_at >= ?", "reset-intent:"+id, "queued", now.Add(-2*time.Minute)).Error != nil {
		return false
	}
	var intent allowanceResetIntent
	if json.Unmarshal([]byte(row.Details), &intent) != nil || intent.KeyID == "" || intent.Model == "" || len(intent.Candidates) == 0 || len(intent.Candidates) > 1000 {
		return false
	}
	project, ok := store.GetProject(intent.ProjectID)
	if !ok || project.Status != StatusActive {
		return false
	}
	key, ok := store.GetAPIKey(intent.KeyID)
	if !ok || key.ProjectID != project.ID || key.Status != StatusActive || key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) || len(modelAccessReasons(project, key, intent.Model)) > 0 {
		return false
	}
	cfg, err := store.providerAllowanceConfig(ctx, id)
	if err != nil || cfg.Revision != intent.Revision {
		return false
	}
	routes, err := store.SelectRouteCandidates(intent.Model)
	if err != nil {
		return false
	}
	routes, err = s.filterProviderAccountRoutesByModel(intent.Model, routes)
	if err != nil {
		return false
	}
	routes, _, err = s.resolveScopedRoutingPolicy(CallContext{Project: project, Key: key, Model: Model{Name: intent.Model}}, routes)
	if err != nil {
		return false
	}
	ids := []string{}
	current := map[string]string{}
	for _, route := range routes {
		if !routeMatchesProject(route.Route, project.ID) {
			continue
		}
		if route.Resource == nil {
			return false
		}
		original, allowed := intent.Candidates[route.Resource.ID]
		if !allowed || original != route.ProviderModel {
			return false
		}
		current[route.Resource.ID] = route.ProviderModel
		ids = append(ids, route.Resource.ID)
	}
	if _, allowed := current[id]; !allowed {
		return false
	}
	data, _, err := store.loadAccountRoutingData(ctx, ids, nil)
	if err != nil {
		return false
	}
	for candidate, model := range current {
		entry := data[candidate]
		decision := allowance.Evaluate(entry.Allowance, entry.Policy, model, now, accountAllowanceMaxAge)
		if decision.Held {
			if candidate == id {
				return false
			}
			continue
		}
		if decision.View.Status != allowance.Exhausted {
			return false
		}
	}
	return true
}

func (s *Server) runAllowanceReset(ctx context.Context, resource ProviderResource, cfg providerAllowanceConfig, card allowance.ResetCard, reason string, count int, now time.Time) error {
	store, ok := s.store.(*GormStore)
	if !ok {
		return nil
	}
	return store.RunClusterOperation(ctx, "allowance-auto-reset:"+resource.ID, func(ctx context.Context) error {
		current, err := store.providerAllowanceConfig(ctx, resource.ID)
		if err != nil {
			return err
		}
		if current.Revision != cfg.Revision {
			return nil
		}
		actual, exists := s.store.GetProviderResource(resource.ID)
		if !exists || !sameProviderAccountSnapshot(actual, resource) {
			return nil
		}
		data, _, err := store.loadAccountRoutingData(ctx, []string{resource.ID}, nil)
		if err != nil {
			return err
		}
		if allowance.ResetHeldByCap(data[resource.ID].Allowance, current.Policy, time.Now().UTC(), accountAllowanceMaxAge) {
			return nil
		}
		if reason == "weekly_exhausted" && !s.allowanceResetIntentCurrent(ctx, resource.ID, time.Now().UTC()) {
			return nil
		}
		sum := sha256.Sum256([]byte(resource.ID + "\x00" + resource.CreatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + card.ID))
		eventID := fmt.Sprintf("auto-reset-%x", sum[:])
		var existing providerAllowanceEvent
		if store.db.WithContext(ctx).First(&existing, "id = ?", eventID).Error == nil {
			return nil
		}
		limit := cfg.Policy.Reset.WeeklyLimit
		if limit == 0 {
			limit = 1
		}
		// A reservation consumes the weekly bound even if its outcome is unknown.
		// A crash between reservation and sending never causes another spend.
		monday := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(int(now.UTC().Weekday())+6)%7)
		reserved := false
		err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var n int64
			if err := tx.Model(&providerAllowanceEvent{}).Where("resource_id = ? AND kind = ? AND created_at >= ?", resource.ID, "automatic_reset", monday).Count(&n).Error; err != nil {
				return err
			}
			if n >= int64(limit) {
				return nil
			}
			row := providerAllowanceEvent{ID: eventID, ResourceID: resource.ID, Kind: "automatic_reset", Cycle: monday.Format(time.DateOnly), State: "pending", CreatedAt: now, UpdatedAt: now, Details: reason}
			insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			reserved = insert.RowsAffected == 1
			return insert.Error
		})
		if err != nil || !reserved {
			return err
		}
		result, err := s.resetOpenAIAccountQuota(ctx, resource.ID, openAIAccountQuotaResetRequest{Confirm: true, IdempotencyKey: eventID, ExpectedAvailableCount: &count, CreditID: &card.ID})
		state, details := "succeeded", result.Code
		if err != nil {
			state = "unknown"
			details = AsHTTPError(err).Code
		}
		update := store.db.WithContext(ctx).Model(&providerAllowanceEvent{}).Where("id = ?", eventID).Updates(map[string]any{"state": state, "details": details, "updated_at": time.Now().UTC()}).Error
		if err != nil {
			return err
		}
		if update != nil {
			return update
		}
		_, _ = s.queryOpenAIAccountQuotaCached(ctx, resource.ID, true)
		return nil
	})
}
