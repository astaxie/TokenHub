package server

import (
	"sync"
	"time"
)

const (
	// modelLabelTTL bounds how stale a cached catalog snapshot may become. Model
	// changes made through this process invalidate the snapshot immediately;
	// changes made by another replica are picked up within one TTL. The snapshot
	// only decides whether a model name is safe to use as a metrics label — it
	// takes no part in authentication or routing — so a label that lags the
	// catalog by up to one TTL is acceptable.
	modelLabelTTL = 60 * time.Second
	// modelLabelRetryBackoff keeps a database outage from turning every rejected
	// request back into a query: a failed refresh is not retried before it elapses.
	modelLabelRetryBackoff = 5 * time.Second
)

// modelLabelCache holds the set of model names the catalog knows, so bounding a
// metrics label to that set costs a map lookup instead of a query per request.
type modelLabelCache struct {
	mu sync.Mutex
	// names stays nil until a refresh has succeeded at least once.
	names       map[string]struct{}
	fetchedAt   time.Time
	nextAttempt time.Time
}

func newModelLabelCache() *modelLabelCache {
	return &modelLabelCache{}
}

// invalidate marks the snapshot stale so the next lookup reloads it. Callers must
// not hold an open database transaction: a refresh already in flight runs its
// query while holding the same mutex.
func (c *modelLabelCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.fetchedAt = time.Time{}
	c.nextAttempt = time.Time{}
	c.mu.Unlock()
}

// lookup reports whether name is a known model, refreshing the snapshot through
// load when it is missing or older than modelLabelTTL. The second result is false
// only when there is no cache to consult at all: a cache whose refresh is failing
// answers from its last snapshot, or reports the model as unknown when it has
// none, rather than sending the caller back to a database it just failed to reach.
func (c *modelLabelCache) lookup(name string, clock func() time.Time, load func() ([]string, error)) (bool, bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := clock()
	if c.stale(now) && !now.Before(c.nextAttempt) {
		names, err := load()
		// A failing database usually fails slowly, so both stamps are read after
		// the query returns. Timing them from before it would let a load that took
		// longer than the interval hand back a window that has already elapsed.
		completed := clock()
		if err != nil {
			// Serving a stale snapshot is strictly better than widening the label
			// set, so the previous one is kept and only the retry is delayed.
			c.nextAttempt = completed.Add(modelLabelRetryBackoff)
		} else {
			set := make(map[string]struct{}, len(names))
			for _, modelName := range names {
				set[modelName] = struct{}{}
			}
			c.names = set
			c.fetchedAt = completed
			c.nextAttempt = time.Time{}
		}
	}
	if c.names == nil {
		// The catalog has never been read. Reporting the model as unknown keeps the
		// label bounded and, unlike a query per call, does not let a client hammering
		// invented names pile load onto a database that is already failing.
		return false, true
	}
	_, known := c.names[name]
	return known, true
}

func (c *modelLabelCache) stale(now time.Time) bool {
	return c.names == nil || now.Sub(c.fetchedAt) >= modelLabelTTL
}
