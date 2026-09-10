package server

import (
	"net/http"
	"net/url"
	"testing"
)

func TestPluginRuntimeMutationRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "install", method: http.MethodPost, path: "/api/admin/plugins/install", want: true},
		{name: "update", method: http.MethodPost, path: "/api/admin/plugins/example/update", want: true},
		{name: "rollback", method: http.MethodPost, path: "/api/admin/plugins/example/rollback", want: true},
		{name: "state", method: http.MethodPatch, path: "/api/admin/plugins/example/state", want: true},
		{name: "delete package", method: http.MethodDelete, path: "/api/admin/plugin-packages/example", want: true},
		{name: "action", method: http.MethodPost, path: "/api/admin/plugins/example/actions/run", want: false},
		{name: "permission diff", method: http.MethodPost, path: "/api/admin/plugins/example/permission-diff", want: false},
		{name: "list", method: http.MethodGet, path: "/api/admin/plugins", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Method: tt.method, URL: &url.URL{Path: tt.path}}
			if got := pluginRuntimeMutationRequest(r); got != tt.want {
				t.Fatalf("pluginRuntimeMutationRequest(%s %s) = %t, want %t", tt.method, tt.path, got, tt.want)
			}
		})
	}
}
