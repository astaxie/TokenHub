package reconciliation

import (
	"strings"
	"testing"
	"time"
)

func TestReconciliationRequiresKnownProviderCostIncludingFree(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, granularity := range []string{GranularityDay, GranularityDetail} {
		t.Run(granularity, func(t *testing.T) {
			run := Run{ID: "run", PeriodStart: at, PeriodEnd: at.Add(24 * time.Hour), Granularity: granularity, MatchDimensions: []string{"request_id", "currency"}, Timezone: "UTC", Currency: "USD", AmountTolerance: "0", RatioTolerance: "0", USDExchangeRate: "1"}
			usage := Usage{ID: "usage", RequestID: "request", CostUSD: 100, ProviderCostUSD: 0, CreatedAt: at}
			bills := []BillingRecord{{ID: "bill", ExternalRequestID: "request", Currency: "USD", NetAmount: "0", UsageStartAt: at}}
			if _, _, err := calculate(run, bills, []Usage{usage}); err == nil || !strings.Contains(err.Error(), "provider cost is pending") {
				t.Fatalf("unknown cost fell back to tenant charge: %v", err)
			}
			usage.ProviderCostKnown = true
			result, items, err := calculate(run, bills, []Usage{usage})
			if err != nil {
				t.Fatal(err)
			}
			if result.TokenHubAmount != "0" && result.TokenHubAmount != "0.000000" {
				t.Fatalf("free cost=%s", result.TokenHubAmount)
			}
			if len(items) != 1 || items[0].Status != Matched {
				t.Fatalf("free cost not matched: %+v", items)
			}
		})
	}
}
