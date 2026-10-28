package server

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const subscriptionWorkerPoolLimit = 32
const subscriptionWorkerIdleLifetime = 10 * time.Minute

type subscriptionWorkerLease struct {
	process    *subscriptionWorkerProcess
	references int
	touched    time.Time
}

type subscriptionAccountGate struct {
	token      chan struct{}
	references int
}

type subscriptionWorkerPool struct {
	startOnce sync.Once
	accounts  map[string]*subscriptionAccountGate
	mu        sync.Mutex
	entries   map[string]*subscriptionWorkerLease
	sessions  map[string]*subscriptionWorkerAuthorization
	client    *http.Client
	start     func(*http.Client, string, func(context.Context, string, string) error) (*subscriptionWorkerProcess, error)
	stop      chan struct{}
	closed    bool
	closeDone chan struct{}
}

func newSubscriptionWorkerPool(client *http.Client) *subscriptionWorkerPool {
	p := &subscriptionWorkerPool{accounts: map[string]*subscriptionAccountGate{}, entries: map[string]*subscriptionWorkerLease{}, sessions: map[string]*subscriptionWorkerAuthorization{}, client: client, start: startSubscriptionWorker, stop: make(chan struct{})}
	return p
}

func (p *subscriptionWorkerPool) startEviction() {
	p.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-p.stop:
					return
				case now := <-ticker.C:
					p.evict(now)
				}
			}
		}()
	})
}

func (p *subscriptionWorkerPool) evict(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, session := range p.sessions {
		if !session.expires.After(now) {
			session.process.cancel()
			delete(p.sessions, key)
			if entry := p.entries[session.poolKey]; entry != nil {
				entry.references--
				entry.touched = now.Add(-subscriptionWorkerIdleLifetime)
			}
		}
	}
	for key, entry := range p.entries {
		if entry.references == 0 && now.Sub(entry.touched) >= subscriptionWorkerIdleLifetime {
			entry.process.cancel()
			delete(p.entries, key)
		}
	}
}

func (p *subscriptionWorkerPool) acquire(ctx context.Context, key, auth string, save func(context.Context, string, string) error) (*subscriptionWorkerProcess, func(), error) {
	p.startEviction()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, NewHTTPError(503, "subscription_runtime_closed", "Subscription runtime is shutting down")
	}
	entry := p.entries[key]
	if entry != nil {
		entry.process.mu.Lock()
		closed := entry.process.closed
		entry.process.mu.Unlock()
		if closed || entry.process.ctx.Err() != nil || entry.process.authSnapshot() != auth {
			entry.process.cancel()
			delete(p.entries, key)
			entry = nil
		}
	}
	if entry == nil {
		if len(p.entries) >= subscriptionWorkerPoolLimit {
			oldestKey := ""
			var oldest *subscriptionWorkerLease
			for candidate, item := range p.entries {
				if item.references == 0 && (oldest == nil || item.touched.Before(oldest.touched)) {
					oldestKey, oldest = candidate, item
				}
			}
			if oldest == nil {
				return nil, nil, NewHTTPError(503, "subscription_runtime_busy", "All subscription runtime slots are active; retry shortly")
			}
			oldest.process.cancel()
			delete(p.entries, oldestKey)
		}
		process, err := p.start(p.client, auth, save)
		if err != nil {
			return nil, nil, err
		}
		entry = &subscriptionWorkerLease{process: process, touched: time.Now()}
		p.entries[key] = entry
	}
	entry.references++
	var once sync.Once
	release := func() { once.Do(func() { p.mu.Lock(); entry.references--; entry.touched = time.Now(); p.mu.Unlock() }) }
	return entry.process, release, nil
}

func (p *subscriptionWorkerPool) Close() {
	p.mu.Lock()
	if p.closed {
		done := p.closeDone
		p.mu.Unlock()
		<-done
		return
	}
	p.closed = true
	p.closeDone = make(chan struct{})
	close(p.stop)
	processes := make([]*subscriptionWorkerProcess, 0, len(p.entries))
	for _, entry := range p.entries {
		processes = append(processes, entry.process)
		entry.process.cancel()
	}
	p.entries = map[string]*subscriptionWorkerLease{}
	p.mu.Unlock()
	defer close(p.closeDone)
	// Cancellation terminates each child, but its stdout reader can still be
	// persisting a received token under the separate ten-second save deadline.
	// Every caller must wait for that cleanup before retiring the database host.
	for _, process := range processes {
		<-process.done
	}
}

// Serialize operations sharing a rotating account token, including callers
// whose retained conversations live in separate processes.
func (p *subscriptionWorkerPool) enterAccount(ctx context.Context, key string) (func(), error) {
	p.mu.Lock()
	entry := p.accounts[key]
	if entry == nil {
		entry = &subscriptionAccountGate{token: make(chan struct{}, 1)}
		p.accounts[key] = entry
	}
	entry.references++
	p.mu.Unlock()
	cleanup := func() {
		p.mu.Lock()
		entry.references--
		if entry.references == 0 {
			delete(p.accounts, key)
		}
		p.mu.Unlock()
	}
	select {
	case entry.token <- struct{}{}:
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-p.stop:
		cleanup()
		return nil, NewHTTPError(503, "subscription_runtime_closed", "Subscription runtime is shutting down")
	}
	var once sync.Once
	return func() { once.Do(func() { <-entry.token; cleanup() }) }, nil
}
