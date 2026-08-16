package server

import (
	"net/http"
	"strings"
)

type methodRoute struct {
	Method  string
	Handler http.HandlerFunc
}

type methodNotAllowedFactory func(allowedMethods string) http.HandlerFunc

// registerSingleMethodRoute must only be called once for a path. Multi-method
// resources need one shared fallback with the complete Allow value.
func (s *Server) registerSingleMethodRoute(method string, pattern string, handler http.HandlerFunc, methodNotAllowed http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+pattern, handler)
	if method == http.MethodGet {
		s.mux.HandleFunc(http.MethodHead+" "+pattern, methodNotAllowed)
	}
	s.mux.HandleFunc(pattern, methodNotAllowed)
}

// registerMethodRoutes registers every allowed method for one path and one
// shared fallback. The Allow value comes from the same route list so the two
// cannot drift apart.
func (s *Server) registerMethodRoutes(pattern string, methodNotAllowed methodNotAllowedFactory, routes ...methodRoute) {
	hasGET := false
	for _, route := range routes {
		hasGET = hasGET || route.Method == http.MethodGet
		s.mux.HandleFunc(route.Method+" "+pattern, route.Handler)
	}
	fallback := methodNotAllowed(methodRoutesAllow(routes))
	if hasGET {
		s.mux.HandleFunc(http.MethodHead+" "+pattern, fallback)
	}
	s.mux.HandleFunc(pattern, fallback)
}

func methodRoutesAllow(routes []methodRoute) string {
	allowedMethods := make([]string, 0, len(routes))
	for _, route := range routes {
		allowedMethods = append(allowedMethods, route.Method)
	}
	return strings.Join(allowedMethods, ", ")
}

// registerDynamicGETRoute rejects the HEAD requests that a GET pattern matches
// automatically. The route family must also register a path-only subtree
// handler for other wrong methods and malformed paths.
func (s *Server) registerDynamicGETRoute(pattern string, handler http.HandlerFunc, headFallback http.HandlerFunc) {
	s.mux.HandleFunc(http.MethodGet+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headFallback(w, r)
			return
		}
		handler(w, r)
	})
}

// registerDynamicMethodRoute registers one non-GET method-specific part of a
// dynamic route. The route family handles other methods through its subtree.
func (s *Server) registerDynamicMethodRoute(method string, pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+pattern, handler)
}

func jsonMethodNotAllowed(allowedMethod string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowedMethod)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func anthropicMethodNotAllowed(allowedMethod string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowedMethod)
		writeAnthropicError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func plainTextMethodNotAllowed(allowedMethod string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allowedMethod)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) adminAuthenticationMethodNotAllowed(allowedMethod string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethod)
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authorizeAdminUser(w, r); !ok {
			return
		}
		reject(w, r)
	}
}

func (s *Server) adminMethodNotAllowed(resource string, allowedMethod string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethod)
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireAdmin(w, r, resource, r.Method); !ok {
			return
		}
		reject(w, r)
	}
}
