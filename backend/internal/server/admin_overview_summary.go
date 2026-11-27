package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type OverviewSummaryStatus struct {
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Processed int64     `json:"processed,omitempty"`
}

type overviewSummaryEntry struct {
	summary UsageSummary
	status  OverviewSummaryStatus
	expires time.Time
}

type overviewSummaryFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	entry   overviewSummaryEntry
	err     error
}

type overviewSummaryCache struct {
	mu      sync.Mutex
	entries map[string]overviewSummaryEntry
	flights map[string]*overviewSummaryFlight
}

func (s *Server) overviewSummaryForUser(ctx context.Context, user AdminUser) (map[string]any, OverviewSummaryStatus) {
	scoped := s.usageQueryServer(ctx)
	query := scoped.usageSummaryQueryForUser(user)
	for _, scope := range []*UsageSummaryScope{&query.UsageRecords, &query.RequestLogs} {
		sort.Strings(scope.AttributedUserIDs)
		sort.Strings(scope.ProjectIDs)
		sort.Strings(scope.APIKeyIDs)
	}
	encoded, _ := json.Marshal(query)
	key := user.ID + ":" + user.Role + ":" + string(encoded)
	entry, err := s.overviewSummaries.get(ctx, key, func(ctx context.Context) (overviewSummaryEntry, error) {
		summary, progress, err := s.store.QueryOverviewSummary(ctx, query)
		if err != nil {
			return overviewSummaryEntry{}, err
		}
		if math.IsNaN(summary.EstimatedCostUSD) || math.IsInf(summary.EstimatedCostUSD, 0) {
			return overviewSummaryEntry{}, fmt.Errorf("overview amount is outside the response range")
		}
		now := time.Now().UTC()
		entry := overviewSummaryEntry{summary: summary, status: OverviewSummaryStatus{State: "ready", UpdatedAt: now}, expires: now.Add(15 * time.Second)}
		if !progress.Ready {
			entry.status = OverviewSummaryStatus{State: "preparing"}
			if query.UsageRecords.Global && query.RequestLogs.Global {
				entry.status.Processed = progress.Processed
			}
			entry.expires = now.Add(time.Second)
		}
		return entry, nil
	})
	if err != nil {
		return nil, OverviewSummaryStatus{State: "error"}
	}
	if entry.status.State == "preparing" {
		return nil, entry.status
	}
	return usageSummaryPayload(entry.summary), entry.status
}

func (c *overviewSummaryCache) get(ctx context.Context, key string, load func(context.Context) (overviewSummaryEntry, error)) (overviewSummaryEntry, error) {
	c.mu.Lock()
	cached, found := c.entries[key]
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return cached.onRefreshFailure(err)
	}
	if found && time.Now().Before(cached.expires) {
		c.mu.Unlock()
		return cached, nil
	}
	if c.flights == nil {
		c.flights = make(map[string]*overviewSummaryFlight)
	}
	flight := c.flights[key]
	if flight == nil {
		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maximumUsageQueryDuration)
		flight = &overviewSummaryFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = flight
		go func() {
			entry, err := load(queryCtx)
			if err == nil {
				err = queryCtx.Err()
			}
			cancel()
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.flights[key] == flight {
				delete(c.flights, key)
				if err == nil {
					if c.entries == nil {
						c.entries = make(map[string]overviewSummaryEntry)
					}
					// Entries contain aggregates only; cap distinct permission scopes.
					if len(c.entries) >= 128 {
						clear(c.entries)
					}
					c.entries[key] = entry
				}
			}
			flight.entry, flight.err = entry, err
			close(flight.done)
		}()
	}
	flight.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		flight.waiters--
		if flight.waiters == 0 && c.flights[key] == flight {
			delete(c.flights, key)
			flight.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return cached.onRefreshFailure(ctx.Err())
	case <-flight.done:
		if ctx.Err() == context.Canceled {
			return overviewSummaryEntry{}, ctx.Err()
		}
		if flight.err != nil {
			return cached.onRefreshFailure(flight.err)
		}
		return flight.entry, nil
	}
}

func (cached overviewSummaryEntry) onRefreshFailure(err error) (overviewSummaryEntry, error) {
	// The page deadline can expire before the shared refresh's own timeout.
	// Each waiter applies the same freshness rule when it stops waiting.
	if err != context.Canceled && cached.status.State == "ready" && time.Since(cached.status.UpdatedAt) <= time.Minute {
		cached.status.State = "stale"
		return cached, nil
	}
	return overviewSummaryEntry{}, err
}
