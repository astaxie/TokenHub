package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemOneRejectsResourceAmplification(t *testing.T) {
	for _, location := range []string{"wide state", "aggregate fields", "exponent"} {
		t.Run(location, func(t *testing.T) {
			req := systemOneTestRequest(t)
			switch location {
			case "wide state":
				req.State = json.RawMessage(`[` + strings.Repeat(`null,`, 32768) + `null]`)
			case "aggregate fields":
				req.State = json.RawMessage(`[` + strings.Repeat(`null,`, 17000) + `null]`)
				q := req.Questions["urgent"]
				q.Instructions = req.State
				req.Questions["urgent"] = q
			case "exponent":
				req.State = json.RawMessage(`[1e` + strings.Repeat("9", 129) + `]`)
			}
			if err := req.validate(); err == nil {
				t.Fatal("resource-amplifying request was accepted")
			}
		})
	}
}

func TestSystemOneRejectsDuplicateMembers(t *testing.T) {
	for _, replacement := range []struct{ name, old, next string }{
		{"state", `"order_id":9007199254740993`, `"contact":"13812345678","contact":"safe"`},
		{"escaped member", `"order_id":9007199254740993`, `"contact":"13812345678","cont\u0061ct":"safe"`},
		{"instructions", `"Is immediate attention required?"`, `{"contact":"13812345678","contact":"safe"}`},
		{"criteria", `"refund":"Return funds"`, `"refund":"blocked","refund":"Return funds"`},
		{"question", `"urgent":`, `"urgent":{"type":"noul","instructions":"blocked"},"urgent":`},
		{"envelope", `"state":`, `"state":{"contact":"13812345678"},"state":`},
		{"field alias", `"state":`, `"State":{"contact":"13812345678"},"state":`},
		{"question field alias", `"instructions":"Is immediate attention required?"`, `"Instructions":"blocked","instructions":"safe"`},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			data := strings.Replace(systemOneFixtureRequest, replacement.old, replacement.next, 1)
			var req SystemOneRequest
			if err := json.Unmarshal([]byte(data), &req); err == nil {
				t.Fatal("duplicate JSON member was accepted")
			}
		})
	}
}

func TestSystemOneEqualityRejectsAmbiguousOrExcessiveJSON(t *testing.T) {
	for _, tc := range []struct{ name, left, right string }{
		{"duplicate member", `{"a":"blocked","a":"safe"}`, `{"a":"safe"}`},
		{"large exponent", `[1e` + strings.Repeat("9", 1_000_000) + `]`, `[1e` + strings.Repeat("9", 1_000_000) + `]`},
		{"trailing value", `{"a":1} {"a":2}`, `{"a":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if systemOneEntriesEqual(json.RawMessage(tc.left), json.RawMessage(tc.right)) {
				t.Fatal("unsafe entries compared equal")
			}
		})
	}
}

func TestSystemOneResponseRejectsDuplicateMembers(t *testing.T) {
	for _, tc := range []struct {
		name, old, next string
		validUsage      bool
	}{
		{"answer", `"choice":"refund"`, `"choice":"other","choice":"refund"`, true},
		{"legend", `"0":"low"`, `"0":"wrong","0":"low"`, true},
		{"usage", `"input_tokens":422`, `"input_tokens":1,"input_tokens":422`, false},
		{"envelope", `"usage":`, `"usage":{"input_tokens":1,"output_tokens":1},"usage":`, false},
		{"answer field alias", `"choice":"refund"`, `"Choice":"other","choice":"refund"`, true},
		{"usage field alias", `"input_tokens":422`, `"INPUT_TOKENS":1,"input_tokens":422`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(systemOneFixtureResponse, tc.old, tc.next, 1)
			response, err := decodeSystemOneResponse([]byte(data))
			if err == nil {
				t.Fatal("duplicate response member was accepted")
			}
			usage := response.meteredUsage()
			if usage.MeteringInvalid == tc.validUsage || tc.validUsage && usage.TotalTokens != 491 {
				t.Fatalf("independent usage validity=%v got=%+v", tc.validUsage, usage)
			}
		})
	}
}

func TestSystemOneJSONBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"node limit", `[` + strings.Repeat(`null,`, systemOneMaxJSONNodes-2) + `null]`, true},
		{"node overflow", `[` + strings.Repeat(`null,`, systemOneMaxJSONNodes-1) + `null]`, false},
		{"object keys count", `{"a":1}`, true},
		{"exponent limit", `1e` + strings.Repeat("9", systemOneMaxExponentDigits), true},
		{"positive exponent overflow", `1e+` + strings.Repeat("9", systemOneMaxExponentDigits+1), false},
		{"negative exponent overflow", `1e-` + strings.Repeat("9", systemOneMaxExponentDigits+1), false},
		{"zero exponent overflow", `0e` + strings.Repeat("0", systemOneMaxExponentDigits+1), false},
		{"byte limit", `"` + strings.Repeat("a", systemOneMaxJSONBytes-2) + `"`, true},
		{"byte overflow", `"` + strings.Repeat("a", systemOneMaxJSONBytes-1) + `"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := &systemOneJSONBudget{}
			_, err := budget.decode([]byte(tc.data), systemOneMaxJSONDepth)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.name == "object keys count" && budget.nodes != 3 {
				t.Fatalf("nodes=%d", budget.nodes)
			}
		})
	}
	budget := &systemOneJSONBudget{nodes: systemOneMaxJSONNodes - 1}
	if _, err := budget.decode([]byte(`null`), systemOneMaxJSONDepth); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.decode([]byte(`null`), systemOneMaxJSONDepth); err == nil {
		t.Fatal("budget reset between entries")
	}
}

func TestSystemOneNumberNormalizationBoundsExponentBeforeParsing(t *testing.T) {
	for _, sign := range []string{"", "+", "-"} {
		if _, err := normalizeSystemOneNumber(json.Number("1e" + sign + strings.Repeat("9", systemOneMaxExponentDigits))); err != nil {
			t.Fatal(err)
		}
		if _, err := normalizeSystemOneNumber(json.Number("1e" + sign + strings.Repeat("0", 1_000_000))); err == nil {
			t.Fatal("unbounded exponent reached normalization")
		}
	}
}
