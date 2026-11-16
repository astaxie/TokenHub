package server

import (
	"context"
	"time"

	"tokenhub/backend/internal/allowance"
)

func (s *Server) refreshCarefulAllowances(ctx context.Context, routes []RouteSelection, data map[string]accountRoutingData) bool {
	needed := false
	for _, entry := range data {
		if entry.Policy.CapCareful {
			needed = true
			break
		}
	}
	if !needed {
		return false
	}
	changed := false
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	seen := map[string]bool{}
	for _, route := range routes {
		if route.Resource == nil || seen[route.Resource.ID] {
			continue
		}
		seen[route.Resource.ID] = true
		entry := data[route.Resource.ID]
		now := time.Now().UTC()
		if !allowance.CarefulNeedsRefresh(entry.Allowance, entry.Policy, route.ProviderModel, now) {
			continue
		}
		if entry.Allowance.Status == allowance.QueryError && now.Sub(entry.Allowance.CheckedAt) < 15*time.Second {
			continue
		}
		_ = s.store.RunClusterOperation(ctx, "provider-careful:"+route.Resource.ID, func(ctx context.Context) error {
			store, ok := s.store.(*GormStore)
			if !ok {
				return nil
			}
			current, _, err := store.loadAccountRoutingData(ctx, []string{route.Resource.ID}, nil)
			if err != nil {
				return err
			}
			entry := current[route.Resource.ID]
			if !allowance.CarefulNeedsRefresh(entry.Allowance, entry.Policy, route.ProviderModel, time.Now().UTC()) {
				return nil
			}
			if entry.Allowance.Status == allowance.QueryError && time.Since(entry.Allowance.CheckedAt) < 15*time.Second {
				return nil
			}
			changed = true
			_, handled, err := s.refreshConfiguredAllowance(ctx, *route.Resource, true)
			if !handled {
				_, err = s.queryProviderMonitoringQuota(ctx, route.Provider, *route.Resource)
			}
			return err
		})
		if ctx.Err() != nil {
			break
		}
	}
	return changed
}
