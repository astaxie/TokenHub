package quotasource

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCloudSignaturesBindKnownHostAndRegion(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, auth := range []string{"volcengine", "tencent", "alibaba"} {
		c := Config{Auth: auth, AccessKeyID: "synthetic-access-id", PlanID: "synthetic-team", URL: "https://unrelated.invalid/"}
		req, err := CloudRequest(context.Background(), c, "synthetic-secret", now)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.Host == "unrelated.invalid" || !strings.Contains(req.Header.Get("Authorization"), "synthetic-access-id") {
			t.Fatalf("%s signature lost fixed audience", auth)
		}
		if strings.Contains(req.Header.Get("Authorization"), "synthetic-secret") {
			t.Fatalf("%s sent raw signing secret", auth)
		}
	}
}

func TestCloudReadersPreserveWindowsAndUnits(t *testing.T) {
	now := time.Now().UTC()
	fixtures := []struct {
		preset, body, unit string
		used               float64
	}{
		{"volcengine-coding", `{"Result":{"QuotaUsage":[{"Level":"weekly","Percent":42,"ResetTimestamp":1900000000}]}}`, "", 42},
		{"volcengine-agent", `{"Result":{"Tier":"Pro","AFPWeekly":{"Used":12,"Quota":100,"ResetTime":1900000000000}}}`, "", 12},
		{"tencent", `{"Response":{"Name":"Test team","Status":"enable","ProductType":"enterprise-auto","PackageInfo":{"CycleQuota":1000,"TotalUsed":50}}}`, "tokens", 5},
		{"alibaba", `{"data":{"success":true,"DataV2":{"data":{"data":{"per5HourPercentage":0.25,"per5HourResetTime":1900000000}}}}}`, "", 25},
		{"stepfun-plan", `{"five_hour_usage_left_rate":0.75,"five_hour_usage_reset_time":"1900000000"}`, "", 25},
	}
	for _, f := range fixtures {
		result, err := ParsePlan([]byte(f.body), Config{Preset: f.preset}, now)
		if err != nil {
			t.Fatalf("%s: %v", f.preset, err)
		}
		if len(result.Allowance.Windows) != 1 || *result.Allowance.Windows[0].UsedPercent != f.used || result.Allowance.Windows[0].Unit != f.unit {
			t.Fatalf("%s: %+v", f.preset, result)
		}
	}
}
