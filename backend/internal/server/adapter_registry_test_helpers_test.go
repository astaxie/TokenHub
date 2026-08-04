package server

// registerTestAdapter installs an adapter for adapterType without declaring any
// capabilities. It replaces the older habit of writing straight into the
// server's adapter map, and deliberately leaves the capability descriptor
// untouched so that capability-gated routing keeps treating an ad hoc test type
// as undeclared. Tests that need the type to advertise capabilities call
// server.adapterRegistry.Register with them instead.
func registerTestAdapter(s *Server, adapterType string, adapter any) {
	s.adapterRegistry.adapters[adapterType] = adapter
}
