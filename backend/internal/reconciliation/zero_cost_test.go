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

func TestUnknownProviderCostCannotMatchZeroBill(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, granularity := range []string{GranularityDetail, GranularityHour, GranularityDay, GranularityMonth} {
		t.Run(granularity, func(t *testing.T) {
			run := Run{PeriodStart: at, PeriodEnd: at.Add(24 * time.Hour), Timezone: "UTC", Currency: "USD", USDExchangeRate: "1", AmountTolerance: "0", RatioTolerance: "0", Granularity: granularity, MatchDimensions: []string{"model"}}
			bill := BillingRecord{ID: "bill", Model: "model", NetAmount: "0", Currency: "USD", UsageStartAt: at}
			usage := Usage{ID: "unknown", ModelName: "model", CreatedAt: at}
			_, items, err := calculate(run, []BillingRecord{bill}, []Usage{usage})
			_, code, _, _ := ErrorInfo(err)
			if code != "reconciliation_provider_cost_unknown" || len(items) != 0 {
				t.Fatalf("unknown cost matched: items=%v err=%v", items, err)
			}
			usage.ProviderCostKnown = true
			_, items, err = calculate(run, []BillingRecord{bill}, []Usage{usage})
			if err != nil || len(items) != 1 || items[0].Status != Matched {
				t.Fatalf("explicit free cost did not match: %v %v", items, err)
			}
		})
	}
}
