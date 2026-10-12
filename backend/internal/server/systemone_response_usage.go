package server

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Invalid answers must not discard independently valid usage. Reject ambiguous
// envelope/usage members while skipping answer subtrees without materializing them.
func systemOneIndependentUsage(data []byte) SystemOneUsage {
	if len(data) > systemOneMaxJSONBytes {
		return SystemOneUsage{}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return SystemOneUsage{}
	}
	seen := map[string]bool{}
	var rawUsage json.RawMessage
	for decoder.More() {
		if len(seen) >= systemOneMaxJSONNodes {
			return SystemOneUsage{}
		}
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || key != "usage" && strings.EqualFold(key, "usage") {
			return SystemOneUsage{}
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return SystemOneUsage{}
		}
		if key == "usage" {
			rawUsage = raw
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return SystemOneUsage{}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return SystemOneUsage{}
	}
	decoded, err := (&systemOneJSONBudget{}).decode(rawUsage, systemOneMaxJSONDepth)
	if err != nil || !systemOneExactFieldNames(decoded, "input_tokens", "output_tokens") {
		return SystemOneUsage{}
	}
	var usage SystemOneUsage
	if json.Unmarshal(rawUsage, &usage) != nil {
		return SystemOneUsage{}
	}
	return usage
}
