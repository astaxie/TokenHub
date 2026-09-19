package server

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

func systemOneEntriesEqual(left, right json.RawMessage) bool {
	decode := func(data json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return normalizeSystemOneJSON(value), nil
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	return err == nil && reflect.DeepEqual(a, b)
}

func normalizeSystemOneJSON(value any) any {
	switch typed := value.(type) {
	case json.Number:
		return normalizeSystemOneNumber(typed)
	case []any:
		for index := range typed {
			typed[index] = normalizeSystemOneJSON(typed[index])
		}
	case map[string]any:
		for key := range typed {
			typed[key] = normalizeSystemOneJSON(typed[key])
		}
	}
	return value
}

func normalizeSystemOneNumber(value json.Number) json.Number {
	mantissa := string(value)
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		exponent.SetString(mantissa[index+1:], 10)
		mantissa = mantissa[:index]
	}
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(mantissa)-index-1)))
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return json.Number("0")
	}
	coefficient := strings.TrimRight(mantissa, "0")
	exponent.Add(exponent, big.NewInt(int64(len(mantissa)-len(coefficient))))
	// Keep exponents symbolic; expanding an arbitrary JSON exponent can exhaust memory.
	return json.Number(sign + coefficient + "e" + exponent.String())
}
