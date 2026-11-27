package quotasource

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

const MaxResponseBytes = 1 << 20
const MaxExpressionBytes = 8192

// ReadParts preserves Magpie's labeled, partially successful balance expressions.
// One missing field does not destroy other amounts; an entirely failed response
// is a query failure, never a reading at zero.
func ReadParts(body []byte, expression string) ([]BalancePart, error) {
	if err := validateInput(body, expression); err != nil {
		return nil, err
	}
	return readBalanceParts(body, expression)
}

// ReadNumber evaluates one numeric expression without display formatting.
func ReadNumber(body []byte, expression string) (*float64, error) {
	if err := validateInput(body, expression); err != nil {
		return nil, err
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return nil, errors.New("quota response is not JSON")
	}
	e := &balanceExpr{src: strings.TrimSpace(expression), reply: value}
	n, err := e.number()
	if err != nil {
		return nil, err
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, errors.New("quota expression is not finite")
	}
	return &n, nil
}

// ReadValue reads a path, including filtered collections, for timestamp and
// model-scope fields. Expressions are deliberately not executable code.
func ReadValue(body []byte, path string) (any, error) {
	if err := validateInput(body, path); err != nil {
		return nil, err
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return nil, errors.New("quota response is not JSON")
	}
	return (&balanceExpr{reply: value}).at(path)
}

func validateInput(body []byte, expression string) error {
	if len(body) > MaxResponseBytes {
		return errors.New("quota response exceeds size limit")
	}
	if len(expression) == 0 || len(expression) > MaxExpressionBytes {
		return errors.New("quota expression is empty or exceeds size limit")
	}
	depth := 0
	for _, c := range expression {
		switch c {
		case '(', '[':
			depth++
			if depth > 64 {
				return errors.New("quota expression exceeds nesting limit")
			}
		case ')', ']':
			depth--
			if depth < 0 {
				return errors.New("quota expression has unbalanced brackets")
			}
		}
	}
	if depth != 0 || strings.Count(expression, ";") > 63 {
		return errors.New("quota expression has unbalanced brackets or too many parts")
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// numericPart retains precise values beside display text; formatted money is
// never parsed back into routing data.
func numericPart(body []byte, expression string, part BalancePart) BalancePart {
	expression = strings.TrimSpace(expression)
	for sign, unit := range map[string]string{"$": "USD", "¥": "CNY", "€": "EUR", "£": "GBP"} {
		if strings.HasPrefix(expression, sign) {
			part.Unit = unit
			expression = strings.TrimSpace(strings.TrimPrefix(expression, sign))
			break
		}
	}
	expression = strings.TrimSpace(strings.TrimSuffix(expression, "%"))
	part.Value, _ = ReadNumber(body, expression)
	return part
}
