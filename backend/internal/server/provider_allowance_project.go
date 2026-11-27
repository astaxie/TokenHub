package server

import (
	"net/http"
	"strconv"
	"time"

	"tokenhub/backend/internal/allowance"
)

type projectAllowanceSummary struct {
	Model       string           `json:"model"`
	Status      allowance.Status `json:"status"`
	Available   int              `json:"available"`
	QueryErrors int              `json:"query_error"`
	Unknown     int              `json:"unknown"`
	Exhausted   int              `json:"exhausted"`
	Held        int              `json:"held"`
	NextResetAt *time.Time       `json:"next_reset_at,omitempty"`
	CheckedAt   time.Time        `json:"checked_at"`
}

// API keys can read aggregate availability only for their authorized routes.
// Supplier identities, wallet amounts, configuration and other tenants' routing
// are never included. Waiting observes caches; it never queries a provider.
func (s *Server) handleProjectAllowanceGet(w http.ResponseWriter, r *http.Request) {
	wait, err := strconv.Atoi(firstNonEmpty(r.URL.Query().Get("wait_seconds"), "0"))
	if err != nil || wait < 0 || wait > 60 {
		writeError(w, r, NewHTTPError(400, "invalid_quota_wait", "wait_seconds must be between 0 and 60"))
		return
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	if wait > 0 {
		_, key, err := s.authenticate(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		release, accepted := s.acquireAllowanceWaiter(key.ID)
		if !accepted {
			w.Header().Set("Retry-After", "5")
			writeError(w, r, NewHTTPError(429, "allowance_wait_busy", "Too many quota waits; retry later"))
			return
		}
		defer release()
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		project, key, err := s.authenticate(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if len(modelAccessReasons(project, key, model)) > 0 {
			writeError(w, r, NewHTTPError(403, "model_not_allowed", "Model is not allowed for this credential"))
			return
		}
		routes, err := s.store.SelectRouteCandidates(model)
		if err != nil {
			writeError(w, r, err)
			return
		}
		routes, err = s.filterProviderAccountRoutesByModel(model, routes)
		if err != nil {
			writeError(w, r, err)
			return
		}
		routes, _, err = s.resolveScopedRoutingPolicy(CallContext{Project: project, Key: key, Model: Model{Name: model}}, routes)
		if err != nil {
			writeError(w, r, err)
			return
		}
		var ids []string
		seen := map[string]bool{}
		for _, route := range routes {
			if route.Resource != nil && routeMatchesProject(route.Route, project.ID) && !seen[route.Resource.ID] {
				ids = append(ids, route.Resource.ID)
				seen[route.Resource.ID] = true
			}
		}
		store, ok := s.store.(accountRoutingStore)
		if !ok {
			writeError(w, r, NewHTTPError(503, "allowance_store_unavailable", "Allowance cache is unavailable"))
			return
		}
		data, _, err := store.loadAccountRoutingData(r.Context(), ids, nil)
		if err != nil {
			writeError(w, r, err)
			return
		}
		now := time.Now().UTC()
		summary := projectAllowanceSummary{Model: model, Status: allowance.Unknown, CheckedAt: now}
		seen = map[string]bool{}
		for _, route := range routes {
			if route.Resource == nil || !routeMatchesProject(route.Route, project.ID) || seen[route.Resource.ID+"\x00"+route.ProviderModel] {
				continue
			}
			seen[route.Resource.ID+"\x00"+route.ProviderModel] = true
			entry := data[route.Resource.ID]
			decision := allowance.Evaluate(entry.Allowance, entry.Policy, route.ProviderModel, now, accountAllowanceMaxAge)
			if decision.Held {
				summary.Held++
				continue
			}
			switch decision.View.Status {
			case allowance.Available:
				summary.Available++
			case allowance.Exhausted:
				summary.Exhausted++
			case allowance.QueryError:
				summary.QueryErrors++
			default:
				summary.Unknown++
			}
			for _, reset := range decision.View.Renewals {
				if reset.After(now) && (summary.NextResetAt == nil || reset.Before(*summary.NextResetAt)) {
					copy := reset
					summary.NextResetAt = &copy
				}
			}
		}
		if summary.Available > 0 {
			summary.Status = allowance.Available
		} else if summary.Unknown == 0 && summary.QueryErrors > 0 {
			summary.Status = allowance.QueryError
		} else if summary.Unknown == 0 && (summary.Exhausted > 0 || summary.Held > 0) {
			summary.Status = allowance.Exhausted
		}
		if wait == 0 || summary.Available > 0 {
			writeJSON(w, 200, summary)
			return
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-deadline.C:
			timer.Stop()
			writeJSON(w, 200, summary)
			return
		case <-timer.C:
		}
	}
}
