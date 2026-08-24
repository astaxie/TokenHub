package reconciliation

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

const (
	moneyScale       int64 = 1_000_000_000
	moneyOutputScale int64 = 1_000_000
	moneyOutputRatio int64 = moneyScale / moneyOutputScale
)

type money int64

func parseMoney(value string) (money, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "0"
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, fmt.Errorf("invalid decimal amount %q", value)
	}
	scaled := new(big.Rat).Mul(rational, big.NewRat(moneyScale, 1))
	numerator := new(big.Int).Set(scaled.Num())
	denominator := new(big.Int).Set(scaled.Denom())
	negative := numerator.Sign() < 0
	numerator.Abs(numerator)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("decimal amount %q exceeds supported precision", value)
	}
	parsed := quotient.Int64()
	if parsed == math.MinInt64 {
		return 0, fmt.Errorf("decimal amount %q exceeds supported precision", value)
	}
	return money(parsed), nil
}

func moneyFromFloat(value float64) (money, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid floating-point amount")
	}
	return parseMoney(strconv.FormatFloat(value, 'f', 9, 64))
}

func (value money) String() string {
	return formatScaledMoney(value, moneyScale, 9)
}

func (value money) OutputString() string {
	amount := int64(value)
	negative := amount < 0
	if negative {
		amount = -amount
	}
	quotient, remainder := amount/moneyOutputRatio, amount%moneyOutputRatio
	if remainder*2 >= moneyOutputRatio {
		quotient++
	}
	if negative {
		quotient = -quotient
	}
	return formatScaledMoney(money(quotient), moneyOutputScale, 6)
}

func formatScaledMoney(value money, scale int64, precision int) string {
	amount := int64(value)
	negative := amount < 0
	if negative {
		amount = -amount
	}
	whole := amount / scale
	fraction := amount % scale
	formatted := strconv.FormatInt(whole, 10)
	if fraction != 0 {
		formatted += "." + strings.TrimRight(fmt.Sprintf("%0*d", precision, fraction), "0")
	}
	if negative && formatted != "0" {
		return "-" + formatted
	}
	return formatted
}

func addMoney(left money, right money) (money, error) {
	if right > 0 && left > money(math.MaxInt64)-right {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	if right < 0 && left < money(math.MinInt64)-right {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	result := left + right
	if result == money(math.MinInt64) {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	return result, nil
}

func multiplyMoney(amount money, rate money) (money, error) {
	product := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(int64(rate)))
	negative := product.Sign() < 0
	product.Abs(product)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, big.NewInt(moneyScale), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(moneyScale)) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	if !quotient.IsInt64() || quotient.Int64() == math.MinInt64 {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	return money(quotient.Int64()), nil
}

func absoluteMoney(value money) money {
	if value < 0 {
		return -value
	}
	return value
}

func ratio(difference money, provider money) money {
	denominator := absoluteMoney(provider)
	if denominator == 0 {
		if difference == 0 {
			return 0
		}
		return money(moneyScale)
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(absoluteMoney(difference))), big.NewInt(moneyScale))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(int64(denominator)), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(int64(denominator))) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return money(math.MaxInt64)
	}
	return money(quotient.Int64())
}
