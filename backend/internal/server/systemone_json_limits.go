package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	systemOneMaxJSONBytes      = 8 << 20
	systemOneMaxJSONNodes      = 32768
	systemOneMaxJSONDepth      = 64
	systemOneMaxEnvelopeDepth  = systemOneMaxJSONDepth + 4
	systemOneMaxExponentDigits = 128
)

var errSystemOneJSONLimit = errors.New("system one JSON is invalid, has duplicate members, or exceeds resource limits")

// Count containers, scalar values, and object keys across the entire payload.
// Stop before decoding an unbounded collection into maps, slices, or targets.
type systemOneJSONBudget struct {
	nodes int
	bytes int
}

func (b *systemOneJSONBudget) take() error {
	b.nodes++
	if b.nodes > systemOneMaxJSONNodes {
		return errSystemOneJSONLimit
	}
	return nil
}

func (b *systemOneJSONBudget) decode(data []byte, maxDepth int) (any, error) {
	if len(data) > systemOneMaxJSONBytes-b.bytes {
		return nil, errSystemOneJSONLimit
	}
	b.bytes += len(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := b.value(decoder, 0, maxDepth)
	if err != nil {
		return nil, errSystemOneJSONLimit
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errSystemOneJSONLimit
	}
	return value, nil
}

func (b *systemOneJSONBudget) value(decoder *json.Decoder, depth, maxDepth int) (any, error) {
	if err := b.take(); err != nil {
		return nil, err
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if number, ok := token.(json.Number); ok && !systemOneExponentAllowed(number) {
		return nil, errSystemOneJSONLimit
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	if depth >= maxDepth {
		return nil, errSystemOneJSONLimit
	}
	var result any
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			if err := b.take(); err != nil {
				return nil, err
			}
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return nil, errSystemOneJSONLimit
			}
			if _, exists := object[name]; exists {
				return nil, errSystemOneJSONLimit
			}
			child, err := b.value(decoder, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			object[name] = child
		}
		result = object
	case '[':
		array := []any{}
		for decoder.More() {
			child, err := b.value(decoder, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			array = append(array, child)
		}
		result = array
	default:
		return nil, errSystemOneJSONLimit
	}
	closing, err := decoder.Token()
	if err != nil || delimiter == '{' && closing != json.Delim('}') || delimiter == '[' && closing != json.Delim(']') {
		return nil, errSystemOneJSONLimit
	}
	return result, nil
}

func systemOneExponentAllowed(number json.Number) bool {
	text := number.String()
	index := strings.IndexAny(text, "eE")
	if index < 0 {
		return true
	}
	exponent := strings.TrimLeft(text[index+1:], "+-")
	return len(exponent) <= systemOneMaxExponentDigits
}

func validateSystemOneJSONValue(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errSystemOneJSONLimit
	}
	decoded, err := (&systemOneJSONBudget{}).decode(data, systemOneMaxEnvelopeDepth)
	if err == nil {
		_, response := value.(SystemOneResponse)
		err = systemOneCanonicalFields(decoded, response)
	}
	return err
}

func (response *SystemOneResponse) UnmarshalJSON(data []byte) error {
	decoded, err := (&systemOneJSONBudget{}).decode(data, systemOneMaxEnvelopeDepth)
	if err != nil {
		return err
	}
	if err := systemOneCanonicalFields(decoded, true); err != nil {
		return err
	}
	type wire SystemOneResponse
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*response = SystemOneResponse(value)
	return nil
}

// encoding/json matches struct fields case-insensitively. Reject alternate
// spellings of protocol fields so they cannot act as duplicate assignments.
// Arbitrary user object keys and question/criterion IDs remain case-sensitive.
func systemOneCanonicalFields(value any, response bool) error {
	root, _ := value.(map[string]any)
	fields := []string{"model", "state", "questions"}
	group, memberFields := "questions", []string{"type", "instructions", "criteria"}
	if response {
		fields = []string{"model", "answers", "usage"}
		group, memberFields = "answers", []string{"type", "choice", "noul", "score", "confidence", "probabilities", "legend"}
		if !systemOneExactFieldNames(root["usage"], "input_tokens", "output_tokens") {
			return errSystemOneJSONLimit
		}
	}
	if !systemOneExactFieldNames(root, fields...) {
		return errSystemOneJSONLimit
	}
	members, _ := root[group].(map[string]any)
	for _, member := range members {
		if !systemOneExactFieldNames(member, memberFields...) {
			return errSystemOneJSONLimit
		}
	}
	return nil
}

func systemOneExactFieldNames(value any, fields ...string) bool {
	object, _ := value.(map[string]any)
	for key := range object {
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				return false
			}
		}
	}
	return true
}
