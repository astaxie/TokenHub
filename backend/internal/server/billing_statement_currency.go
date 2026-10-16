package server

import (
	"fmt"
	"math/big"
	"strings"

	"tokenhub/backend/internal/metering"
)

type statementCurrencyConversion struct {
	Currency        string `json:"currency"`
	USDExchangeRate string `json:"usd_exchange_rate"`
}

func (q *statementQuery) validateDisplayCurrency() error {
	q.DisplayCurrency = strings.ToUpper(strings.TrimSpace(q.DisplayCurrency))
	q.DisplayExchangeRate = strings.TrimSpace(q.DisplayExchangeRate)
	invalid := func() error {
		return NewHTTPError(400, "invalid_statement_conversion", "Select USD, CNY, JPY, or RUB and a positive decimal display_exchange_rate (1 USD = target currency); USD requires a rate of 1")
	}
	switch q.DisplayCurrency {
	case "":
		if q.DisplayExchangeRate != "" {
			return invalid()
		}
		return nil
	case "USD":
		if q.DisplayExchangeRate == "" {
			q.DisplayExchangeRate = "1"
		}
	case "CNY", "JPY", "RUB":
	default:
		return invalid()
	}
	rate, err := metering.Decimal(q.DisplayExchangeRate)
	if err != nil || rate.Sign() <= 0 || q.DisplayCurrency == "USD" && rate.Cmp(big.NewRat(1, 1)) != 0 {
		return invalid()
	}
	q.DisplayExchangeRate = rate.FloatString(12)
	return nil
}

func convertStatementCurrency(out *statementResult) error {
	if out.Query.DisplayCurrency == "" {
		return nil
	}
	rate, err := metering.Decimal(out.Query.DisplayExchangeRate)
	if err != nil {
		return err
	}
	out.Conversion = &statementCurrencyConversion{Currency: out.Query.DisplayCurrency, USDExchangeRate: out.Query.DisplayExchangeRate}
	out.DisplayTotals = map[string]string{}
	sums := map[string]*big.Rat{}
	for i := range out.Rows {
		row := &out.Rows[i]
		// A present pointer to nil distinguishes an unknown converted amount
		// from an omitted conversion in the backward-compatible response.
		var amount *string
		row.DisplayAmount = &amount
		basis, multiplier := row.USD, rate
		if basis == nil && row.Currency == out.Conversion.Currency {
			basis, multiplier = row.Amount, big.NewRat(1, 1)
		}
		if basis == nil {
			continue
		}
		usd, ok := new(big.Rat).SetString(*basis)
		if !ok {
			return fmt.Errorf("invalid stored statement USD amount")
		}
		converted := new(big.Rat).Mul(usd, multiplier)
		amount = statementString(roundStatementCurrency(converted))
		if sums[row.Source] == nil {
			sums[row.Source] = new(big.Rat)
		}
		sums[row.Source].Add(sums[row.Source], converted)
	}
	for source, amount := range sums {
		out.DisplayTotals[source] = roundStatementCurrency(amount)
	}
	var margin *string
	out.DisplayEstimatedMargin = &margin
	if out.EstimatedMarginUSD != nil {
		converted := new(big.Rat)
		if amount := sums["tenant"]; amount != nil {
			converted.Add(converted, amount)
		}
		if amount := sums["provider_estimate"]; amount != nil {
			converted.Sub(converted, amount)
		}
		margin = statementString(roundStatementCurrency(converted))
	}
	return nil
}

// Convert each exact aggregate once, using symmetric half-even rounding for
// signed amounts, including supplier refunds and negative estimated margins.
func roundStatementCurrency(amount *big.Rat) string {
	scale := big.NewInt(1000000000000)
	scaled := new(big.Rat).Mul(amount, new(big.Rat).SetInt(scale))
	numerator := new(big.Int).Abs(scaled.Num())
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(numerator, scaled.Denom(), remainder)
	comparison := new(big.Int).Lsh(remainder, 1).Cmp(scaled.Denom())
	if comparison > 0 || comparison == 0 && integer.Bit(0) == 1 {
		integer.Add(integer, big.NewInt(1))
	}
	if amount.Sign() < 0 {
		integer.Neg(integer)
	}
	return new(big.Rat).SetFrac(integer, scale).FloatString(12)
}
