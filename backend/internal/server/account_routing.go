package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
)

const accountAllowanceMaxAge = 10 * time.Minute

type accountRoutingAttempt struct {
	PoolID   string
	Initial  string
	Expected accountrouting.State
}

type accountRoutingData struct {
	Allowance    allowance.Snapshot
	RecentTokens int64
	Policy       allowance.Policy
}

type accountRoutingStore interface {
	loadAccountRoutingData(context.Context, []string, []string) (map[string]accountRoutingData, map[string]accountrouting.State, error)
	recordAccountRoutingAttempt(context.Context, accountRoutingAttempt, string, bool) (accountrouting.State, error)
}

// Only account positions within the same route and resource-priority tier move.
// The model strategy, Provider shares, project filtering, and route-rank hooks
// have already decided these slots; this layer never introduces a candidate.
func (s *Server) planAccountOrder(ctx context.Context, call CallContext, planned []RouteSelection) []RouteSelection {
	strategy := modelAccountStrategy(call.Model)

	store, ok := s.store.(accountRoutingStore)
	if !ok {
		return planned
	}
	planned = append([]RouteSelection(nil), planned...)
	groups := map[string][]int{}
	var resourceIDs []string
	for i, route := range planned {
		if route.Resource == nil {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", route.Route.ID, route.Provider.ID, route.ProviderModel, route.Route.Priority, routeResourcePriority(route))
		groups[key] = append(groups[key], i)
		resourceIDs = append(resourceIDs, route.Resource.ID)
	}
	poolIDs := map[string]string{}
	var pools []string
	for key, indexes := range groups {
		var ids []string
		for _, i := range indexes {
			ids = append(ids, planned[i].Resource.ID)
		}
		sort.Strings(ids)
		sum := sha256.Sum256([]byte(call.Project.ID + "\x00" + key + "\x00" + strings.Join(ids, "\x00")))
		poolID := fmt.Sprintf("ars_%x", sum[:])
		poolIDs[key] = poolID
		if strategy == accountrouting.Drain {
			pools = append(pools, poolID)
		}
	}
	data, states, err := store.loadAccountRoutingData(ctx, resourceIDs, pools)
	if err != nil {
		log.Printf("[tokenhub] account routing cache unavailable: %s", AsHTTPError(err).Code)
		return nil
	}
	now := time.Now().UTC()
	filtered := make([]RouteSelection, 0, len(planned))
	for _, route := range planned {
		if route.Resource != nil {
			entry := data[route.Resource.ID]
			if allowance.Evaluate(entry.Allowance, entry.Policy, route.ProviderModel, now, accountAllowanceMaxAge).Held {
				continue
			}
		}
		filtered = append(filtered, route)
	}
	planned = filtered
	groups = map[string][]int{}
	for i, route := range planned {
		if route.Resource == nil {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", route.Route.ID, route.Provider.ID, route.ProviderModel, route.Route.Priority, routeResourcePriority(route))
		groups[key] = append(groups[key], i)
	}
	for key, indexes := range groups {
		seats := make([]accountrouting.Candidate, len(indexes))
		original := make([]RouteSelection, len(indexes))
		for i, index := range indexes {
			original[i] = planned[index]
			id := planned[index].Resource.ID
			entry := data[id]
			seats[i] = accountrouting.Candidate{ID: id, Quota: allowance.Evaluate(entry.Allowance, entry.Policy, planned[index].ProviderModel, now, accountAllowanceMaxAge).View, RecentTokens: entry.RecentTokens}
		}
		poolID := poolIDs[key]
		order := accountrouting.Order(strategy, seats, states[poolID])
		sort.SliceStable(order, func(i, j int) bool {
			a, b := data[seats[order[i]].ID], data[seats[order[j]].ID]
			return allowance.Evaluate(a.Allowance, a.Policy, original[order[i]].ProviderModel, now, accountAllowanceMaxAge).CreditRank < allowance.Evaluate(b.Allowance, b.Policy, original[order[j]].ProviderModel, now, accountAllowanceMaxAge).CreditRank
		})
		for i, from := range order {
			selected := original[from]
			if strategy == accountrouting.Drain {
				selected.AccountRouting = &accountRoutingAttempt{PoolID: poolID, Initial: seats[order[0]].ID, Expected: states[poolID]}
			}
			planned[indexes[i]] = selected
		}
	}
	return planned
}

func recordAccountRoutingOutcome(ctx context.Context, store Store, route RouteSelection, exhausted bool) {
	if route.AccountRouting == nil || route.Resource == nil {
		return
	}
	if persistence, ok := store.(accountRoutingStore); ok {
		state, err := persistence.recordAccountRoutingAttempt(ctx, *route.AccountRouting, route.Resource.ID, exhausted)
		if err != nil {
			log.Printf("[tokenhub] account routing state unavailable: %s", AsHTTPError(err).Code)
		} else if !exhausted {
			route.AccountRouting.Expected = state
		}
	}
}
