package server

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelPricingValidationCacheInvalidatesWithoutTimestampChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProviderModel)
	}{
		{"input", func(p *ProviderModel) { p.InputPriceUSDPer1M = -1 }},
		{"output", func(p *ProviderModel) { p.OutputPriceUSDPer1M = -1 }},
		{"cache_read", func(p *ProviderModel) { p.CacheReadPriceUSDPer1M = -1 }},
		{"cache_write", func(p *ProviderModel) { p.CacheWritePriceUSDPer1M = -1 }},
		{"cache_write_5m", func(p *ProviderModel) { p.CacheWrite5mPriceUSDPer1M = -1 }},
		{"cache_write_1h", func(p *ProviderModel) { p.CacheWrite1hPriceUSDPer1M = -1 }},
		{"timezone", func(p *ProviderModel) { p.PricingPeriods[0].Timezone = "Invalid/Zone" }},
		{"weekday", func(p *ProviderModel) { p.PricingPeriods[0].Weekdays = []int{7} }},
		{"effective_from", func(p *ProviderModel) { p.PricingPeriods[0].EffectiveFrom = "invalid" }},
		{"effective_until", func(p *ProviderModel) { p.PricingPeriods[0].EffectiveUntil = "invalid" }},
		{"clock", func(p *ProviderModel) { p.PricingPeriods[0].EndTime = "25:00" }},
		{"period_price", func(p *ProviderModel) { p.PricingPeriods[0].InputPriceUSDPer1M = new(-1.0) }},
		{"overlap", func(p *ProviderModel) { p.PricingPeriods = append(p.PricingPeriods, p.PricingPeriods[0]) }},
		{"audio_metadata", func(p *ProviderModel) { p.Metadata[audioSecondPriceKey] = "-1" }},
		{"retrieval_metadata", func(p *ProviderModel) { p.Metadata[retrievalSearchUnitPriceKey] = "-1" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cache modelPricingValidationCache
			source := ProviderModel{ID: "same-source", UpdatedAt: time.Unix(1, 0), Metadata: map[string]string{"pricing_status": "configured"}, PricingPeriods: []ModelPricingPeriod{{Timezone: "UTC", StartTime: "08:00", EndTime: "09:00"}}}
			calls := 0
			validate := func(source ProviderModel) error {
				calls++
				return validateProviderModelCosts(source)
			}
			for range 2 {
				if err := cache.validate(source, validate); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatalf("unchanged policy validated %d times", calls)
			}
			test.mutate(&source)
			for range 2 {
				if err := cache.validate(source, validate); err == nil {
					t.Fatal("corrupt policy reused successful validation")
				}
			}
			if calls != 2 {
				t.Fatalf("changed policy was not validated exactly once: %d", calls)
			}
		})
	}
}

func TestModelPricingValidationCacheCoalescesConcurrentPolicies(t *testing.T) {
	var cache modelPricingValidationCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseValidation := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseValidation()
	validate := func(source ProviderModel) error {
		if source.ID == "shared" {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
		}
		return nil
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := cache.validate(ProviderModel{ID: "shared"}, validate); err != nil {
				t.Error(err)
			}
		})
	}
	<-started
	independent := make(chan error, 1)
	go func() { independent <- cache.validate(ProviderModel{ID: "other"}, validate) }()
	select {
	case err := <-independent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("validation held the cache lock while processing another policy")
	}
	releaseValidation()
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("shared policy validated %d times", calls.Load())
	}
}

func TestModelPricingValidationCacheBoundsEntriesAndRejectsNonFinitePrices(t *testing.T) {
	var cache modelPricingValidationCache
	for i := range modelPricingValidationCacheLimit + 1 {
		if err := cache.validate(ProviderModel{ID: fmt.Sprintf("source-%d", i)}, validateProviderModelCosts); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) != modelPricingValidationCacheLimit {
		t.Fatalf("unexpected cache size: %d", len(cache.entries))
	}
	if err := cache.validate(ProviderModel{InputPriceUSDPer1M: math.NaN()}, validateProviderModelCosts); err == nil {
		t.Fatal("non-finite source configuration accepted")
	}
}

func BenchmarkModelPricingValidation(b *testing.B) {
	source := ProviderModel{ID: "maximum-window-policy"}
	for i := range 64 {
		start, end := i*15, (i+1)*15
		source.PricingPeriods = append(source.PricingPeriods, ModelPricingPeriod{Timezone: "UTC", StartTime: fmt.Sprintf("%02d:%02d", start/60, start%60), EndTime: fmt.Sprintf("%02d:%02d", end/60, end%60)})
	}
	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := validateProviderModelCosts(source); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		var cache modelPricingValidationCache
		if err := cache.validate(source, validateProviderModelCosts); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := cache.validate(source, validateProviderModelCosts); err != nil {
				b.Fatal(err)
			}
		}
	})
}
