package metering

import "testing"

func TestPricePreservesExactCurrencyAndConvertsBeforeRounding(t *testing.T) {
	rates := Rates{Input: "1.5", CacheRead: "0.05", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "4.5"}
	usage := Units{Input: 200000, CacheRead: 800000, Output: 10000}
	charge, err := Price(rates, usage, "CNY", "0.14")
	if err != nil {
		t.Fatal(err)
	}
	if charge.Amount != "0.385000000000" || charge.USD != "0.053900000000" {
		t.Fatalf("unexpected charge: %+v", charge)
	}
	charge, err = Price(rates, usage, "CNY", "")
	if err != nil || charge.USD != "" || charge.Amount != "0.385000000000" {
		t.Fatalf("missing FX lost original amount: %+v %v", charge, err)
	}
}

func TestPriceDistinguishesMissingFromFreeAndRejectsNegativeUsage(t *testing.T) {
	rates := Rates{Input: "2", CacheRead: "0", Output: "6"}
	free, err := Price(rates, Units{CacheRead: 80}, "USD", "")
	if err != nil || free.Amount != "0.000000000000" {
		t.Fatalf("free price: %+v %v", free, err)
	}
	rates.CacheRead = ""
	if _, err = Price(rates, Units{CacheRead: 80}, "USD", ""); err == nil {
		t.Fatal("missing price accepted")
	}
	if _, err = Price(rates, Units{Input: -1}, "USD", ""); err == nil {
		t.Fatal("negative usage accepted")
	}
}

func TestMoneyRoundsHalfEvenOnceAndReservationRoundsUp(t *testing.T) {
	for _, tc := range []struct{ rate, want string }{
		{"0.0000005", "0.000000000000"},
		{"0.0000015", "0.000000000002"},
	} {
		charge, err := Price(Rates{Input: tc.rate}, Units{Input: 1}, "USD", "")
		if err != nil || charge.Amount != tc.want {
			t.Fatalf("%s: %+v %v", tc.rate, charge, err)
		}
	}
	bound, err := Reserve(Rates{Input: "0.0000005", CacheRead: "0", CacheWrite: "0", CacheWrite5m: "0", CacheWrite1h: "0", Output: "0"}, 1, 0)
	if err != nil || bound != "0.000000000001" {
		t.Fatalf("reservation rounded down: %s %v", bound, err)
	}
}
