package server

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

func systemOneEntriesEqual(left, right json.RawMessage) bool {
	decode := func(data json.RawMessage) (any, error) {
		value, err := (&systemOneJSONBudget{}).decode(data, systemOneMaxJSONDepth)
		if err != nil {
			return nil, err
		}
		return normalizeSystemOneJSON(value)
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	return err == nil && reflect.DeepEqual(a, b)
}

func normalizeSystemOneJSON(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		return normalizeSystemOneNumber(typed)
	case []any:
		for index := range typed {
			var err error
			typed[index], err = normalizeSystemOneJSON(typed[index])
			if err != nil {
				return nil, err
			}
		}
	case map[string]any:
		for key := range typed {
			var err error
			typed[key], err = normalizeSystemOneJSON(typed[key])
			if err != nil {
				return nil, err
			}
		}
	}
	return value, nil
}

func normalizeSystemOneNumber(value json.Number) (json.Number, error) {
	if !systemOneExponentAllowed(value) {
		return "", errSystemOneJSONLimit
	}
	mantissa := string(value)
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		if _, ok := exponent.SetString(mantissa[index+1:], 10); !ok {
			return "", errSystemOneJSONLimit
		}
		mantissa = mantissa[:index]
	}
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(mantissa)-index-1)))
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return json.Number("0"), nil
	}
	coefficient := strings.TrimRight(mantissa, "0")
	exponent.Add(exponent, big.NewInt(int64(len(mantissa)-len(coefficient))))
	// Keep exponents symbolic; expanding an arbitrary JSON exponent can exhaust memory.
	return json.Number(sign + coefficient + "e" + exponent.String()), nil
}
