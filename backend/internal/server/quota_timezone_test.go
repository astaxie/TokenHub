package server

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func setQuotaTimezone(t *testing.T, store *GormStore, zone string) {
	t.Helper()
	_, err := store.CreateResourceChecked("settings", AdminResource{ID: gatewaySettingsID, Name: "Gateway", Status: StatusActive, Fields: map[string]any{quotaTimezoneField: zone}})
	if err != nil {
		t.Fatal(err)
	}
}

func quotaTestClock(t *testing.T, store *GormStore, initial string) *time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339, initial)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.Callback().Row().Before("gorm:row").Register("quota_test_clock", func(tx *gorm.DB) {
		if tx.Statement.SQL.String() == "SELECT (julianday('now') - 2440587.5) * 86400" {
			tx.Statement.SQL.Reset()
			tx.Statement.SQL.WriteString("SELECT ?")
			tx.Statement.Vars = []any{float64(now.UnixNano()) / float64(time.Second)}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return &now
}

func TestQuotaTimezoneBoundaries(t *testing.T) {
	cases := []struct{ name, zone, at, day, month string }{
		{"default UTC", "", "2026-09-30T16:00:00Z", "2026-09-30", "2026-09"},
		{"Shanghai before midnight", "Asia/Shanghai", "2026-09-30T15:59:59Z", "2026-09-30", "2026-09"},
		{"Shanghai month boundary", "Asia/Shanghai", "2026-09-30T16:00:00Z", "2026-10-01", "2026-10"},
		{"Shanghai year boundary", "Asia/Shanghai", "2026-12-31T16:00:00Z", "2027-01-01", "2027-01"},
		{"New York previous month", "America/New_York", "2026-10-01T03:59:59Z", "2026-09-30", "2026-09"},
		{"New York month boundary", "America/New_York", "2026-10-01T04:00:00Z", "2026-10-01", "2026-10"},
		{"spring DST midnight", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-08", "2026-03"},
		{"spring DST next midnight", "America/New_York", "2026-03-09T04:00:00Z", "2026-03-09", "2026-03"},
		{"fall DST first hour", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01", "2026-11"},
		{"fall DST repeated hour", "America/New_York", "2026-11-01T06:30:00Z", "2026-11-01", "2026-11"},
		{"fall DST next midnight", "America/New_York", "2026-11-02T05:00:00Z", "2026-11-02", "2026-11"},
	}
	store := NewMemoryStore()
	t.Setenv("TZ", "Pacific/Auckland")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setQuotaTimezone(t, store, tc.zone)
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			got, err := currentQuotaPeriods(store.db, at)
			want := quotaPeriods{Day: tc.day, Month: tc.month}
			if err != nil || got != want {
				t.Fatalf("periods = %+v, %v; want %+v", got, err, want)
			}
		})
	}
	if err := store.db.Delete(&AdminResource{}, "kind = ? AND id = ?", "settings", gatewaySettingsID).Error; err != nil {
		t.Fatal(err)
	}
	got, err := currentQuotaPeriods(store.db, time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC))
	if err != nil || got.Day != "2026-09-30" {
		t.Fatalf("missing setting must default to UTC: %+v, %v", got, err)
	}
}

func TestQuotaTimezoneAdmissionResetsAtLocalBoundary(t *testing.T) {
	for _, scope := range []string{"api_key", "user"} {
		for _, period := range []string{"daily", "monthly"} {
			t.Run(scope+"/"+period, func(t *testing.T) {
				limits := map[string]any{}
				if scope == "user" {
					limits[period+"_requests"] = 1
				}
				store, project, key, _ := setupUserQuotaTest(t, limits)
				if scope == "api_key" {
					if period == "daily" {
						key.Limits.DailyRequests = 1
					} else {
						key.Limits.MonthlyRequests = 1
					}
					var err error
					key, err = store.UpdateAPIKey(key.ID, key)
					if err != nil {
						t.Fatal(err)
					}
				}
				setQuotaTimezone(t, store, "Asia/Shanghai")
				clock := quotaTestClock(t, store, "2026-09-30T15:59:59Z")
				first, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
				if err != nil {
					t.Fatal(err)
				}
				if !first.StartedAt.Equal(*clock) {
					t.Fatalf("admission clock = %s, want %s", first.StartedAt, clock)
				}
				if _, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
					t.Fatalf("expected quota rejection, got %v", err)
				}
				*clock = clock.Add(time.Second)
				second, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
				if err != nil {
					t.Fatalf("quota did not reset at local midnight: %v", err)
				}
				store.FinishCall(first, RouteSelection{}, Usage{TotalTokens: 7}, 200, "", "", "quota-test")
				store.FinishCall(second, RouteSelection{}, Usage{TotalTokens: 11}, 200, "", "", "quota-test")
				snapshot, err := store.apiKeyQuotaSnapshot(store.db, key, project)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Day.Bucket != "2026-10-01" || snapshot.Month.Bucket != "2026-10" || snapshot.Day.Usage.Requests != 1 || snapshot.Day.Usage.TotalTokens != 11 || snapshot.Month.Usage.TotalTokens != 11 {
					t.Fatalf("wrong current quota snapshot: %+v", snapshot)
				}
				id := key.ID
				if scope == "user" {
					id = "usr_user_quota"
				}
				usage, ok, err := store.GetQuotaPolicyUsage(scope, id)
				if err != nil || !ok || usage.Daily.TotalTokens != 11 || usage.Monthly.TotalTokens != 11 {
					t.Fatalf("wrong policy usage: %+v, %v, %v", usage, ok, err)
				}
				old, err := readQuotaCounter(store.db, key.ID, "day", "2026-09-30")
				if err != nil || old.TotalTokens != 7 {
					t.Fatalf("cross-midnight settlement changed admission bucket: %+v, %v", old, err)
				}
			})
		}
	}
}

