package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClampRequestBodyBytes(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero falls back to default", 0, defaultMaxRequestBodyBytes},
		{"negative falls back to default", -1, defaultMaxRequestBodyBytes},
		{"below floor clamps up", 1, minMaxRequestBodyBytes},
		{"at floor passes", minMaxRequestBodyBytes, minMaxRequestBodyBytes},
		{"within range passes", 8 << 20, 8 << 20},
		{"at ceiling passes", maxMaxRequestBodyBytes, maxMaxRequestBodyBytes},
		{"above ceiling clamps down", 256 << 20, maxMaxRequestBodyBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRequestBodyBytes(tc.in); got != tc.want {
				t.Fatalf("clampRequestBodyBytes(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaxRequestBodyBytesFromContext(t *testing.T) {
	if got := maxRequestBodyBytesFromContext(context.Background()); got != defaultMaxRequestBodyBytes {
		t.Fatalf("empty context = %d, want default %d", got, defaultMaxRequestBodyBytes)
	}
	ctx := context.WithValue(context.Background(), requestBodyLimitKey{}, 8<<20)
	if got := maxRequestBodyBytesFromContext(ctx); got != 8<<20 {
		t.Fatalf("configured context = %d, want 8MiB", got)
	}
	// A non-positive value is ignored so a misconfigured context cannot disable the cap.
	ctx = context.WithValue(context.Background(), requestBodyLimitKey{}, 0)
	if got := maxRequestBodyBytesFromContext(ctx); got != defaultMaxRequestBodyBytes {
		t.Fatalf("zero context value = %d, want default %d", got, defaultMaxRequestBodyBytes)
	}
}

func TestServerReadsMaxRequestBodyBytesSetting(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"honors raised value", 8 << 20, 8 << 20},
		{"clamps over ceiling", 256 << 20, maxMaxRequestBodyBytes},
		{"clamps below floor", 100 << 10, minMaxRequestBodyBytes},
		{"zero falls back to default", 0, defaultMaxRequestBodyBytes},
		{"garbage falls back to default", "lots", defaultMaxRequestBodyBytes},
		{"empty falls back to default", "", defaultMaxRequestBodyBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			s := New(store)
			store.CreateResource("settings", AdminResource{
				ID:     "cfg_gateway",
				Status: StatusActive,
				Fields: map[string]any{"max_request_body_bytes": tc.value},
			})
			if got := s.readMaxRequestBodyBytesSetting(); got != tc.want {
				t.Fatalf("readMaxRequestBodyBytesSetting() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("missing setting falls back to default", func(t *testing.T) {
		store := NewMemoryStore()
		s := New(store)
		if got := s.readMaxRequestBodyBytesSetting(); got != defaultMaxRequestBodyBytes {
			t.Fatalf("expected default %d, got %d", defaultMaxRequestBodyBytes, got)
		}
	})

	t.Run("prefers cfg_gateway over other settings records", func(t *testing.T) {
		store := NewMemoryStore()
		s := New(store)
		store.CreateResource("settings", AdminResource{
			ID:     "cfg_legacy",
			Status: StatusActive,
			Fields: map[string]any{"max_request_body_bytes": 2 << 20},
		})
		store.CreateResource("settings", AdminResource{
			ID:     "cfg_gateway",
			Status: StatusActive,
			Fields: map[string]any{"max_request_body_bytes": 16 << 20},
		})
		if got := s.readMaxRequestBodyBytesSetting(); got != 16<<20 {
			t.Fatalf("expected cfg_gateway value 16MiB, got %d", got)
		}
	})
}

func TestWithRequestBodyLimitInjectsSettingIntoContext(t *testing.T) {
	store := NewMemoryStore()
	s := New(store)
	store.CreateResource("settings", AdminResource{
		ID:     "cfg_gateway",
		Status: StatusActive,
		Fields: map[string]any{"max_request_body_bytes": 8 << 20},
	})

	observed := 0
	h := s.withRequestBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = maxRequestBodyBytesFromContext(r.Context())
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if observed != 8<<20 {
		t.Fatalf("expected injected limit 8MiB, got %d", observed)
	}
}

func TestDecodeJSONHonorsContextRequestBodyLimit(t *testing.T) {
	// A JSON string just over the default 4 MiB limit.
	payload := `"` + strings.Repeat("a", defaultMaxRequestBodyBytes+1) + `"`

	var target any
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	if err := decodeJSON(req, &target); err == nil {
		t.Fatal("expected rejection at the default limit, got nil")
	}

	raised := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	ctx := context.WithValue(raised.Context(), requestBodyLimitKey{}, 8<<20)
	raised = raised.WithContext(ctx)
	if err := decodeJSON(raised, &target); err != nil {
		t.Fatalf("expected acceptance under a raised limit, got %v", err)
	}
}
