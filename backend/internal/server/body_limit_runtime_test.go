package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadEffectiveBodyLimits(t *testing.T) {
	cases := []struct {
		name           string
		envJSON        int64
		envMultimodal  int64
		dbJSON         any
		dbMultimodal   any
		wantJSON       int64
		wantMultimodal int64
	}{
		{
			name:    "no override falls back to env defaults",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			wantJSON: 1 << 10, wantMultimodal: 4 << 10,
		},
		{
			name:    "override honored",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			dbJSON: int64(2 << 10), dbMultimodal: int64(8 << 10),
			wantJSON: 2 << 10, wantMultimodal: 8 << 10,
		},
		{
			name:    "override over ceiling clamps",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			dbJSON: int64(1024 << 30), dbMultimodal: int64(1024 << 30),
			wantJSON: maxConfigurableRequestBytes, wantMultimodal: maxConfigurableRequestBytes,
		},
		{
			name:    "zero override falls back to env",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			dbJSON: int64(0), dbMultimodal: int64(0),
			wantJSON: 1 << 10, wantMultimodal: 4 << 10,
		},
		{
			name:    "garbage override falls back to env",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			dbJSON: "lots", dbMultimodal: "nope",
			wantJSON: 1 << 10, wantMultimodal: 4 << 10,
		},
		{
			name:    "partial override only changes one tier",
			envJSON: 1 << 10, envMultimodal: 4 << 10,
			dbJSON: int64(2 << 10), dbMultimodal: nil,
			wantJSON: 2 << 10, wantMultimodal: 4 << 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			s := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: tc.envJSON, MaxMultimodalRequestBytes: tc.envMultimodal})
			fields := map[string]any{}
			if tc.dbJSON != nil {
				fields["max_json_request_bytes"] = tc.dbJSON
			}
			if tc.dbMultimodal != nil {
				fields["max_multimodal_request_bytes"] = tc.dbMultimodal
			}
			store.CreateResource("settings", AdminResource{ID: "cfg_gateway", Status: StatusActive, Fields: fields})
			got := s.readEffectiveBodyLimits()
			if got.jsonLimit != tc.wantJSON || got.multimodalLimit != tc.wantMultimodal {
				t.Fatalf("readEffectiveBodyLimits() = (json=%d, multimodal=%d), want (json=%d, multimodal=%d)",
					got.jsonLimit, got.multimodalLimit, tc.wantJSON, tc.wantMultimodal)
			}
		})
	}
}

func TestReadEffectiveBodyLimitsPrefersCfgGateway(t *testing.T) {
	store := NewMemoryStore()
	s := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: 1 << 10, MaxMultimodalRequestBytes: 4 << 10})
	store.CreateResource("settings", AdminResource{ID: "cfg_legacy", Status: StatusActive, Fields: map[string]any{"max_json_request_bytes": int64(1 << 10)}})
	store.CreateResource("settings", AdminResource{ID: "cfg_gateway", Status: StatusActive, Fields: map[string]any{"max_json_request_bytes": int64(8 << 10)}})
	got := s.readEffectiveBodyLimits()
	if got.jsonLimit != 8<<10 {
		t.Fatalf("expected cfg_gateway value 8KiB, got %d", got.jsonLimit)
	}
}

func decodeJSONAt(t *testing.T, s *Server, limit int64, n int) error {
	t.Helper()
	body := `"` + strings.Repeat("a", n-2) + `"`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	var target any
	return s.decodeJSONLimit(rec, req, &target, limit)
}

