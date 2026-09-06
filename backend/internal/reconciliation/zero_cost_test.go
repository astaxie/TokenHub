package reconciliation

import (
	"testing"
	"time"
)

func TestZeroProviderCostNeverUsesTenantPrice(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, granularity := range []string{GranularityDetail, GranularityDay} {
		run := Run{PeriodStart: at, PeriodEnd: at.Add(24 * time.Hour), Timezone: "UTC", Currency: "USD", USDExchangeRate: "1", AmountTolerance: "0", RatioTolerance: "0", Granularity: granularity, MatchDimensions: []string{"model"}}
		got, _, err := calculate(run, nil, []Usage{{ID: "zero", RequestID: "request", ModelName: "model", CostUSD: 10, ProviderCostUSD: 0, ProviderCostKnown: true, CreatedAt: at}})
		if err != nil {
			t.Fatal(err)
		}
		if got.TokenHubAmount != "0" {
			t.Fatalf("granularity %s: provider amount %s", granularity, got.TokenHubAmount)
		}
	}
}
