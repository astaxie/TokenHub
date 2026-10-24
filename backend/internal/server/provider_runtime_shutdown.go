package server

// Built-in adapters may retain account workers across HTTP turns. Their close
// methods are idempotent because several provider types can share one pool.
func (s *Server) shutdownProviderRuntimes() {
	if s == nil || s.adapterRegistry == nil {
		return
	}
	for _, adapter := range s.adapterRegistry.adapters {
		if runtime, ok := adapter.(interface{ Close() }); ok {
			runtime.Close()
		}
	}
}