func TestDecodeJSONHonorsRuntimeJSONOverride(t *testing.T) {
	// Env default 1KiB: a 2KiB body is over the limit and is rejected with 413.
	store := NewMemoryStore()
	s := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: 1 << 10, MaxMultimodalRequestBytes: 4 << 10})
	if err := decodeJSONAt(t, s, s.effectiveJSONRequestLimit(), 2<<10); !isPayloadTooLarge(err) {
		t.Fatalf("expected 413 at env default, got %v", err)
	}

	// Raise the limit at runtime via cfg_gateway; the same 2KiB body is now accepted,
	// and a body over the raised limit is still rejected.
	store2 := NewMemoryStore()
	s2 := NewWithConfig(store2, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: 1 << 10, MaxMultimodalRequestBytes: 4 << 10})
	store2.CreateResource("settings", AdminResource{ID: "cfg_gateway", Status: StatusActive, Fields: map[string]any{"max_json_request_bytes": int64(4 << 10)}})
	if err := decodeJSONAt(t, s2, s2.effectiveJSONRequestLimit(), 2<<10); err != nil {
		t.Fatalf("expected acceptance under raised runtime limit, got %v", err)
	}
	if err := decodeJSONAt(t, s2, s2.effectiveJSONRequestLimit(), 5<<10); !isPayloadTooLarge(err) {
		t.Fatalf("expected 413 over raised runtime limit, got %v", err)
	}
}

func TestDecodeJSONMultimodalOverrideSeparateFromJSON(t *testing.T) {
	store := NewMemoryStore()
	s := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: 1 << 10, MaxMultimodalRequestBytes: 2 << 10})
	// Raise only the multimodal tier; the JSON tier stays at the 1KiB env default.
	store.CreateResource("settings", AdminResource{ID: "cfg_gateway", Status: StatusActive, Fields: map[string]any{"max_multimodal_request_bytes": int64(8 << 10)}})
	// 4KiB body: over the 1KiB JSON limit but under the 8KiB multimodal override.
	if err := decodeJSONAt(t, s, s.effectiveMultimodalRequestLimit(), 4<<10); err != nil {
		t.Fatalf("expected acceptance under raised multimodal limit, got %v", err)
	}
	if err := decodeJSONAt(t, s, s.effectiveJSONRequestLimit(), 4<<10); !isPayloadTooLarge(err) {
		t.Fatalf("expected 413 at unchanged 1KiB JSON limit, got %v", err)
	}
}

// TestBodyLimitCacheReadsSettingsOnceUntilTTLExpiry verifies the 10-second
// cache: within the TTL, repeated effective-limit calls serve the cached
// snapshot (a settings change is NOT re-read), and after the TTL expires the
// next call re-reads settings and picks up the change.
func TestBodyLimitCacheReadsSettingsOnceUntilTTLExpiry(t *testing.T) {
	store := NewMemoryStore()
	s := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MaxJSONRequestBytes: 1 << 20, MaxMultimodalRequestBytes: 4 << 20})
	store.CreateResource("settings", AdminResource{ID: "cfg_gateway", Status: StatusActive, Fields: map[string]any{"max_json_request_bytes": int64(8 << 20)}})

	// First call reads settings and caches the snapshot with refreshedAt set.
	if got := s.currentBodyLimits().jsonLimit; got != 8<<20 {
		t.Fatalf("first read: jsonLimit=%d, want 8MiB", got)
	}

	// Change the override; within TTL the cache must serve the stale value
	// (no re-read), proving the snapshot is fresh and not re-queried per call.
	store.UpdateResource("settings", "cfg_gateway", AdminResource{Fields: map[string]any{"max_json_request_bytes": int64(16 << 20)}})
	if got := s.currentBodyLimits().jsonLimit; got != 8<<20 {
		t.Fatalf("within TTL: jsonLimit=%d, want stale 8MiB (no re-read)", got)
	}

	// Back-date the snapshot's refreshedAt to simulate TTL expiry.
	s.bodyLimits.snapshot.Store(&bodyLimitSnapshot{jsonLimit: 8 << 20, multimodalLimit: 4 << 20, refreshedAt: time.Now().Add(-bodyLimitTTL - time.Second)})

	// After expiry, the next call re-reads settings and picks up the new override.
	if got := s.currentBodyLimits().jsonLimit; got != 16<<20 {
		t.Fatalf("after TTL expiry: jsonLimit=%d, want 16MiB (re-read)", got)
	}
}
