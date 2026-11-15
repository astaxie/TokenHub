package quotasource

import (
	"errors"
	"testing"
	"time"
	"tokenhub/backend/internal/allowance"
)

func TestTencentNativeListCompletesEachPlanAndKeepsFallback(t *testing.T) {
	body := []byte(`{"Response":{"TokenPlanSet":[{"TeamId":"one","PackageInfo":{"CycleQuota":100,"TotalUsed":10}},{"TeamId":"two","PackageInfo":{"CycleQuota":200,"TotalUsed":30}}]}}`)
	merged := CompleteTencentPlanList(body, func(team string) ([]byte, error) {
		if team == "two" {
			return nil, errors.New("synthetic detail failure")
		}
		return []byte(`{"Response":{"PackageInfo":{"CycleQuota":100,"TotalUsed":80},"TokenSummary":{"CycleEndTime":"2026-12-01T00:00:00Z"}}}`), nil
	})
	result, err := ParsePlan(merged, Config{Preset: "tencent"}, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || len(result.Allowance.Windows) != 2 {
		t.Fatalf("list read failed: %+v %v", result, err)
	}
	one, two := result.Allowance.Windows[0], result.Allowance.Windows[1]
	if one.UsedPercent == nil || *one.UsedPercent != 80 || one.ResetAt == nil || !one.Informational || two.UsedPercent == nil || *two.UsedPercent != 15 || !two.Informational {
		t.Fatalf("detail or fallback lost: %+v %+v", one, two)
	}
}

func TestTencentUnboundSinglePlanHonorsStoppedDetail(t *testing.T) {
	for _, reason := range []string{"FROZEN", "ISOLATED"} {
		t.Run(reason, func(t *testing.T) {
			body := []byte(`{"Response":{"TokenPlanSet":[{"TeamId":"one","Status":"enable","PackageInfo":{"CycleQuota":100,"TotalUsed":20}}]}}`)
			merged := CompleteTencentPlanList(body, func(string) ([]byte, error) {
				return []byte(`{"Response":{"Status":"disable","StopReason":"` + reason + `","PackageInfo":{"CycleQuota":100,"TotalUsed":20}}}`), nil
			})
			now := time.Now().UTC()
			result, err := ParsePlan(merged, Config{Preset: "tencent"}, now)
			if err != nil || result.Allowance.For("model", now, time.Minute).Status != allowance.Exhausted {
				t.Fatalf("stopped plan advertised spare quota: %+v %v", result, err)
			}
		})
	}
}

func TestTencentSingleNativePlanIsAccountQuota(t *testing.T) {
	body := []byte(`{"Response":{"TokenPlanSet":[{"TeamId":"one","PackageInfo":{"CycleQuota":100,"TotalUsed":20}}]}}`)
	result, err := ParsePlan(body, Config{Preset: "tencent"}, time.Now())
	if err != nil || len(result.Allowance.Windows) != 1 || result.Allowance.Windows[0].Informational {
		t.Fatalf("unambiguous plan cannot enforce account caps: %+v %v", result, err)
	}
}
