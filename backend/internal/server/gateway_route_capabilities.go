package server

func (s *Server) firstRouteAdapterTypeWithCapability(routes []RouteSelection, capability AdapterCapability) string {
	if s == nil || s.adapterRegistry == nil {
		return ""
	}
	for _, route := range routes {
		descriptor, ok := s.adapterRegistry.Describe(route.Provider.Type)
		if ok && adapterSupports(descriptor, capability) {
			return route.Provider.Type
		}
	}
	return ""
}
