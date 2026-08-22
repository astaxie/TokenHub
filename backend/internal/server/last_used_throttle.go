package server

import (
	"sync"
	"sync/atomic"
	"time"
)

// The last_used_at columns on API keys, model routes and provider resources are
// display-only: the admin console renders them as "last used" and nothing in
// authentication, quota accounting or routing reads them back. Refreshing them
// on every gateway request meant up to three extra write transactions per
// request, each taking the store mutex, so they are throttled to one write per
// object per lastUsedThrottleWindow.
//
// Replicas throttle independently, so N replicas can each write once per window
// per object. Nothing orders those writes: a slow write can land after a newer
// one and move the stored value backwards, by at most one window plus the
// write's own latency. A "last used" column tolerates that, and making the
// update conditional (only overwrite an older timestamp) was considered and
// rejected — it buys nothing a reader would notice.
const (
	// lastUsedThrottleWindow bounds how stale a persisted last_used_at may be.
	lastUsedThrottleWindow = time.Minute
	// lastUsedThrottleFailureBackoff prevents a database outage from turning
	// every request into another write attempt and log line. Failed objects are
	// retried after the normal throttle window.
	lastUsedThrottleFailureBackoff = lastUsedThrottleWindow
	// lastUsedThrottleEntryTTL is how long an idle entry survives a sweep. It
	// must stay well above lastUsedThrottleWindow so sweeping never drops an
	// entry that is still suppressing writes.
	lastUsedThrottleEntryTTL = 10 * time.Minute
	// lastUsedThrottleMaxEntries is a hard ceiling on tracked objects. Past it
	// the throttle stops tracking new keys and writes them through instead, so
	// memory is capped no matter how many distinct objects appear. Sweeps are
	// therefore also bounded: one scan of at most this many entries, at most
	// once per lastUsedThrottleSweepInterval, and only while the map is full.
	lastUsedThrottleMaxEntries = 100_000
	// lastUsedThrottleSweepInterval keeps the O(entries) sweep off the hot path
	// when the map is full but every entry is too fresh to drop.
	lastUsedThrottleSweepInterval = time.Minute
)

// Keys are namespaced because the three object kinds share one map and their
// IDs are only unique within a kind.
func lastUsedAPIKeyKey(id string) string { return "key:" + id }

func lastUsedRouteKey(id string) string { return "route:" + id }

func lastUsedResourceKey(id string) string { return "resource:" + id }

// lastUsedWriting is the placeholder stored while a write is in flight. It is a
// distinct type rather than a sentinel time so a committed timestamp can never
// be mistaken for a write in progress, and it is pre-boxed so storing it neither
// allocates nor produces two interface values that compare unequal.
type lastUsedWriting struct{}

var lastUsedWriteInFlight any = lastUsedWriting{}

// lastUsedFailedAt records a completed write attempt that failed. Keeping it
// distinct from a successful timestamp makes the retry policy explicit while
// still letting sweeps reclaim the entry after it becomes idle.
type lastUsedFailedAt struct {
	at time.Time
}

// lastUsedClaim is one caller's right to write a key. An untracked claim means
// the throttle was full and declined to remember the key: the caller still
// writes, it just gets no throttling.
type lastUsedClaim struct {
	key     string
	tracked bool
}

// lastUsedThrottle decides which last_used_at writes are worth making. It holds
// no lock of its own: entries is a sync.Map and writers claim a key by swapping
// in the in-flight placeholder.
type lastUsedThrottle struct {
	entries        *sync.Map // key string -> time.Time, lastUsedFailedAt or lastUsedWriting
	now            func() time.Time
	window         time.Duration
	failureBackoff time.Duration
	entryTTL       time.Duration
	sweepEvery     time.Duration
	maxEntries     int64
	count          atomic.Int64
	nextSweep      atomic.Int64 // unix nanos; the CAS on it single-flights sweeps
}

func newLastUsedThrottle() *lastUsedThrottle {
	return &lastUsedThrottle{
		entries: &sync.Map{},
		// Deliberately not time.Now().UTC(): stripping the monotonic reading
		// would let a backwards system-clock jump suppress writes for far
		// longer than the window. The timestamps that reach the database are
		// the callers' own, not this clock.
		now:            time.Now,
		window:         lastUsedThrottleWindow,
		failureBackoff: lastUsedThrottleFailureBackoff,
		entryTTL:       lastUsedThrottleEntryTTL,
		sweepEvery:     lastUsedThrottleSweepInterval,
		maxEntries:     lastUsedThrottleMaxEntries,
	}
}

