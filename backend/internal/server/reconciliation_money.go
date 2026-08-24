package server

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

const (
	reconciliationScale       int64 = 1_000_000_000
	reconciliationOutputScale int64 = 1_000_000
	reconciliationOutputRatio int64 = reconciliationScale / reconciliationOutputScale
)

type reconciliationMoney int64

func parseReconciliationMoney(value string) (reconciliationMoney, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "0"
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, fmt.Errorf("invalid decimal amount %q", value)
	}
	scaled := new(big.Rat).Mul(rational, big.NewRat(reconciliationScale, 1))
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
	return reconciliationMoney(parsed), nil
}

func reconciliationMoneyFromFloat(value float64) (reconciliationMoney, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid floating-point amount")
	}
	return parseReconciliationMoney(strconv.FormatFloat(value, 'f', 9, 64))
}

func (value reconciliationMoney) String() string {
	return formatScaledReconciliationMoney(value, reconciliationScale, 9)
}

func (value reconciliationMoney) OutputString() string {
	amount := int64(value)
	negative := amount < 0
	if negative {
		amount = -amount
	}
	quotient, remainder := amount/reconciliationOutputRatio, amount%reconciliationOutputRatio
	if remainder*2 >= reconciliationOutputRatio {
		quotient++
	}
	if negative {
		quotient = -quotient
	}
	return formatScaledReconciliationMoney(reconciliationMoney(quotient), reconciliationOutputScale, 6)
}

func formatScaledReconciliationMoney(value reconciliationMoney, scale int64, precision int) string {
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

func addReconciliationMoney(left reconciliationMoney, right reconciliationMoney) (reconciliationMoney, error) {
	if right > 0 && left > reconciliationMoney(math.MaxInt64)-right {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	if right < 0 && left < reconciliationMoney(math.MinInt64)-right {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	result := left + right
	if result == reconciliationMoney(math.MinInt64) {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	return result, nil
}

func multiplyReconciliationMoney(amount reconciliationMoney, rate reconciliationMoney) (reconciliationMoney, error) {
	product := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(int64(rate)))
	negative := product.Sign() < 0
	product.Abs(product)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, big.NewInt(reconciliationScale), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(reconciliationScale)) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	if !quotient.IsInt64() || quotient.Int64() == math.MinInt64 {
		return 0, fmt.Errorf("reconciliation amount overflow")
	}
	return reconciliationMoney(quotient.Int64()), nil
}

func absoluteReconciliationMoney(value reconciliationMoney) reconciliationMoney {
	if value < 0 {
		return -value
	}
	return value
}

func reconciliationRatio(difference reconciliationMoney, provider reconciliationMoney) reconciliationMoney {
	denominator := absoluteReconciliationMoney(provider)
	if denominator == 0 {
		if difference == 0 {
			return 0
		}
		return reconciliationMoney(reconciliationScale)
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(absoluteReconciliationMoney(difference))), big.NewInt(reconciliationScale))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(int64(denominator)), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(big.NewInt(int64(denominator))) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return reconciliationMoney(math.MaxInt64)
	}
	return reconciliationMoney(quotient.Int64())
}
