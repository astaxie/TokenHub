package server

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
)

const accountRoutingStateKind = "account-routing-state"

func (s *GormStore) loadAccountRoutingData(ctx context.Context, resourceIDs, poolIDs []string) (map[string]accountRoutingData, map[string]accountrouting.State, error) {
	data := map[string]accountRoutingData{}
	states := map[string]accountrouting.State{}
	if len(resourceIDs) == 0 {
		return data, states, nil
	}
	err := eachRouteCandidateBatch(resourceIDs, func(ids []string) error {
		var observations []ProviderResourceObservation
		if err := s.db.WithContext(ctx).Where("resource_id IN ?", ids).Find(&observations).Error; err != nil {
			return err
		}
		for _, o := range observations {
			fetched := time.Time{}
			if o.QuotaFetchedAt != nil {
				fetched = *o.QuotaFetchedAt
			}
			data[o.ResourceID] = accountRoutingData{Allowance: allowance.Read([]byte(o.QuotaSnapshot), fetched)}
		}
		var rows []struct {
			ProviderResourceID string
			Tokens             int64
		}
		if err := s.db.WithContext(ctx).Model(&RouteAttemptLog{}).Select("provider_resource_id, SUM(total_tokens) AS tokens").Where("provider_resource_id IN ? AND invoked = ? AND created_at >= ?", ids, true, time.Now().UTC().Add(-time.Hour)).Group("provider_resource_id").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			value := data[row.ProviderResourceID]
			value.RecentTokens = max(int64(0), row.Tokens)
			data[row.ProviderResourceID] = value
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	err = eachRouteCandidateBatch(poolIDs, func(ids []string) error {
		var rows []AdminResource
		if err := s.db.WithContext(ctx).Where("kind = ? AND id IN ?", accountRoutingStateKind, ids).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			current, _ := row.Fields["current"].(string)
			past, _ := row.Fields["past"].(bool)
			revision, _ := row.Fields["revision"].(string)
			states[row.ID] = accountrouting.State{Current: current, Past: past, Revision: revision}
		}
		return nil
	})
	return data, states, err
}

// State is shared across backend instances. Comparing the state captured by the
// plan prevents an older in-flight request from rewinding a newer drain choice.
// Simulation only reads state; it never calls this execution-only operation.
func (s *GormStore) recordAccountRoutingAttempt(ctx context.Context, attempt accountRoutingAttempt, resourceID string, exhausted bool) (accountrouting.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var state accountrouting.State
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "account_routing", attempt.PoolID); err != nil {
			return err
		}
		var row AdminResource
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND id = ?", accountRoutingStateKind, attempt.PoolID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		current, _ := row.Fields["current"].(string)
		past, _ := row.Fields["past"].(bool)
		revision, _ := row.Fields["revision"].(string)
		state = accountrouting.State{Current: current, Past: past, Revision: revision}
		if exhausted {
			if current != resourceID || past || revision != attempt.Expected.Revision {
				return nil
			}
			past = true
		} else {
			if state != attempt.Expected {
				return nil
			}
			if current == attempt.Initial && !past {
				return nil
			}
			current, past = attempt.Initial, false
		}
		if row.ID == "" {
			row = AdminResource{ID: attempt.PoolID, Kind: accountRoutingStateKind, Name: "Account routing state", Status: StatusActive, CreatedAt: time.Now().UTC()}
		}
		state = accountrouting.State{Current: current, Past: past, Revision: NewID("acr")}
		row.Fields = map[string]any{"current": current, "past": past, "revision": state.Revision}
		row.UpdatedAt = time.Now().UTC()
		return tx.Save(&row).Error
	})
	return state, err
}
