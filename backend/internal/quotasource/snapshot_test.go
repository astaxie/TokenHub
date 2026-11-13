package quotasource

import (
	"math"
	"testing"
	"time"

	"tokenhub/backend/internal/allowance"
)

func TestQuotaTimeRejectsNonFiniteTimestamps(t *testing.T) {
	for _, value := range []any{"NaN", "Inf", "-Inf", math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := Time(value); got != nil {
			t.Fatalf("non-finite timestamp %v produced %v", value, got)
		}
	}
}

func TestFusecodePartialReplyPreservesUnknownWindows(t *testing.T) {
	c, err := Resolve(Config{Preset: "fusecode"}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result, err := ParsePlan([]byte(`{"remaining":21.25,"subscription":{"weekly_usage_usd":20,"weekly_limit_usd":100,"monthly_usage_usd":35,"monthly_limit_usd":200}}`), c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Allowance.Windows) != 3 || result.Allowance.Windows[0].Balance == nil || *result.Allowance.Windows[0].Balance != 21.25 || result.Allowance.Windows[0].Unit != "USD" || result.Allowance.For("claude-sonnet", now, 0).Status != allowance.Available {
		t.Fatalf("Fusecode normalization: %+v", result)
	}
	result, err = ParsePlan([]byte(`{"remaining":21.25}`), c, now)
	if err != nil || result.Allowance.For("claude-sonnet", now, 0).Status != allowance.Unknown {
		t.Fatalf("missing subscription inferred spare quota: %+v %v", result, err)
	}
}

func TestPlanReadersKeepMissingNumericValuesUnknown(t *testing.T) {
	now := time.Now().UTC()
	for _, p := range []struct{ id, body string }{
		{"glm", `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":6,"number":1}]}}`},
		{"minimax", `{"base_resp":{"status_code":0},"model_remains":[]}`},
		{"opencode-go", `{"usage":{"weekly":{}}}`},
	} {
		result, err := ParsePlan([]byte(p.body), Config{Preset: p.id}, now)
		if err == nil && result.Allowance.For("model", now, 0).Status != allowance.Unknown {
			t.Fatalf("%s invented usage: %+v", p.id, result)
		}
	}
}

func TestMistralSelectsOnlyThisKeyBudget(t *testing.T) {
	body := []byte(`{"vibe_budget":{"usage_percentage":99,"initial_budget":100,"currency":"EUR"},"api_budget":{"usage_percentage":5,"initial_budget":20,"currency":"EUR"}}`)
	result, err := ParseMistral(body, "api", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if *result.Allowance.Windows[0].UsedPercent != 5 {
		t.Fatal("API key read Vibe budget")
	}
	if _, err := ParseMistral(body, "", time.Now().UTC()); err == nil {
		t.Fatal("unknown key kind selected a budget")
	}
}