func TestQuotaTimezoneSettlementSurvivesSettingChange(t *testing.T) {
	for _, action := range []string{"finish", "image rollback", "response rollback", "response refund"} {
		t.Run(action, func(t *testing.T) {
			store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_tokens": 100, "monthly_tokens": 100})
			setQuotaTimezone(t, store, "Asia/Shanghai")
			quotaTestClock(t, store, "2026-09-30T16:00:00Z")
			call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 10)
			if err != nil {
				t.Fatal(err)
			}
			// Recovery loads durable evidence again, without an in-memory timezone cache.
			setQuotaTimezone(t, store, "UTC")
			switch action {
			case "finish":
				store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 4}, 200, "", "", "quota-test")
			case "image rollback":
				job := ImageJob{RequestID: call.RequestID, APIKeyID: key.ID, AttributedUserID: call.AttributedUserID, UserQuotaEnabled: true, ReservedTokens: 10, AdmittedAt: &call.StartedAt}
				err = store.db.Transaction(func(tx *gorm.DB) error { return store.rollbackImageJobAdmission(tx, job) })
			default:
				job := ResponseJob{RequestID: call.RequestID, APIKeyID: key.ID, AttributedUserID: call.AttributedUserID, UserQuotaEnabled: true, ReservedTokens: 10, AdmittedAt: &call.StartedAt, Phase: responseJobPhaseAdmitted}
				err = store.db.Transaction(func(tx *gorm.DB) error {
					if action == "response refund" {
						return store.refundUndispatchedResponseJobReservation(tx, job)
					}
					return store.rollbackResponseJobAdmission(tx, job)
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			wantRequests, wantTokens := int64(0), int64(0)
			if action == "finish" {
				wantRequests, wantTokens = 1, 4
			}
			if action == "response refund" {
				wantRequests = 1
			}
			for _, period := range []struct{ scope, bucket string }{{"day", "2026-10-01"}, {"month", "2026-10"}} {
				for _, id := range []string{key.ID, call.UserQuotaID} {
					counter, err := readQuotaCounter(store.db, id, period.scope, period.bucket)
					if err != nil || counter.Requests != wantRequests || counter.TotalTokens != wantTokens {
						t.Fatalf("%s %s counter = %+v, %v; want requests=%d tokens=%d", id, period.scope, counter, err, wantRequests, wantTokens)
					}
				}
			}
			var wrong int64
			if err := store.db.Model(&QuotaBucket{}).Where("scope = ? AND bucket = ? OR scope = ? AND bucket = ?", "day", "2026-09-30", "month", "2026-09").Count(&wrong).Error; err != nil {
				t.Fatal(err)
			}
			if wrong != 0 {
				t.Fatalf("settlement wrote %d UTC buckets after timezone change", wrong)
			}
		})
	}
}

func TestQuotaTimezoneLegacyAdmissionAndValidation(t *testing.T) {
	store := NewMemoryStore()
	setQuotaTimezone(t, store, "Asia/Shanghai")
	at := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(meteringRequestSnapshot{RequestID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&meteringEntry{ID: "legacy:admission", Kind: "admission", Payload: string(payload)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "missing", ""} {
		periods, err := admittedQuotaPeriods(store.db, id, at)
		if err != nil || periods.Day != "2026-09-30" || periods.Month != "2026-09" {
			t.Fatalf("legacy admission must retain UTC: %+v, %v", periods, err)
		}
	}
	for _, zone := range []string{"Nope/Nowhere", "Local", "../UTC"} {
		req := AdminResource{ID: gatewaySettingsID, Name: "Gateway", Fields: map[string]any{quotaTimezoneField: zone}}
		response := doJSON(t, New(store).Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, req, "")
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_quota_timezone") {
			t.Fatalf("invalid zone %s response = %d %s", zone, response.Code, response.Body)
		}
	}
	setQuotaTimezone(t, store, "Nope/Nowhere")
	if _, err := currentQuotaPeriods(store.db, at); err == nil {
		t.Fatal("invalid persisted timezone must fail closed")
	}
}
