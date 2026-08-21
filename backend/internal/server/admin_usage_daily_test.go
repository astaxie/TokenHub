package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUsageDailyUsesDashboardTimezoneWindow(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusActive,
		Fields: map[string]any{dashboardTimezoneField: "Asia/Shanghai"},
	})
	project := store.CreateProject(Project{ID: "prj_daily", Name: "Daily project", CostCenter: "CC-DAILY", Status: StatusActive})
	records := []UsageRecord{
		{
			ID:          "use_daily_previous",
			RequestID:   "req_daily_previous",
			ProjectID:   project.ID,
			APIKeyID:    "key_daily",
			ModelName:   "gpt-before",
			InputTokens: 10,
			TotalTokens: 10,
			CostUSD:     1,
			CreatedAt:   time.Date(2026, 3, 5, 15, 59, 59, 0, time.UTC),
		},
		{
			ID:           "use_daily_inside",
			RequestID:    "req_daily_inside",
			ProjectID:    project.ID,
			APIKeyID:     "key_daily",
			ModelName:    "gpt-today",
			InputTokens:  3,
			OutputTokens: 4,
			TotalTokens:  7,
			CostUSD:      0.25,
			CreatedAt:    time.Date(2026, 3, 5, 16, 0, 0, 0, time.UTC),
		},
		{
			ID:          "use_daily_next",
			RequestID:   "req_daily_next",
			ProjectID:   project.ID,
			APIKeyID:    "key_next",
			ModelName:   "gpt-next",
			TotalTokens: 20,
			CostUSD:     2,
			CreatedAt:   time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC),
		},
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	requestLogs := []RequestLog{
		{
			ID:         "log_daily_previous",
			RequestID:  "req_daily_previous",
			ProjectID:  project.ID,
			APIKeyID:   "key_daily",
			StatusCode: http.StatusOK,
			CreatedAt:  time.Date(2026, 3, 5, 15, 59, 59, 0, time.UTC),
		},
		{
			ID:         "log_daily_inside",
			RequestID:  "req_daily_inside",
			ProjectID:  project.ID,
			APIKeyID:   "key_daily",
			StatusCode: http.StatusOK,
			CreatedAt:  time.Date(2026, 3, 5, 16, 0, 0, 0, time.UTC),
		},
		{
			ID:         "log_daily_error",
			RequestID:  "req_daily_error",
			ProjectID:  project.ID,
			APIKeyID:   "key_daily",
			StatusCode: http.StatusBadGateway,
			CreatedAt:  time.Date(2026, 3, 5, 17, 0, 0, 0, time.UTC),
		},
		{
			ID:         "log_daily_next",
			RequestID:  "req_daily_next",
			ProjectID:  project.ID,
			APIKeyID:   "key_next",
			StatusCode: http.StatusOK,
			CreatedAt:  time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC),
		},
	}
	if err := store.db.Create(&requestLogs).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 3, 6, 2, 30, 0, 0, time.UTC)
	daily, err := server.usageDailyForUser(t.Context(), AdminUser{ID: "usr_daily_admin", Role: "admin", Status: StatusActive}, now)
	if err != nil {
		t.Fatal(err)
	}

	if got := daily["date"]; got != "2026-03-06" {
		t.Fatalf("date = %#v, want 2026-03-06", got)
	}
	if got := daily["window_start"]; got != "2026-03-05T16:00:00Z" {
		t.Fatalf("window_start = %#v, want Asia/Shanghai day start in UTC", got)
	}
	summary := daily["summary"].(map[string]any)
	if got := summary["request_count"]; got != int64(2) {
		t.Fatalf("request_count = %#v, want 2", got)
	}
	if got := summary["usage_record_count"]; got != int64(1) {
		t.Fatalf("usage_record_count = %#v, want 1", got)
	}
	if got := summary["errors"]; got != int64(1) {
		t.Fatalf("errors = %#v, want 1", got)
	}
	if got := summary["total_tokens"]; got != int64(7) {
		t.Fatalf("total_tokens = %#v, want 7", got)
	}
	breakdown := daily["breakdown"].(map[string]any)
	models := breakdown["models"].([]map[string]any)
	if len(models) != 1 || models[0]["id"] != "gpt-today" {
		t.Fatalf("model breakdown = %#v, want only today's record", models)
	}
	apiKeys := breakdown["api_keys"].([]map[string]any)
	if len(apiKeys) != 1 || apiKeys[0]["id"] != "key_daily" {
		t.Fatalf("api key breakdown = %#v, want key_daily", apiKeys)
	}
}