// mark runs write at most once per window for key and returns whatever write
// returned. A skipped write reports nil: callers treat "someone else refreshed
// this recently" as success. A failed or panicking write records a failed-at
// state, so another attempt and its corresponding log cannot occur until the
// failure backoff expires.
//
// A nil throttle writes unconditionally, so a store built without one degrades
// to the previous behaviour rather than panicking.
func (t *lastUsedThrottle) mark(key string, write func() error) error {
	if t == nil {
		return write()
	}
	claim, ok := t.shouldWrite(key)
	if !ok {
		return nil
	}
	// Every path that is not a successful write, panics included, replaces the
	// in-flight placeholder with a failed-at state. Without that defer a panic
	// would suppress this object's writes until the process restarts.
	committed := false
	defer func() {
		if !committed {
			t.fail(claim, t.now())
		}
	}()
	if err := write(); err != nil {
		return err
	}
	// The window is measured from completion, not from the start of the write,
	// so a slow write does not let the next one fire right after it.
	t.commit(claim, t.now())
	committed = true
	return nil
}

// shouldWrite claims key for the caller. Claiming is a compare-and-swap so that
// a burst of concurrent requests for one object produces a single write rather
// than one per request.
func (t *lastUsedThrottle) shouldWrite(key string) (lastUsedClaim, bool) {
	now := t.now()
	current, loaded := t.entries.Load(key)
	if !loaded {
		if !t.admit(now) {
			return lastUsedClaim{key: key}, true
		}
		if current, loaded = t.entries.LoadOrStore(key, lastUsedWriteInFlight); !loaded {
			return lastUsedClaim{key: key, tracked: true}, true
		}
		// Another goroutine created the entry first. Hand the reservation back
		// and judge the entry it left behind.
		t.count.Add(-1)
	}
	var attemptedAt time.Time
	var backoff time.Duration
	switch state := current.(type) {
	case time.Time:
		attemptedAt = state
		backoff = t.window
	case lastUsedFailedAt:
		attemptedAt = state.at
		backoff = t.failureBackoff
	default:
		// Another goroutine is writing this key right now.
		return lastUsedClaim{}, false
	}
	if now.Sub(attemptedAt) < backoff {
		return lastUsedClaim{}, false
	}
	if !t.entries.CompareAndSwap(key, current, lastUsedWriteInFlight) {
		// Another writer claimed the key, or a sweep dropped it, between the
		// load and the swap. Skip this round; the next request re-evaluates.
		return lastUsedClaim{}, false
	}
	return lastUsedClaim{key: key, tracked: true}, true
}

func (t *lastUsedThrottle) commit(claim lastUsedClaim, completedAt time.Time) {
	if !claim.tracked {
		return
	}
	t.entries.Store(claim.key, completedAt)
}

func (t *lastUsedThrottle) fail(claim lastUsedClaim, failedAt time.Time) {
	if !claim.tracked {
		return
	}
	t.entries.Store(claim.key, lastUsedFailedAt{at: failedAt})
}

// admit reserves room for one new entry. At the ceiling it sweeps once and
// retries; if that frees nothing it declines, and the caller writes untracked.
// Declining rather than growing is what makes the ceiling real.
func (t *lastUsedThrottle) admit(now time.Time) bool {
	if t.reserve() {
		return true
	}
	t.maybeSweep(now)
	return t.reserve()
}

func (t *lastUsedThrottle) reserve() bool {
	for {
		count := t.count.Load()
		if count >= t.maxEntries {
			return false
		}
		if t.count.CompareAndSwap(count, count+1) {
			return true
		}
	}
}

// maybeSweep rate-limits sweeps rather than strictly serializing them: the CAS
// only decides who starts one, so a very slow sweep can still overlap the next.
// The schedule reads the wall clock, so a large backwards clock jump delays the
// next sweep — which costs write-through requests, never correctness.
func (t *lastUsedThrottle) maybeSweep(now time.Time) {
	next := t.nextSweep.Load()
	if now.UnixNano() < next {
		return
	}
	if !t.nextSweep.CompareAndSwap(next, now.Add(t.sweepEvery).UnixNano()) {
		return // another goroutine just claimed this slot
	}
	t.sweep(now)
}

// sweep drops entries no request has refreshed for entryTTL, which is how
// deleted keys, routes and resources give their slot back. Successful and
// failed-at states are both eligible. Reclamation is pressure-driven: an idle
// entry is only dropped once the map is full, which is the only time the space
// is worth anything. In-flight placeholders are left alone so the writer can
// still record its outcome.
func (t *lastUsedThrottle) sweep(now time.Time) {
	cutoff := now.Add(-t.entryTTL)
	t.entries.Range(func(key, value any) bool {
		var attemptedAt time.Time
		switch state := value.(type) {
		case time.Time:
			attemptedAt = state
		case lastUsedFailedAt:
			attemptedAt = state.at
		default:
			return true
		}
		if attemptedAt.After(cutoff) {
			return true
		}
		if t.entries.CompareAndDelete(key, value) {
			t.count.Add(-1)
		}
		return true
	})
}
