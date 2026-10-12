package metering

import (
	"fmt"
	"math/big"
)

// PriceNative prices an explicitly named non-token unit without converting it
// into tokens. The decimal rate is per unit, unlike token rates per million.
func PriceNative(unit string, quantity int64, rate, currency, fx string) (Charge, error) {
	if unit != "search_unit" || quantity < 0 {
		return Charge{}, fmt.Errorf("unsupported unit or negative quantity")
	}
	if !currencyPattern.MatchString(currency) {
		return Charge{}, fmt.Errorf("invalid currency")
	}
	parsed, err := Decimal(rate)
	if err != nil {
		return Charge{}, err
	}
	amount := new(big.Rat).Mul(parsed, new(big.Rat).SetInt64(quantity))
	charge := Charge{Currency: currency, Amount: round(amount, false), ExchangeRate: fx, Lines: []Line{{Kind: unit, Units: quantity, Rate: rate, Amount: round(amount, false)}}}
	if currency == "USD" {
		charge.USD = charge.Amount
		charge.ExchangeRate = "1"
	} else if fx != "" {
		exchange, err := Decimal(fx)
		if err != nil || exchange.Sign() <= 0 {
			return Charge{}, fmt.Errorf("invalid exchange rate")
		}
		charge.USD = round(new(big.Rat).Mul(amount, exchange), false)
	}
	return charge, nil
}
