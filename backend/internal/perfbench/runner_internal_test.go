package perfbench

import "testing"

func TestRateResultBufferSizeIsBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
		want   int
	}{
		{name: "explicit extreme values", config: Config{Rate: 1_000_000_000, MaxInFlight: 1_000_000_000}, want: 1000},
		{name: "max in flight below cap", config: Config{Rate: 1000, MaxInFlight: 25}, want: 25},
		{name: "rate below cap", config: Config{Rate: 10, MaxInFlight: 1000}, want: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rateResultBufferSize(test.config); got != test.want {
				t.Fatalf("rate result buffer size = %d, want %d", got, test.want)
			}
		})
	}
}
