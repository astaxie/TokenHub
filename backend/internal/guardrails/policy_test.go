package guardrails

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizePolicyAppliesMinimalDefaults(t *testing.T) {
	policy, err := NormalizePolicy(Policy{
		Name: "  Outbound protection  ",
		DetectionItems: []DetectionItem{{
			Name:         " Internal names ",
			DetectorType: "PATTERN",
			Action:       "BLOCK",
			Config: map[string]any{
				"keywords": []any{" Aurora ", "Aurora", ""},
			},
		}, {
			Name:         "Qwen guard",
			DetectorType: DetectorModel,
			Action:       ActionAudit,
			Config:       map[string]any{},
		}},
		Bindings: []Binding{{ScopeType: ScopeAllProjects}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Name != "Outbound protection" || policy.Status != StatusActive || policy.ConfigVersion != CurrentConfigVersion {
		t.Fatalf("unexpected policy normalization: %#v", policy)
	}
	if got := policy.DetectionItems[0].Config["keywords"]; !reflect.DeepEqual(got, []any{"Aurora"}) {
		t.Fatalf("unexpected normalized keywords: %#v", got)
	}
	modelConfig := policy.DetectionItems[1].Config
	if modelConfig["block_on"] != "unsafe" || modelConfig["on_unavailable"] != "allow_and_audit" {
		t.Fatalf("unexpected model defaults: %#v", modelConfig)
	}
	if policy.Bindings[0].Checkpoint != CheckpointBeforeProvider || policy.Bindings[0].Protocol != ProtocolAll {
		t.Fatalf("unexpected binding defaults: %#v", policy.Bindings[0])
	}
}

func TestNormalizePolicyRejectsUnsupportedConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		code   string
	}{
		{
			name: "too many detection items",
			policy: func() Policy {
				policy := validPolicyWithItem(DetectionItem{
					Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
					Config: map[string]any{"keywords": []string{"Aurora"}},
				})
				policy.DetectionItems = make([]DetectionItem, maxDetectionItems+1)
				for index := range policy.DetectionItems {
					policy.DetectionItems[index] = DetectionItem{
						Name: fmt.Sprintf("Pattern %d", index), DetectorType: DetectorPattern, Action: ActionBlock,
						Config: map[string]any{"keywords": []string{"Aurora"}},
					}
				}
				return policy
			}(),
			code: "guardrail_detection_items_limit_exceeded",
		},
		{
			name: "too many patterns",
			policy: func() Policy {
				keywords := make([]string, maxPatternExpressions+1)
				for index := range keywords {
					keywords[index] = fmt.Sprintf("keyword-%d", index)
				}
				return validPolicyWithItem(DetectionItem{
					Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
					Config: map[string]any{"keywords": keywords},
				})
			}(),
			code: "guardrail_pattern_limit_exceeded",
		},
		{
			name: "pattern too long",
			policy: validPolicyWithItem(DetectionItem{
				Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
				Config: map[string]any{"keywords": []string{strings.Repeat("x", maxPatternValueBytes+1)}},
			}),
			code: "guardrail_pattern_limit_exceeded",
		},
		{
			name: "unknown detector",
			policy: validPolicyWithItem(DetectionItem{
				Name: "Unknown", DetectorType: "custom", Action: ActionAudit, Config: map[string]any{},
			}),
			code: "unknown_guardrail_detector_type",
		},
		{
			name: "model mask",
			policy: validPolicyWithItem(DetectionItem{
				Name: "Model", DetectorType: DetectorModel, Action: ActionMask, Config: map[string]any{},
			}),
			code: "invalid_guardrail_action",
		},
		{
			name: "unknown config field",
			policy: validPolicyWithItem(DetectionItem{
				Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
				Config: map[string]any{"keywords": []string{"Aurora"}, "script": "run"},
			}),
			code: "invalid_guardrail_detector_config",
		},
		{
			name: "mixed all projects binding",
			policy: func() Policy {
				policy := validPolicyWithItem(DetectionItem{
					Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
					Config: map[string]any{"keywords": []string{"Aurora"}},
				})
				policy.Bindings = append(policy.Bindings, Binding{ScopeType: ScopeProject, ScopeID: "prj_1"})
				return policy
			}(),
			code: "guardrail_binding_conflict",
		},
		{
			name: "unsupported version",
			policy: func() Policy {
				policy := validPolicyWithItem(DetectionItem{
					Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
					Config: map[string]any{"keywords": []string{"Aurora"}},
				})
				policy.ConfigVersion = 2
				return policy
			}(),
			code: "unsupported_guardrail_config_version",
		},
		{
			name: "unsupported checkpoint",
			policy: func() Policy {
				policy := validPolicyWithItem(DetectionItem{
					Name: "Pattern", DetectorType: DetectorPattern, Action: ActionBlock,
					Config: map[string]any{"keywords": []string{"Aurora"}},
				})
				policy.Bindings[0].Checkpoint = "after_provider"
				return policy
			}(),
			code: "unsupported_guardrail_checkpoint",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizePolicy(test.policy)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || validationErr.Code != test.code {
				t.Fatalf("expected %s, got %#v", test.code, err)
			}
		})
	}
}

func TestNormalizePolicyAcceptsExpandedSensitiveDataTypes(t *testing.T) {
	policy, err := NormalizePolicy(validPolicyWithItem(DetectionItem{
		Name: "Sensitive data", DetectorType: DetectorSensitiveData, Action: ActionMask,
		Config: map[string]any{"data_types": []string{"bank_card", "person_name", "address", "birth_date"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"address", "bank_card", "birth_date", "person_name"}
	if got := policy.DetectionItems[0].Config["data_types"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected normalized data types: %#v", got)
	}
}

func validPolicyWithItem(item DetectionItem) Policy {
	return Policy{
		Name:           "Outbound protection",
		DetectionItems: []DetectionItem{item},
		Bindings:       []Binding{{ScopeType: ScopeAllProjects}},
	}
}