func TestUsageDailyUsesConfiguredDashboardTimezone(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusActive,
		Fields: map[string]any{dashboardTimezoneField: "Asia/Shanghai"},
	})

	daily, err := server.usageDailyForUser(t.Context(), AdminUser{ID: "usr_daily_admin", Role: "admin", Status: StatusActive}, time.Date(2026, 3, 6, 2, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if got := daily["timezone"]; got != "Asia/Shanghai" {
		t.Fatalf("timezone = %#v, want Asia/Shanghai", got)
	}
	if got := daily["window_start"]; got != "2026-03-05T16:00:00Z" {
		t.Fatalf("window_start = %#v, want dashboard timezone start", got)
	}
}

func TestUsageDailyEndpointIgnoresTimezoneQuery(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusActive,
		Fields: map[string]any{dashboardTimezoneField: "Asia/Shanghai"},
	})
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodGet, "/api/admin/usage/daily?timezone=UTC", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("usage daily response = %d %s, want 200", response.Code, response.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["timezone"]; got != "Asia/Shanghai" {
		t.Fatalf("timezone = %#v, want configured dashboard timezone", got)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	localNow := time.Now().In(location)
	expectedStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).UTC().Format(time.RFC3339)
	if got := payload["window_start"]; got != expectedStart {
		t.Fatalf("window_start = %#v, want dashboard timezone day start", got)
	}
}

func TestUsageDailyRedactsProviderBreakdownsForNonPlatformRoles(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	store.CreateResource("teams", AdminResource{ID: "team_daily", Status: StatusActive})
	teamProject, err := store.CreateProjectChecked(Project{
		ID:          "prj_daily_team",
		Name:        "Daily team project",
		TeamID:      "team_daily",
		OwnerUserID: "usr_daily_user",
		Status:      StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	record := UsageRecord{
		ID:                 "use_daily_provider",
		RequestID:          "req_daily_provider",
		ProjectID:          teamProject.ID,
		APIKeyID:           "key_daily_provider",
		ModelName:          "gpt-daily-provider",
		ProviderID:         "provider_sensitive_daily",
		ProviderResourceID: "resource_sensitive_daily",
		InputTokens:        10,
		OutputTokens:       5,
		TotalTokens:        15,
		CostUSD:            12.34,
		CreatedAt:          time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC),
	}
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		user AdminUser
	}{
		{name: "team leader", user: AdminUser{ID: "usr_daily_leader", Role: "team_leader", TeamID: "team_daily", Status: StatusActive}},
		{name: "user", user: AdminUser{ID: "usr_daily_user", Role: "user", TeamID: "team_daily", Status: StatusActive}},
		{name: "security admin", user: AdminUser{ID: "usr_daily_security", Role: "security_admin", Status: StatusActive}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daily, err := server.usageDailyForUser(t.Context(), test.user, time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			breakdown := daily["breakdown"].(map[string]any)
			if _, ok := breakdown["providers"]; ok {
				t.Fatalf("providers breakdown = %#v, want redacted", breakdown["providers"])
			}
			if _, ok := breakdown["provider_resources"]; ok {
				t.Fatalf("provider_resources breakdown = %#v, want redacted", breakdown["provider_resources"])
			}
			body, err := json.Marshal(daily)
			if err != nil {
				t.Fatal(err)
			}
			bodyText := string(body)
			if strings.Contains(bodyText, "provider_sensitive_daily") || strings.Contains(bodyText, "resource_sensitive_daily") {
				t.Fatalf("daily payload leaked provider identifiers: %s", bodyText)
			}
		})
	}
}

func TestUsageDailyIncludesProviderBreakdownsForPlatformAdmin(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	project := store.CreateProject(Project{ID: "prj_daily_platform", Name: "Daily platform project", Status: StatusActive})
	record := UsageRecord{
		ID:                 "use_daily_platform_provider",
		RequestID:          "req_daily_platform_provider",
		ProjectID:          project.ID,
		ModelName:          "gpt-daily-platform-provider",
		ProviderID:         "provider_platform_daily",
		ProviderResourceID: "resource_platform_daily",
		TotalTokens:        15,
		CostUSD:            12.34,
		CreatedAt:          time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC),
	}
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	daily, err := server.usageDailyForUser(t.Context(), AdminUser{ID: "usr_daily_admin", Role: "admin", Status: StatusActive}, time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	breakdown := daily["breakdown"].(map[string]any)
	providers := breakdown["providers"].([]map[string]any)
	if len(providers) != 1 || providers[0]["id"] != "provider_platform_daily" || providers[0]["estimated_cost_usd"] != 12.34 {
		t.Fatalf("providers breakdown = %#v, want platform provider row", providers)
	}
	resources := breakdown["provider_resources"].([]map[string]any)
	if len(resources) != 1 || resources[0]["id"] != "resource_platform_daily" || resources[0]["estimated_cost_usd"] != 12.34 {
		t.Fatalf("provider_resources breakdown = %#v, want platform resource row", resources)
	}
}

func TestGatewaySettingsRejectInvalidDashboardTimezone(t *testing.T) {
	store := NewMemoryStore()
	setting := AdminResource{
		ID:     gatewaySettingsID,
		Name:   "Gateway settings",
		Status: StatusActive,
		Fields: map[string]any{
			dashboardTimezoneField:        "Nope/Nowhere",
			syntheticDNSEnabledField:      false,
			syntheticDNSCIDRsField:        defaultSyntheticDNSCIDRs,
			syntheticDNSAllowPrivateField: false,
		},
	}
	response := doJSON(t, New(store).Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, setting, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_dashboard_timezone") {
		t.Fatalf("invalid dashboard timezone response = %d %s, want invalid_dashboard_timezone", response.Code, response.Body)
	}
}
