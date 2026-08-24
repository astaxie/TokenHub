package guardrails

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type testModelDetector struct {
	result ModelResult
	err    error
	calls  int
	texts  []string
}

func (d *testModelDetector) Detect(_ context.Context, text string) (ModelResult, error) {
	d.calls++
	d.texts = append(d.texts, text)
	return d.result, d.err
}

func TestEngineMergesActionsAndMasksMutableText(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Enterprise protection",
		DetectionItems: []DetectionItem{
			{Name: "Internal term", DetectorType: DetectorPattern, Action: ActionAudit, Config: map[string]any{"keywords": []string{"Aurora"}}},
			{Name: "Customer email", DetectorType: DetectorSensitiveData, Action: ActionMask, Config: map[string]any{"data_types": []string{"email"}}},
		},
		Bindings: []Binding{{ScopeType: ScopeProject, ScopeID: "prj_one"}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		ProjectID: "prj_one", Fragments: []Fragment{{ID: "message.0", Text: "Aurora contact demo@example.com", Mutable: true}}, Policies: []Policy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionMask || decision.Replacements["message.0"] != "Aurora contact [REDACTED]" || len(decision.Findings) != 2 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestEngineKeepsOneFindingPerNonMaskDetectionItem(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Repeated audit",
		DetectionItems: []DetectionItem{{
			Name: "Common term", DetectorType: DetectorPattern, Action: ActionAudit,
			Config: map[string]any{"keywords": []string{"x"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 64*1024)}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Action != ActionAudit || len(decision.Findings) != 1 {
		t.Fatalf("unexpected decision: %#v err=%v", decision, err)
	}
}

func TestEngineMasksEverySensitiveDataMatch(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Mask all emails",
		DetectionItems: []DetectionItem{{
			Name: "Email", DetectorType: DetectorSensitiveData, Action: ActionMask,
			Config: map[string]any{"data_types": []string{"email"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: "a@example.com and b@example.com", Mutable: true}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Replacements["input"] != "[REDACTED] and [REDACTED]" || len(decision.Findings) != 2 {
		t.Fatalf("unexpected decision: %#v err=%v", decision, err)
	}
}

func TestEngineMasksCompletePEMPrivateKey(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Mask private keys",
		DetectionItems: []DetectionItem{{
			Name: "Credentials", DetectorType: DetectorSensitiveData, Action: ActionMask,
			Config: map[string]any{"data_types": []string{"credential"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	privateKey := "-----BEGIN PRIVATE KEY-----\nQUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=\n-----END PRIVATE KEY-----"
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: "before\n" + privateKey + "\nafter", Mutable: true}}, Policies: []Policy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	masked := decision.Replacements["input"]
	if masked != "before\n[REDACTED]\nafter" {
		t.Fatalf("expected the complete PEM block to be masked, got %q", masked)
	}
	if strings.Contains(masked, "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo") || strings.Contains(masked, "END PRIVATE KEY") {
		t.Fatalf("masked output retained private-key material: %q", masked)
	}
}

func TestEngineDetectsSensitiveDataExamples(t *testing.T) {
	tests := []struct {
		name     string
		dataType string
		text     string
		category string
	}{
		{name: "cloud credential", dataType: "credential", text: "token sk-example_12345678901234567890", category: "credential"},
		{name: "email", dataType: "email", text: "电子邮箱：demo@example.com", category: "email"},
		{name: "phone", dataType: "phone", text: "联系电话：13812345678", category: "phone"},
		{name: "validated identity card", dataType: "cn_id_card", text: "身份证号码：11010519491231002X", category: "cn_id_card"},
		{name: "labelled bank card", dataType: "bank_card", text: "银行卡号：6222020200123456789", category: "bank_card"},
		{name: "luhn bank card", dataType: "bank_card", text: "payment 4111 1111 1111 1111", category: "bank_card"},
		{name: "labelled person name", dataType: "person_name", text: "紧急联系人：李雯 联系电话：13987654321", category: "person_name"},
		{name: "labelled address", dataType: "address", text: "联系地址：上海市黄浦区南京东路 123 弄 4 号 602 室", category: "address"},
		{name: "labelled birth date", dataType: "birth_date", text: "出生日期：1992 年 05 月 16 日", category: "birth_date"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := mustNormalizePolicy(t, Policy{
				Name: "Sensitive data examples",
				DetectionItems: []DetectionItem{{
					Name: "Sensitive data", DetectorType: DetectorSensitiveData, Action: ActionMask,
					Config: map[string]any{"data_types": []string{test.dataType}},
				}},
				Bindings: []Binding{{ScopeType: ScopeAllProjects}},
			})
			decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
				Fragments: []Fragment{{ID: "input", Text: test.text, Mutable: true}}, Policies: []Policy{policy},
			})
			if err != nil || len(decision.Findings) != 1 || decision.Findings[0].Category != test.category {
				t.Fatalf("unexpected decision=%#v err=%v", decision, err)
			}
			if !strings.Contains(decision.Replacements["input"], "[REDACTED]") {
				t.Fatalf("expected masked replacement, got %q", decision.Replacements["input"])
			}
			if test.dataType == "birth_date" && decision.Replacements["input"] != "出生日期：[REDACTED]" {
				t.Fatalf("expected complete date masking, got %q", decision.Replacements["input"])
			}
		})
	}
}

func TestSensitiveDataValidationAvoidsCommonNumericFalsePositives(t *testing.T) {
	tests := []struct {
		name     string
		dataType string
		text     string
	}{
		{name: "invalid identity checksum", dataType: "cn_id_card", text: "身份证号码：110105194912310021"},
		{name: "invalid calendar date", dataType: "cn_id_card", text: "身份证号码：11010519990230002X"},
		{name: "invalid labelled birth date", dataType: "birth_date", text: "出生日期：1999 年 02 月 30 日"},
		{name: "unlabelled non-luhn account", dataType: "bank_card", text: "订单号 6222020200123456789"},
		{name: "phone embedded in longer number", dataType: "phone", text: "流水号 9138123456787"},
		{name: "unlabelled chinese name", dataType: "person_name", text: "王浩宇参加了会议"},
		{name: "unlabelled location", dataType: "address", text: "我们在上海市黄浦区见面"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := mustNormalizePolicy(t, Policy{
				Name: "False positive protection",
				DetectionItems: []DetectionItem{{
					Name: "Sensitive data", DetectorType: DetectorSensitiveData, Action: ActionBlock,
					Config: map[string]any{"data_types": []string{test.dataType}},
				}},
				Bindings: []Binding{{ScopeType: ScopeAllProjects}},
			})
			decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
				Fragments: []Fragment{{ID: "input", Text: test.text, Mutable: true}}, Policies: []Policy{policy},
			})
			if err != nil || len(decision.Findings) != 0 || decision.Action != ActionAllow {
				t.Fatalf("unexpected decision=%#v err=%v", decision, err)
			}
		})
	}
}

func TestEngineShortCircuitsBeforeModel(t *testing.T) {
	detector := &testModelDetector{result: ModelResult{Safety: "unsafe"}}
	policy := mustNormalizePolicy(t, Policy{
		Name: "Block first",
		DetectionItems: []DetectionItem{
			{Name: "Credential", DetectorType: DetectorSensitiveData, Action: ActionBlock, Config: map[string]any{"data_types": []string{"credential"}}},
			{Name: "Model", DetectorType: DetectorModel, Action: ActionBlock, Config: map[string]any{}},
		},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(detector).Evaluate(context.Background(), EvaluationRequest{
		ProjectID: "prj_one", Fragments: []Fragment{{ID: "message.0", Text: "AKIA0000000000000000", Mutable: true}}, Policies: []Policy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionBlock || !decision.ShortCircuited || detector.calls != 0 {
		t.Fatalf("unexpected decision=%#v model calls=%d", decision, detector.calls)
	}
}

func TestEngineMasksSensitiveDataBeforeModelDetection(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Mask before model",
		DetectionItems: []DetectionItem{
			{Name: "Email", DetectorType: DetectorSensitiveData, Action: ActionMask, Config: map[string]any{"data_types": []string{"email"}}},
			{Name: "Model", DetectorType: DetectorModel, Action: ActionAudit, Config: map[string]any{}},
		},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})

	for _, test := range []struct {
		name              string
		mutable           bool
		expectReplacement bool
	}{
		{name: "mutable provider fragment", mutable: true, expectReplacement: true},
		{name: "immutable detector-only fragment", mutable: false, expectReplacement: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			detector := &testModelDetector{result: ModelResult{Safety: "safe"}}
			decision, err := NewEngine(detector).Evaluate(context.Background(), EvaluationRequest{
				Fragments: []Fragment{
					{ID: "input", Text: "contact demo@example.com", Mutable: test.mutable},
					{ID: "context", Text: "copy ops@example.com", Mutable: false},
				},
				Policies: []Policy{policy},
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != ActionMask || detector.calls != 1 || len(detector.texts) != 1 {
				t.Fatalf("unexpected decision=%#v calls=%d texts=%#v", decision, detector.calls, detector.texts)
			}
			if detector.texts[0] != "contact [REDACTED]\ncopy [REDACTED]" {
				t.Fatalf("model detector received unsanitized text %q", detector.texts[0])
			}
			_, hasReplacement := decision.Replacements["input"]
			if hasReplacement != test.expectReplacement {
				t.Fatalf("replacement presence=%v, want %v: %#v", hasReplacement, test.expectReplacement, decision.Replacements)
			}
			if _, hasReplacement := decision.Replacements["context"]; hasReplacement {
				t.Fatalf("immutable fragment received a provider replacement: %#v", decision.Replacements)
			}
		})
	}
}

func TestEnginePreservesUnmatchedTextForModelDetection(t *testing.T) {
	detector := &testModelDetector{result: ModelResult{Safety: "safe"}}
	policy := mustNormalizePolicy(t, Policy{
		Name: "No local match",
		DetectionItems: []DetectionItem{
			{Name: "Email", DetectorType: DetectorSensitiveData, Action: ActionMask, Config: map[string]any{"data_types": []string{"email"}}},
			{Name: "Model", DetectorType: DetectorModel, Action: ActionAudit, Config: map[string]any{}},
		},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(detector).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: "ordinary request", Mutable: true}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Action != ActionAllow || len(detector.texts) != 1 || detector.texts[0] != "ordinary request" {
		t.Fatalf("unexpected decision=%#v texts=%#v err=%v", decision, detector.texts, err)
	}
}

func TestEngineHonorsModelResultAndUnavailablePolicy(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Model policy",
		DetectionItems: []DetectionItem{{
			Name: "Model", DetectorType: DetectorModel, Action: ActionBlock,
			Config: map[string]any{"block_on": "controversial_or_unsafe", "on_unavailable": "block"},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})

	t.Run("classification", func(t *testing.T) {
		detector := &testModelDetector{result: ModelResult{Safety: "controversial", Categories: []string{"policy"}}}
		decision, err := NewEngine(detector).Evaluate(context.Background(), EvaluationRequest{Fragments: []Fragment{{ID: "input", Text: "review me"}}, Policies: []Policy{policy}})
		if err != nil || decision.Action != ActionBlock || detector.calls != 1 {
			t.Fatalf("decision=%#v calls=%d err=%v", decision, detector.calls, err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		detector := &testModelDetector{err: errors.New("offline")}
		decision, err := NewEngine(detector).Evaluate(context.Background(), EvaluationRequest{Fragments: []Fragment{{ID: "input", Text: "review me"}}, Policies: []Policy{policy}})
		if err != nil || decision.Action != ActionBlock || !decision.DetectionDegraded || decision.Findings[0].ReasonCode != "guardrail_model_unavailable" {
			t.Fatalf("unexpected decision=%#v err=%v", decision, err)
		}
	})
}

func TestEngineInspectsLargeInput(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Large input", DetectionItems: []DetectionItem{{Name: "Keyword", DetectorType: DetectorPattern, Action: ActionAudit, Config: map[string]any{"keywords": []string{"needle"}}}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 1024*1024) + "needle"}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Action != ActionAudit || len(decision.Findings) != 1 {
		t.Fatalf("unexpected large-input decision=%#v err=%v", decision, err)
	}
}

func TestEngineInspectsLongContextWithDozensOfSimpleRules(t *testing.T) {
	items := make([]DetectionItem, 32)
	for index := range items {
		items[index] = DetectionItem{
			Name: "Keyword", DetectorType: DetectorPattern, Action: ActionAudit,
			Config: map[string]any{"keywords": []string{"needle"}},
		}
	}
	policy := mustNormalizePolicy(t, Policy{
		Name: "Long context", DetectionItems: items,
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 4*1024*1024)}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Action != ActionAllow || len(decision.Findings) != 0 {
		t.Fatalf("unexpected long-context decision=%#v err=%v", decision, err)
	}
}

func TestEngineRejectsPathologicalAggregateRegexWorkBeforeScanning(t *testing.T) {
	expressions := make([]string, 64)
	for index := range expressions {
		expressions[index] = fmt.Sprintf("a{%d,512}b", index+1)
	}
	items := make([]DetectionItem, 32)
	for index := range items {
		items[index] = DetectionItem{
			Name: "Expensive regex", DetectorType: DetectorPattern, Action: ActionAudit,
			Config: map[string]any{"regex": expressions},
		}
	}
	policy := mustNormalizePolicy(t, Policy{
		Name: "Pathological workload", DetectionItems: items,
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("a", 64*1024)}}, Policies: []Policy{policy},
	})
	if !errors.Is(err, ErrDeterministicWorkBudgetExceeded) || decision.Action != ActionAllow || len(decision.Findings) != 0 {
		t.Fatalf("expected deterministic budget rejection, decision=%#v err=%v", decision, err)
	}
}

func TestEngineRejectsLongCaseInsensitiveKeywordWorkBeforeScanning(t *testing.T) {
	keywords := make([]string, 64)
	for index := range keywords {
		keywords[index] = strings.Repeat("a", 510) + fmt.Sprintf("%02d", index)
	}
	policy := mustNormalizePolicy(t, Policy{
		Name: "Long keywords", DetectionItems: []DetectionItem{{
			Name: "Expensive keywords", DetectorType: DetectorPattern, Action: ActionAudit,
			Config: map[string]any{"keywords": keywords},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 2*1024*1024)}}, Policies: []Policy{policy},
	})
	if !errors.Is(err, ErrDeterministicWorkBudgetExceeded) || decision.Action != ActionAllow || len(decision.Findings) != 0 {
		t.Fatalf("expected keyword budget rejection, decision=%#v err=%v", decision, err)
	}
}

func TestEngineRejectsDenseMaskMatchesBeforeMaterializingUnboundedFindings(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Dense emails", DetectionItems: []DetectionItem{{
			Name: "Email", DetectorType: DetectorSensitiveData, Action: ActionMask,
			Config: map[string]any{"data_types": []string{"email"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("a@b.co ", maxDeterministicFindings+1), Mutable: true}}, Policies: []Policy{policy},
	})
	if !errors.Is(err, ErrDeterministicWorkBudgetExceeded) || decision.Action != ActionAllow || len(decision.Findings) != 0 || len(decision.Replacements) != 0 {
		t.Fatalf("expected finding budget rejection, decision=%#v err=%v", decision, err)
	}
}

func TestEngineStopsBeforeDeterministicScanningWhenContextIsCanceled(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Canceled", DetectionItems: []DetectionItem{{
			Name: "Keyword", DetectorType: DetectorPattern, Action: ActionAudit,
			Config: map[string]any{"keywords": []string{"needle"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewEngine(nil).Evaluate(ctx, EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 1024*1024)}}, Policies: []Policy{policy},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestEngineFindsValidSensitiveValueAfterInvalidCandidate(t *testing.T) {
	policy := mustNormalizePolicy(t, Policy{
		Name: "Birth dates", DetectionItems: []DetectionItem{{
			Name: "Birth date", DetectorType: DetectorSensitiveData, Action: ActionBlock,
			Config: map[string]any{"data_types": []string{"birth_date"}},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
		Fragments: []Fragment{{ID: "input", Text: "出生日期：1999 年 02 月 30 日；出生日期：1992 年 05 月 16 日"}}, Policies: []Policy{policy},
	})
	if err != nil || decision.Action != ActionBlock || len(decision.Findings) != 1 {
		t.Fatalf("expected the later valid date to match, decision=%#v err=%v", decision, err)
	}
}

func TestEngineAllowsLargeInputWithoutApplicablePolicies(t *testing.T) {
	otherProjectPolicy := mustNormalizePolicy(t, Policy{
		Name: "Other project", DetectionItems: []DetectionItem{{Name: "Keyword", DetectorType: DetectorPattern, Action: ActionAudit, Config: map[string]any{"keywords": []string{"x"}}}},
		Bindings: []Binding{{ScopeType: ScopeProject, ScopeID: "prj_other"}},
	})
	for _, test := range []struct {
		name     string
		policies []Policy
	}{
		{name: "no policies"},
		{name: "policy bound to another project", policies: []Policy{otherProjectPolicy}},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := NewEngine(nil).Evaluate(context.Background(), EvaluationRequest{
				ProjectID: "prj_one", Fragments: []Fragment{{ID: "input", Text: strings.Repeat("x", 1024*1024)}}, Policies: test.policies,
			})
			if err != nil || decision.Action != ActionAllow || len(decision.Findings) != 0 {
				t.Fatalf("unexpected large-input decision=%#v err=%v", decision, err)
			}
		})
	}
}

func TestMaskedFragmentsMergesOverlappingFindings(t *testing.T) {
	fragments := []Fragment{{ID: "input", Text: "0123456789", Mutable: true}}
	findings := []Finding{
		{Action: ActionMask, FragmentID: "input", Start: 0, End: 5},
		{Action: ActionMask, FragmentID: "input", Start: 3, End: 10},
	}

	masked := maskedFragments(fragments, findings)
	if got := masked["input"]; got != "[REDACTED]" {
		t.Fatalf("expected overlapping findings to be fully masked, got %q", got)
	}
}

func TestMaskedFragmentsPrefersLongestFindingAtSameStart(t *testing.T) {
	fragments := []Fragment{{ID: "input", Text: "0123456789", Mutable: true}}
	findings := []Finding{
		{Action: ActionMask, FragmentID: "input", Start: 0, End: 5},
		{Action: ActionMask, FragmentID: "input", Start: 0, End: 10},
	}

	masked := maskedFragments(fragments, findings)
	if got := masked["input"]; got != "[REDACTED]" {
		t.Fatalf("expected the longest same-start finding to be fully masked, got %q", got)
	}
}

func mustNormalizePolicy(t *testing.T, policy Policy) Policy {
	t.Helper()
	normalized, err := NormalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}
