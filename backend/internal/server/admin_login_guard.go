package server

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Failed admin sign-ins are limited per source address and per account. Only
// failures and in-flight attempts consume budget, and a successful sign-in
// clears the account budget, so ordinary use cannot lock a user out while a
// password guessing loop still stalls.
const (
	adminLoginWindow       = time.Minute
	adminLoginSourceLimit  = 20
	adminLoginAccountLimit = 5
	// Bounds memory if an attacker rotates source addresses faster than the
	// window expires. Existing buckets are never evicted to admit a new one.
	adminLoginMaxBuckets = 10000
)

type adminLoginBucket struct {
	failures int
	inflight int
	expires  time.Time
}

func (b adminLoginBucket) used() int {
	return b.failures + b.inflight
}

// adminLoginLimiter keeps short-lived counters with no background goroutine:
// expired buckets are dropped opportunistically, at most once per window.
type adminLoginLimiter struct {
	mu        sync.Mutex
	buckets   map[string]adminLoginBucket
	nextSweep time.Time
}

func adminLoginKeys(source, identity string) [2]string {
	return [2]string{
		"source:" + HashSecret(source),
		"account:" + HashSecret(strings.ToLower(strings.TrimSpace(identity))),
	}
}

// live returns the bucket, dropping it first when the window has passed.
func (l *adminLoginLimiter) live(key string, now time.Time) (adminLoginBucket, bool) {
	bucket, ok := l.buckets[key]
	if ok && !now.Before(bucket.expires) {
		delete(l.buckets, key)
		return adminLoginBucket{}, false
	}
	return bucket, ok
}

func (l *adminLoginLimiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	for key, bucket := range l.buckets {
		if !now.Before(bucket.expires) {
			delete(l.buckets, key)
		}
	}
	l.nextSweep = now.Add(adminLoginWindow)
}

type adminLoginReservation struct {
	limiter   *adminLoginLimiter
	keys      [2]string
	at        time.Time
	accounted bool
}

// reserve admits the attempt and counts it as in flight before the password
// check, so parallel requests cannot all slip past the threshold at once. A
// non-zero wait means the attempt was rejected instead.
func (l *adminLoginLimiter) reserve(source, identity string, now time.Time) (adminLoginReservation, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = make(map[string]adminLoginBucket)
	}
	l.sweep(now)
	keys := adminLoginKeys(source, identity)
	limits := [2]int{adminLoginSourceLimit, adminLoginAccountLimit}
	for index, key := range keys {
		if bucket, _ := l.live(key, now); bucket.used() >= limits[index] {
			return adminLoginReservation{}, bucket.expires.Sub(now)
		}
	}
	for _, key := range keys {
		bucket, ok := l.live(key, now)
		if !ok {
			if len(l.buckets) >= adminLoginMaxBuckets {
				return adminLoginReservation{}, adminLoginWindow
			}
			bucket = adminLoginBucket{}
		}
		bucket.inflight++
		bucket.expires = now.Add(adminLoginWindow)
		l.buckets[key] = bucket
	}
	return adminLoginReservation{limiter: l, keys: keys, at: now, accounted: true}, 0
}

func (r *adminLoginReservation) release() {
	if r.limiter == nil || !r.accounted {
		return
	}
	r.accounted = false
	r.limiter.mu.Lock()
	defer r.limiter.mu.Unlock()
	now := r.at
	for _, key := range r.keys {
		if bucket, ok := r.limiter.live(key, now); ok && bucket.inflight > 0 {
			bucket.inflight--
			r.limiter.buckets[key] = bucket
		}
	}
}

// fail converts the in-flight slot into a failure and restarts both windows
// from now, so pacing attempts just under the limit cannot outlast a counter.
func (r *adminLoginReservation) fail() {
	if r.limiter == nil {
		return
	}
	r.limiter.mu.Lock()
	defer r.limiter.mu.Unlock()
	r.accounted = false
	now := r.at
	for _, key := range r.keys {
		bucket, ok := r.limiter.live(key, now)
		if !ok {
			if len(r.limiter.buckets) >= adminLoginMaxBuckets {
				continue
			}
			bucket = adminLoginBucket{}
		} else if bucket.inflight > 0 {
			bucket.inflight--
		}
		bucket.failures++
		bucket.expires = now.Add(adminLoginWindow)
		r.limiter.buckets[key] = bucket
	}
}

// succeed clears the account budget. The source budget keeps its failures:
// one working account must not buy unlimited attempts against the others.
func (r *adminLoginReservation) succeed() {
	if r.limiter == nil {
		return
	}
	r.limiter.mu.Lock()
	defer r.limiter.mu.Unlock()
	r.accounted = false
	now := r.at
	if bucket, ok := r.limiter.live(r.keys[1], now); ok {
		if bucket.inflight > 0 {
			bucket.inflight--
		}
		if bucket.inflight == 0 {
			delete(r.limiter.buckets, r.keys[1])
		} else {
			r.limiter.buckets[r.keys[1]] = bucket
		}
	}
	if bucket, ok := r.limiter.live(r.keys[0], now); ok && bucket.inflight > 0 {
		bucket.inflight--
		r.limiter.buckets[r.keys[0]] = bucket
	}
}

func (s *Server) beginAdminLogin(w http.ResponseWriter, r *http.Request, identity string) (adminLoginReservation, bool) {
	reservation, retryAfter := s.adminLoginLimits.reserve(s.clientIP(r), identity, time.Now())
	if reservation.limiter != nil {
		return reservation, true
	}
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	err := NewHTTPError(http.StatusTooManyRequests, "login_rate_limited", "Too many failed sign-in attempts; retry later")
	err.Headers = map[string]string{"Retry-After": strconv.Itoa(seconds)}
	writeError(w, r, err)
	return adminLoginReservation{}, false
}
