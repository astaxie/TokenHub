package server

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestAdminProviderResourceActionSuffixRouting guards the nested
// provider-resource action parsing: /quota and /refresh-token must keep
// dispatching to their handlers alongside the /health and /test suffixes.
func TestAdminProviderResourceActionSuffixRouting(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	quota := doJSON(t, app, http.MethodGet, "/api/admin/provider-resources/res-missing/quota", nil, "")
	if !strings.Contains(quota.Body, "provider_resource_not_found") {
		t.Fatalf("expected quota handler to be reached, got %d: %s", quota.Code, quota.Body)
	}

	refresh := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/res-missing/refresh-token", nil, "")
	if strings.Contains(refresh.Body, `"type":"not_found"`) {
		t.Fatalf("refresh-token route fell through suffix parsing, got %d: %s", refresh.Code, refresh.Body)
	}
}

// TestSplitNestedAdminPath covers the table-driven suffix parsing shared by
// the nested admin routes: IDs containing slashes must survive intact and
// only known action suffixes may be split off.
func TestSplitNestedAdminPath(t *testing.T) {
	actions := []string{"health", "test", "refresh-token", "quota"}
	cases := []struct {
		name      string
		remainder string
		want      []string
	}{
		{"empty", "", nil},
		{"plain id", "res-1", []string{"res-1"}},
		{"id with action", "res-1/health", []string{"res-1", "health"}},
		{"slashed id with action", "litellm/key/abc/quota", []string{"litellm/key/abc", "quota"}},
		{"slashed id without action", "litellm/key/abc", []string{"litellm/key/abc"}},
		{"segment ending in action name", "res-refresh-token", []string{"res-refresh-token"}},
		{"unknown action stays in id", "res-1/unknown", []string{"res-1/unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitNestedAdminPath(tc.remainder, actions)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("splitNestedAdminPath(%q) = %v, want %v", tc.remainder, got, tc.want)
			}
		})
	}
}

// TestSplitEscapedAdminPath covers the generic admin resource path parsing:
// an ID that encodes "/" as %2F must survive as a single segment, which is
// what lets migration rollback delete resources whose IDs came from an
// external system.
func TestSplitEscapedAdminPath(t *testing.T) {
	const prefix = "/api/admin/resources/"
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"empty", prefix, nil},
		{"kind only", prefix + "teams", []string{"teams"}},
		{"kind and id", prefix + "teams/team-1", []string{"teams", "team-1"}},
		{"escaped slash stays one segment", prefix + "teams/litellm%2Fteam%2Fcore", []string{"teams", "litellm/team/core"}},
		{"nested action", prefix + "monitors/mon-1/run", []string{"monitors", "mon-1", "run"}},
		{"invalid escape", prefix + "teams/bad%zz", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitEscapedAdminPath(tc.path, prefix)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("splitEscapedAdminPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestAdminResourceDeleteWithSlashedID guards the end-to-end path: a team
// whose ID contains "/" must be reachable through the generic resource route.
func TestAdminResourceDeleteWithSlashedID(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	store.CreateResource("teams", AdminResource{ID: "litellm/team/core", Name: "Core", Status: StatusActive})

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/resources/teams/litellm%2Fteam%2Fcore", nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected the slashed team ID to be routed, got %d: %s", deleted.Code, deleted.Body)
	}
	for _, team := range store.ListResources("teams") {
		if team.ID == "litellm/team/core" {
			t.Fatal("expected the team to be deleted")
		}
	}
}

// TestSplitNestedEscapedAdminPath covers the nested action parsing over the
// escaped path: an ID whose decoded form ends in an action name must not be
// mistaken for that action on a shorter ID.
func TestSplitNestedEscapedAdminPath(t *testing.T) {
	const prefix = "/api/admin/provider-resources/"
	actions := []string{"health", "test", "refresh-token", "quota"}
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"empty", prefix, nil},
		{"plain id", prefix + "res-1", []string{"res-1"}},
		{"real action", prefix + "res-1/health", []string{"res-1", "health"}},
		{"escaped id keeps action", prefix + "litellm%2Fkey%2Fabc/quota", []string{"litellm/key/abc", "quota"}},
		{"escaped id ending in action name", prefix + "tenant%2Fhealth", []string{"tenant/health"}},
		{"escaped id without action", prefix + "litellm%2Fkey%2Fabc", []string{"litellm/key/abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitNestedEscapedAdminPath(tc.path, prefix, actions)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("splitNestedEscapedAdminPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
