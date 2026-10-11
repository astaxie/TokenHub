package guardrails

import (
	"context"
	"strings"
	"testing"
)

// The labelled birth-date pattern explicitly accepts one- or two-digit month
// and day components (0?[1-9]), so every real calendar date it matches must be
// validated and masked. A validator that only understands the zero-padded
// 8-digit form silently drops those dates.
func TestEngineDetectsBirthDatesWithSingleDigitParts(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Birth dates",
		DetectionItems: []DetectionItem{{
			Name: "Birth date", DetectorType: DetectorSensitiveData, Action: ActionMask,
			Config: map[string]any{"data_types": []string{"birth_date"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "single digit month and day", text: "出生日期：1990年1月5日", want: "出生日期：[REDACTED]"},
		{name: "padded day with single digit month", text: "出生日期：1990年1月05日", want: "出生日期：[REDACTED]"},
		{name: "dash separators", text: "生日：2001-3-9", want: "生日：[REDACTED]"},
		{name: "slash separators", text: "出生年月: 1988/12/3", want: "出生年月: [REDACTED]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
				Fragments: []Fragment{{ID: "input", Text: test.text, Mutable: true}}, Policies: []Policy{policy},
			})
			if err != nil || len(decision.Findings) != 1 || decision.Findings[0].Category != "birth_date" {
				t.Fatalf("expected one birth_date finding, decision=%#v err=%v", decision, err)
			}
			if got := decision.Replacements["input"]; got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
			if strings.Contains(decision.Replacements["input"], "1990") {
				t.Fatalf("masked output retained the birth date: %q", decision.Replacements["input"])
			}
		})
	}
}

// Impossible calendar dates must still be rejected even when the components are
// written with one digit, so the fix must not simply accept every matched shape.
func TestEngineRejectsImpossibleBirthDatesWithSingleDigitParts(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Birth dates",
		DetectionItems: []DetectionItem{{
			Name: "Birth date", DetectorType: DetectorSensitiveData, Action: ActionMask,
			Config: map[string]any{"data_types": []string{"birth_date"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	for _, text := range []string{"出生日期：1990年2月30日", "出生日期：1990年4月31日", "生日：1990-2-30"} {
		t.Run(text, func(t *testing.T) {
			decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
				Fragments: []Fragment{{ID: "input", Text: text, Mutable: true}}, Policies: []Policy{policy},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(decision.Findings) != 0 || decision.Action != ActionAllow {
				t.Fatalf("expected an impossible date to stay unmatched, decision=%#v", decision)
			}
		})
	}
}
