package server

import (
	"context"
	"sync"
)

// Shared by every value copy of a store so shutdown sees request-triggered and
// scheduled refreshes, including ones waiting to acquire their account lease.
type nativeCredentialRefreshLifecycle struct {
	mu        sync.Mutex
	active    map[*nativeCredentialRefreshOperation]struct{}
	stopped   bool
	permanent bool
	drained   chan struct{}
}

type nativeCredentialRefreshOperation struct{ cancel context.CancelFunc }

func (l *nativeCredentialRefreshLifecycle) begin(ctx context.Context) (context.Context, func(), error) {
	if l == nil {
		return ctx, func() {}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return nil, nil, NewHTTPError(503, "provider_credential_refresh_stopped", "Provider credential refresh is shutting down")
	}
	ctx, cancel := context.WithCancel(ctx)
	operation := &nativeCredentialRefreshOperation{cancel: cancel}
	if l.active == nil {
		l.active = make(map[*nativeCredentialRefreshOperation]struct{})
	}
	l.active[operation] = struct{}{}
	return ctx, func() {
		cancel()
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.active, operation)
		if l.stopped && len(l.active) == 0 {
			close(l.drained)
		}
	}, nil
}

func (l *nativeCredentialRefreshLifecycle) stop(permanent bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.permanent = l.permanent || permanent
	if !l.stopped {
		l.stopped = true
		l.drained = make(chan struct{})
		if len(l.active) == 0 {
			close(l.drained)
		}
	}
	drained := l.drained
	for operation := range l.active {
		// Cancel acquisition and upstream work. An acquired refresh lease has
		// its own lifetime and still renews through the existing ten-second save.
		operation.cancel()
	}
	l.mu.Unlock()
	<-drained
}

func (l *nativeCredentialRefreshLifecycle) resume() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// A new application can reuse an open, fully drained store. Closing the
	// store itself is permanent and never permits later credential operations.
	if !l.permanent && len(l.active) == 0 {
		l.stopped = false
		l.drained = nil
	}
}

func (s *GormStore) drainNativeCredentialRefreshes() { s.nativeRefreshes.stop(false) }
