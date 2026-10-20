package metering

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// PriceDuration retains fractional audio seconds as native units. It never
// rounds duration up to an integer or converts duration into token counts.
var durationPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Duration validates a finite JSON number without allowing arbitrarily large
// exponents to allocate unbounded rational denominators.
func Duration(seconds string) (*big.Rat, error) {
	if len(seconds) > 512 || !durationPattern.MatchString(seconds) {
		return nil, fmt.Errorf("invalid audio duration")
	}
	if index := strings.IndexAny(seconds, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(seconds[index+1:])
		if err != nil || exponent < -400 || exponent > 400 {
			return nil, fmt.Errorf("unsupported audio duration precision")
		}
	}
	value, err := strconv.ParseFloat(seconds, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("invalid audio duration")
	}
	quantity, ok := new(big.Rat).SetString(seconds)
	if !ok || quantity.Sign() < 0 || (value == 0 && quantity.Sign() != 0) {
		return nil, fmt.Errorf("invalid audio duration")
	}
	return quantity, nil
}

func PriceDuration(seconds, rate string) (Charge, error) {
	quantity, err := Duration(seconds)
	if err != nil {
		return Charge{}, err
	}
	price, err := Decimal(rate)
	if err != nil {
		return Charge{}, err
	}
	amount := new(big.Rat).Mul(quantity, price)
	if finite, _ := amount.Float64(); math.IsInf(finite, 0) {
		return Charge{}, fmt.Errorf("audio charge exceeds supported range")
	}
	total := round(amount, false)
	return Charge{Currency: "USD", Amount: total, USD: total, ExchangeRate: "1", Lines: []Line{{Kind: "audio_second", Quantity: seconds, Rate: rate, Amount: total}}}, nil
}
