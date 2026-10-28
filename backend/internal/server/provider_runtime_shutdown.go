package server

import "context"

// Built-in adapters may retain account workers across HTTP turns. Their close
// methods are idempotent because several provider types can share one pool.
func (s *Server) shutdownProviderRuntimes() {
	if s == nil {
		return
	}
	// Earlier shutdown stages can exhaust the deadline before reaching the
	// scheduler. Stop it on every exit path, then drain both background and
	// request-triggered native rotations before the heartbeat or store closes.
	if s.credentialRefresh != nil {
		s.credentialRefresh.stopScheduler()
		defer func() { _ = s.credentialRefresh.Shutdown(context.Background()) }()
	}
	if lifecycle, ok := s.store.(interface{ drainNativeCredentialRefreshes() }); ok {
		lifecycle.drainNativeCredentialRefreshes()
	}
	if s.adapterRegistry == nil {
		return
	}
	for _, adapter := range s.adapterRegistry.adapters {
		if runtime, ok := adapter.(interface{ Close() }); ok {
			runtime.Close()
		}
	}
}
