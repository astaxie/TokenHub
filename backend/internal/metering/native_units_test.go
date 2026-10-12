package metering

import "testing"

func TestNativeSearchUnitsAreNotTokenRates(t *testing.T) {
	charge, err := PriceNative("search_unit", 2, "0.002", "USD", "")
	if err != nil || charge.USD != "0.004000000000" || charge.Lines[0].Kind != "search_unit" {
		t.Fatalf("charge=%+v err=%v", charge, err)
	}
	for _, quantity := range []int64{0, 1} {
		if _, err := PriceNative("search_unit", quantity, "", "USD", ""); err == nil {
			t.Fatal("missing rate treated as free")
		}
	}
	if _, err := PriceNative("search_unit", -1, "1", "USD", ""); err == nil {
		t.Fatal("negative quantity accepted")
	}
}
