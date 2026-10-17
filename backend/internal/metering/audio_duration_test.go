package metering

import "testing"

func TestPriceDurationKeepsFractionalSeconds(t *testing.T) {
	charge, err := PriceDuration("1.2345678901234", "0.1")
	if err != nil || charge.Amount != "0.123456789012" || len(charge.Lines) != 1 || charge.Lines[0].Quantity != "1.2345678901234" || charge.Lines[0].Kind != "audio_second" || charge.Lines[0].Units != 0 {
		t.Fatalf("unexpected native duration charge: %+v, %v", charge, err)
	}
	for _, seconds := range []string{"0", "12.5", "1e-3"} {
		charge, err := PriceDuration(seconds, "0")
		if err != nil || charge.USD != "0.000000000000" {
			t.Fatalf("explicit free duration %s: %+v, %v", seconds, charge, err)
		}
	}
}

func TestPriceDurationRejectsInvalidQuantitiesAndPrices(t *testing.T) {
	for _, seconds := range []string{"-1", "NaN", "Inf", "1e309", "", "1/2", "1e-1000000000", "1e-400", "0x1p1"} {
		if _, err := PriceDuration(seconds, "0.01"); err == nil {
			t.Fatalf("invalid duration %q accepted", seconds)
		}
	}
	for _, rate := range []string{"", "-1", "NaN", "1e-3"} {
		if _, err := PriceDuration("12.5", rate); err == nil {
			t.Fatalf("invalid rate %q accepted", rate)
		}
	}
	if _, err := PriceDuration("1e308", "100000000000000000"); err == nil {
		t.Fatal("overflowing charge accepted")
	}
}
