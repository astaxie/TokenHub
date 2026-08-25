package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGatewayHookRunnerExecutesRegisteredHooksInPlanOrder(t *testing.T) {
	chain := NewGatewayChainRegistry()
	first := GatewayHookDescriptor{PluginID: "tokenhub.a", HookID: "first", Stage: StagePrivacyPre, Priority: 2000, Writes: []GatewayDataClass{DataRequestBody}}
	second := GatewayHookDescriptor{PluginID: "tokenhub.b", HookID: "second", Stage: StagePrivacyPre, Priority: 2100}
	for _, hook := range []GatewayHookDescriptor{second, first} {
		if err := chain.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	runner := NewGatewayHookRunner(chain)
	var calls []string
	if err := runner.RegisterHandler(first, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		calls = append(calls, "first")
		return GatewayHookResult{
			Decision: HookDecisionContinue,
			Writes: map[GatewayDataClass]RawPatch{
				DataRequestBody: {Value: json.RawMessage(`{"masked":true}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register first handler: %v", err)
	}
	if err := runner.RegisterHandler(second, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		calls = append(calls, "second")
		return GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register second handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StagePrivacyPre, GatewayHookInput{RequestID: "req_1"})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if strings.Join(calls, ",") != "first,second" {
		t.Fatalf("calls = %v, want first,second", calls)
	}
	if len(report.Results) != 2 || report.Results[0].Status != HookRunSucceeded || report.Results[1].Status != HookRunSucceeded {
		t.Fatalf("report = %+v", report)
	}
}

func TestGatewayHookRunnerRejectsUndeclaredWrites(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{PluginID: "tokenhub.privacy", HookID: "mask", Stage: StagePrivacyPre, FailurePolicy: FailurePolicyFailClosed}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{
			Decision: HookDecisionContinue,
			Writes: map[GatewayDataClass]RawPatch{
				DataRequestBody: {Value: json.RawMessage(`{}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StagePrivacyPre, GatewayHookInput{})
	if err == nil {
		t.Fatal("runner accepted undeclared hook writes")
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunFailed {
		t.Fatalf("report = %+v", report)
	}
}

func TestGatewayHookRunnerAppliesFailurePolicyForMissingHandlers(t *testing.T) {
	chain := NewGatewayChainRegistry()
	openHook := GatewayHookDescriptor{PluginID: "tokenhub.cache", HookID: "lookup", Stage: StageCacheLookup, FailurePolicy: FailurePolicyFailOpen}
	closedHook := GatewayHookDescriptor{PluginID: "tokenhub.guardrail", HookID: "pre", Stage: StageGuardrailPre, FailurePolicy: FailurePolicyFailClosed}
	for _, hook := range []GatewayHookDescriptor{openHook, closedHook} {
		if err := chain.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	runner := NewGatewayHookRunner(chain)

	openReport, err := runner.RunStage(context.Background(), StageCacheLookup, GatewayHookInput{})
	if err != nil {
		t.Fatalf("fail-open missing handler returned error: %v", err)
	}
	if len(openReport.Results) != 1 || openReport.Results[0].Status != HookRunSkipped {
		t.Fatalf("fail-open report = %+v", openReport)
	}
	closedReport, err := runner.RunStage(context.Background(), StageGuardrailPre, GatewayHookInput{})
	if err == nil {
		t.Fatal("fail-closed missing handler did not return an error")
	}
	if len(closedReport.Results) != 1 || closedReport.Results[0].Status != HookRunFailed {
		t.Fatalf("fail-closed report = %+v", closedReport)
	}
}

func TestGatewayHookRunnerEnforcesTimeout(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{PluginID: "tokenhub.slow", HookID: "slow", Stage: StageContextOptimize, TimeoutMillis: 1, FailurePolicy: FailurePolicyFailClosed}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(ctx context.Context, _ GatewayHookInput) (GatewayHookResult, error) {
		<-ctx.Done()
		return GatewayHookResult{}, ctx.Err()
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	_, err := runner.RunStage(context.Background(), StageContextOptimize, GatewayHookInput{})
	if !IsGatewayHookTimeout(err) {
		t.Fatalf("error = %v, want gateway hook timeout", err)
	}
}

func TestGatewayHookRunnerClipsInputToDeclaredReads(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{
		PluginID: "tokenhub.privacy",
		HookID:   "mask",
		Stage:    StagePrivacyPre,
		Reads:    []GatewayDataClass{DataNormalizedText},
	}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	var seen GatewayHookInput
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(_ context.Context, input GatewayHookInput) (GatewayHookResult, error) {
		seen = input
		return GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	_, err := runner.RunStage(context.Background(), StagePrivacyPre, GatewayHookInput{
		RequestID: "req_clip",
		Envelope: GatewayEnvelope{
			Version:        "v1",
			Protocol:       "openai",
			Operation:      "responses",
			Model:          "gpt-test",
			RequestBody:    json.RawMessage(`{"secret":"hidden"}`),
			NormalizedText: []TextSegment{{ID: "input", Text: "visible"}},
			Metadata: map[string]json.RawMessage{
				"unsafe": json.RawMessage(`"hidden"`),
			},
		},
		Data: GatewayHookData{
			DataRequestBody:    json.RawMessage(`{"secret":"hidden"}`),
			DataNormalizedText: json.RawMessage(`["visible"]`),
		},
	})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if len(seen.Envelope.RequestBody) != 0 {
		t.Fatalf("request body leaked into clipped input: %s", seen.Envelope.RequestBody)
	}
	if len(seen.Envelope.Metadata) != 0 {
		t.Fatalf("metadata leaked into clipped input: %v", seen.Envelope.Metadata)
	}
	if len(seen.Envelope.NormalizedText) != 1 || seen.Envelope.NormalizedText[0].Text != "visible" {
		t.Fatalf("normalized text = %+v, want visible text", seen.Envelope.NormalizedText)
	}
	if _, ok := seen.Data[DataRequestBody]; ok {
		t.Fatalf("request body data class leaked into clipped input: %+v", seen.Data)
	}
	if string(seen.Data[DataNormalizedText]) != `["visible"]` {
		t.Fatalf("normalized data = %s, want visible data", seen.Data[DataNormalizedText])
	}
}

func TestGatewayHookRunnerStopsOnTerminalDecision(t *testing.T) {
	chain := NewGatewayChainRegistry()
	first := GatewayHookDescriptor{PluginID: "tokenhub.policy", HookID: "deny", Stage: StageAdmission, Priority: 100, FailurePolicy: FailurePolicyFailClosed}
	second := GatewayHookDescriptor{PluginID: "tokenhub.policy", HookID: "after", Stage: StageAdmission, Priority: 200, FailurePolicy: FailurePolicyFailClosed}
	for _, hook := range []GatewayHookDescriptor{first, second} {
		if err := chain.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(first, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{Decision: HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register first handler: %v", err)
	}
	if err := runner.RegisterHandler(second, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		t.Fatal("second hook should not run after a deny decision")
		return GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register second handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StageAdmission, GatewayHookInput{})
	if !IsGatewayHookDenied(err) {
		t.Fatalf("error = %v, want gateway hook denied", err)
	}
	if report.TerminalDecision != HookDecisionDeny || len(report.Results) != 1 {
		t.Fatalf("report = %+v", report)
	}
}
