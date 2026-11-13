package quotasource

import (
	"strings"
	"testing"
)

func TestFusecodeExpressionPreservesBalanceAndPlanShares(t *testing.T) {
	rule := "$remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; Month: subscription.monthly_usage_usd / subscription.monthly_limit_usd %"
	parts, err := ReadParts([]byte(`{"remaining":25,"subscription":{"weekly_usage_usd":12,"weekly_limit_usd":20,"monthly_usage_usd":40,"monthly_limit_usd":200}}`), rule)
	if err != nil || len(parts) != 3 || parts[0].Text != "$25.00" || parts[1].Percent == nil || *parts[1].Percent != 60 || parts[2].Percent == nil || *parts[2].Percent != 20 {
		t.Fatalf("incomplete Fusecode report: %+v, %v", parts, err)
	}
	parts, err = ReadParts([]byte(`{"remaining":25}`), rule)
	if err != nil || len(parts) != 3 || parts[0].Text != "$25.00" || parts[1].Error == "" || parts[1].Percent != nil || parts[2].Percent != nil {
		t.Fatalf("missing subscription fields invented allowance: %+v, %v", parts, err)
	}
	if _, err := ReadParts([]byte(`{}`), rule); err == nil {
		t.Fatal("an entirely missing report succeeded")
	}
}

func TestMagpieCollectionAndArithmeticSemantics(t *testing.T) {
	body := []byte(`{"items":[{"state":"active","used":3,"limit":10},{"state":"active","used":5,"limit":10},{"state":"off","used":100,"limit":100}],"zero":0}`)
	for _, tc := range []struct {
		rule string
		want float64
	}{{"sum(items[state=active].used)", 8}, {"max(items[state!=off].used / items[state!=off].limit)", .5}, {"count(items.*.state)", 3}, {"(10 - min(items.*.used)) * 2", 14}} {
		n, err := ReadNumber(body, tc.rule)
		if err != nil || n == nil || *n != tc.want {
			t.Fatalf("%s = %v, %v; want %v", tc.rule, n, err, tc.want)
		}
	}
	if _, err := ReadNumber(body, "1 / zero"); err == nil {
		t.Fatal("division by zero succeeded")
	}
	if _, err := ReadNumber(body, strings.Repeat("(", 65)+"1"+strings.Repeat(")", 65)); err == nil {
		t.Fatal("unbounded expression nesting accepted")
	}
}
