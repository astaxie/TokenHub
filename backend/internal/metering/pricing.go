// Package metering implements exact prices independently of transport and storage.
package metering

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

type Rates struct {
	Input        string `json:"input"`
	CacheRead    string `json:"cache_read"`
	CacheWrite   string `json:"cache_write"`
	CacheWrite5m string `json:"cache_write_5m"`
	CacheWrite1h string `json:"cache_write_1h"`
	Output       string `json:"output"`
}
type Units struct {
	Input        int64 `json:"input"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Output       int64 `json:"output"`
}
type Line struct {
	Kind   string `json:"kind"`
	Units  int64  `json:"units"`
	Rate   string `json:"rate"`
	Amount string `json:"amount"`
}
type Charge struct {
	Currency     string `json:"currency"`
	Amount       string `json:"amount"`
	USD          string `json:"usd,omitempty"`
	ExchangeRate string `json:"exchange_rate,omitempty"`
	Lines        []Line `json:"lines"`
}

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,12})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func Decimal(value string) (*big.Rat, error) {
	if !decimalPattern.MatchString(value) {
		return nil, fmt.Errorf("invalid decimal: expected a non-negative decimal with at most 18 integer and 12 fractional digits")
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return parsed, nil
}
func (r Rates) values() []string {
	return []string{r.Input, r.CacheRead, r.CacheWrite, r.CacheWrite5m, r.CacheWrite1h, r.Output}
}
func (u Units) values() []int64 {
	return []int64{u.Input, u.CacheRead, u.CacheWrite, u.CacheWrite5m, u.CacheWrite1h, u.Output}
}

var names = []string{"input", "cache_read", "cache_write", "cache_write_5m", "cache_write_1h", "output"}

func (r Rates) Validate(complete bool) error {
	for i, value := range r.values() {
		if value == "" && !complete {
			continue
		}
		if _, err := Decimal(value); err != nil {
			return fmt.Errorf("%s: %w", names[i], err)
		}
	}
	return nil
}
func Price(r Rates, u Units, currency, fx string) (Charge, error) {
	result := Charge{Currency: currency, ExchangeRate: fx, Lines: []Line{}}
	if !currencyPattern.MatchString(currency) {
		return result, fmt.Errorf("currency must be a three-letter uppercase code")
	}
	if err := r.Validate(false); err != nil {
		return result, err
	}
	total := new(big.Rat)
	values := r.values()
	for i, count := range u.values() {
		if count < 0 {
			return result, fmt.Errorf("%s units must be non-negative", names[i])
		}
		if count == 0 {
			continue
		}
		rate, err := Decimal(values[i])
		if err != nil {
			return result, fmt.Errorf("%s price missing or invalid", names[i])
		}
		amount := new(big.Rat).Mul(rate, new(big.Rat).SetFrac(big.NewInt(count), big.NewInt(1000000)))
		total.Add(total, amount)
		result.Lines = append(result.Lines, Line{names[i], count, values[i], round(amount, false)})
	}
	result.Amount = round(total, false)
	if currency == "USD" {
		result.USD = result.Amount
		result.ExchangeRate = "1"
	} else if fx != "" {
		rate, err := Decimal(fx)
		if err != nil || rate.Sign() <= 0 {
			return Charge{}, fmt.Errorf("exchange rate must be positive")
		}
		result.USD = round(new(big.Rat).Mul(total, rate), false)
	}
	return result, nil
}

// Reserve uses the most expensive allowed input category and a caller-enforced
// output cap. It must not be called with a heuristic input estimate.
func Reserve(r Rates, inputBound, outputBound int64) (string, error) {
	if inputBound < 0 || outputBound < 0 {
		return "", fmt.Errorf("bounds must be non-negative")
	}
	if err := r.Validate(true); err != nil {
		return "", err
	}
	maxInput := new(big.Rat)
	for _, value := range r.values()[:5] {
		rate, _ := Decimal(value)
		if rate.Cmp(maxInput) > 0 {
			maxInput = rate
		}
	}
	output, _ := Decimal(r.Output)
	total := new(big.Rat).Mul(maxInput, new(big.Rat).SetInt64(inputBound))
	total.Add(total, new(big.Rat).Mul(output, new(big.Rat).SetInt64(outputBound)))
	total.Quo(total, big.NewRat(1000000, 1))
	return round(total, true), nil
}
func round(value *big.Rat, ceil bool) string {
	scaled := new(big.Rat).Mul(value, big.NewRat(1000000000000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	comparison := new(big.Int).Lsh(remainder, 1).Cmp(scaled.Denom())
	if (ceil && remainder.Sign() > 0) || (!ceil && (comparison > 0 || comparison == 0 && quotient.Bit(0) == 1)) {
		quotient.Add(quotient, big.NewInt(1))
	}
	digits := quotient.String()
	if len(digits) <= 12 {
		digits = strings.Repeat("0", 13-len(digits)) + digits
	}
	return digits[:len(digits)-12] + "." + digits[len(digits)-12:]
}
