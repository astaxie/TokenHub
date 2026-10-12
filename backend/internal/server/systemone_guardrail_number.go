package server

import (
	"encoding/json"
	"strconv"
	"strings"
)

func systemOneGuardrailIntegerText(number json.Number) string {
	// Also inspect integer-valued scientific notation, which can encode phone
	// numbers and numeric IDs. Bound exponent parsing before any expansion.
	original := number.String()
	coefficient, power := original, 0
	if index := strings.IndexAny(coefficient, "eE"); index >= 0 {
		exponent := coefficient[index+1:]
		coefficient = coefficient[:index]
		negative := strings.HasPrefix(exponent, "-")
		exponent = strings.TrimLeft(strings.TrimLeft(exponent, "+-"), "0")
		if len(exponent) > 18 {
			return original
		}
		if exponent != "" {
			var err error
			power, err = strconv.Atoi(exponent)
			if err != nil {
				return original
			}
		}
		if negative {
			power = -power
		}
		// Decimal places and trailing zeros cannot offset a larger exponent.
		// Check before shifting to keep arithmetic within the input's bounds.
		if power > len(coefficient)+64 || power < -len(coefficient) {
			return original
		}
	}
	negative := strings.HasPrefix(coefficient, "-")
	coefficient = strings.TrimPrefix(coefficient, "-")
	if index := strings.IndexByte(coefficient, '.'); index >= 0 {
		power -= len(coefficient) - index - 1
		coefficient = coefficient[:index] + coefficient[index+1:]
	}
	coefficient = strings.TrimLeft(coefficient, "0")
	if coefficient == "" {
		return "0"
	}
	trimmed := strings.TrimRight(coefficient, "0")
	power += len(coefficient) - len(trimmed)
	coefficient = trimmed
	if negative {
		coefficient = "-" + coefficient
	}
	if power < 0 || power > 64 || len(coefficient) > 64-power {
		return original
	}
	return coefficient + strings.Repeat("0", power)
}
