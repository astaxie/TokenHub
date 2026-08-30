package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGatewayHookRunnerExecutesRegisteredHooksInPlanOrder(t *testing.T) {
	chain := NewGatewayChainRegistry()
	first := GatewayHookDescriptor{PluginID: "tokenhub.a", HookID: "first", Stage: StagePrivacyPre, Priority: 2000, Writes: []GatewayDataClass{DataRequestBody}}
	second := GatewayHookDescriptor{PluginID: "tokenhub.b", HookID: "second", Stage: StagePrivacyPre, Priority: 2100, Reads: []GatewayDataClass{DataRequestBody}}
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
	if err := runner.RegisterHandler(second, GatewayHookHandlerFunc(func(_ context.Context, input GatewayHookInput) (GatewayHookResult, error) {
		calls = append(calls, "second")
		if string(input.Envelope.RequestBody) != `{"masked":true}` {
			t.Fatalf("second hook request body = %s, want first hook patch", input.Envelope.RequestBody)
		}
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

func TestGatewayHookRunnerEnforcesCommandHandlerPermissionGrants(t *testing.T) {
	hook := GatewayHookDescriptor{
		PluginID:      "tokenhub.external-privacy",
		HookID:        "mask",
		Stage:         StagePrivacyPre,
		Reads:         []GatewayDataClass{DataRequestBody},
		FailurePolicy: FailurePolicyFailClosed,
	}
	runner := NewGatewayHookRunner(NewGatewayChainRegistry())
	if err := runner.RegisterHandler(hook, NewGatewayCommandRunner(t.TempDir(), "missing.sh", PermissionGrant{Enforced: true})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStageHooks(context.Background(), StagePrivacyPre, GatewayHookInput{
		Envelope: GatewayEnvelope{RequestBody: json.RawMessage(`{"prompt":"secret"}`)},
	}, []GatewayHookDescriptor{hook})
	if err == nil {
		t.Fatal("gateway command handler ran without its required permission grant")
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorPermissionRequired {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorPermissionRequired, err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunFailed {
		t.Fatalf("report = %+v, want failed permission result", report)
	}
}

func TestGatewayHookRunnerRejectsStageMutationLimitViolations(t *testing.T) {
	hook := GatewayHookDescriptor{
		PluginID:      "tokenhub.trace",
		HookID:        "leak",
		Stage:         StageTraceExport,
		Writes:        []GatewayDataClass{DataAudit},
		FailurePolicy: FailurePolicyFailClosed,
	}
	runner := NewGatewayHookRunner(NewGatewayChainRegistry())
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{
			Writes: map[GatewayDataClass]RawPatch{
				DataAudit: {Value: json.RawMessage(`{"mutated":true}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStageHooks(context.Background(), StageTraceExport, GatewayHookInput{}, []GatewayHookDescriptor{hook})
	if err == nil {
		t.Fatal("runner accepted a trace_export write that violates the stage mutation limit")
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunFailed {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(err.Error(), `stage "trace_export" cannot write data class "audit"`) {
		t.Fatalf("error = %v, want stage mutation limit violation", err)
	}
}

func TestGatewayHookRunnerReportsAuditEvents(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{
		PluginID:      "tokenhub.audit",
		HookID:        "decision",
		Stage:         StageAdmission,
		FailurePolicy: FailurePolicyFailClosed,
	}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	auditEvent := json.RawMessage(`{"reason":"policy_match","category":"quota"}`)
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{
			Decision:    HookDecisionContinue,
			AuditEvents: []json.RawMessage{auditEvent},
		}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StageAdmission, GatewayHookInput{RequestID: "req_audit"})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if len(report.Results) != 1 || len(report.Results[0].AuditEvents) != 1 {
		t.Fatalf("audit events = %+v, want one propagated audit event", report.Results)
	}
	auditEvent[0] = '['
	if string(report.Results[0].AuditEvents[0]) != `{"reason":"policy_match","category":"quota"}` {
		t.Fatalf("audit event was not cloned: %s", report.Results[0].AuditEvents[0])
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if strings.Contains(string(encoded), "policy_match") || strings.Contains(string(encoded), "audit_events") {
		t.Fatalf("audit events leaked through report JSON: %s", encoded)
	}
}

func TestGatewayHookRunnerBoundsAuditEvents(t *testing.T) {
	hook := GatewayHookDescriptor{
		PluginID:      "tokenhub.audit",
		HookID:        "bounded",
		Stage:         StageAdmission,
		FailurePolicy: FailurePolicyFailClosed,
	}
	runner := NewGatewayHookRunner(NewGatewayChainRegistry())
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		events := make([]json.RawMessage, 0, MaxGatewayHookAuditEventsPerRun+1)
		events = append(events, json.RawMessage(`"`+strings.Repeat("x", MaxGatewayHookAuditEventBytes+1)+`"`))
		for index := 0; index < MaxGatewayHookAuditEventsPerRun; index++ {
			events = append(events, json.RawMessage(`{"event":"allowed"}`))
		}
		return GatewayHookResult{
			Decision:    HookDecisionContinue,
			AuditEvents: events,
		}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStageHooks(context.Background(), StageAdmission, GatewayHookInput{}, []GatewayHookDescriptor{hook})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if len(report.Results) != 1 || len(report.Results[0].AuditEvents) != MaxGatewayHookAuditEventsPerRun {
		t.Fatalf("audit events = %+v, want capped at %d", report.Results, MaxGatewayHookAuditEventsPerRun)
	}
	var marker map[string]any
	if err := json.Unmarshal(report.Results[0].AuditEvents[0], &marker); err != nil {
		t.Fatalf("decode oversized marker: %v", err)
	}
	if marker["truncated"] != true || marker["limit_bytes"] != float64(MaxGatewayHookAuditEventBytes) {
		t.Fatalf("oversized marker = %+v, want truncation marker", marker)
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

func TestGatewayHookRunnerReportsSkipRouteFailurePolicy(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{PluginID: "tokenhub.transform", HookID: "shape", Stage: StageRequestTransform, FailurePolicy: FailurePolicySkipRoute}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{}, errors.New("provider-specific transform failed")
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StageRequestTransform, GatewayHookInput{})
	if !IsGatewayHookRouteSkipped(err) {
		t.Fatalf("error = %v, want gateway hook route skipped", err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunFailed {
		t.Fatalf("report = %+v", report)
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

func TestGatewayHookRunnerAppliesFailurePolicyForTimeouts(t *testing.T) {
	tests := []struct {
		name       string
		hook       GatewayHookDescriptor
		wantErr    func(error) bool
		wantStatus GatewayHookRunStatus
	}{
		{
			name:       "fail open timeout skips hook",
			hook:       GatewayHookDescriptor{PluginID: "tokenhub.slow", HookID: "open", Stage: StageContextOptimize, TimeoutMillis: 1, FailurePolicy: FailurePolicyFailOpen},
			wantStatus: HookRunSkipped,
		},
		{
			name:       "fail closed timeout blocks stage",
			hook:       GatewayHookDescriptor{PluginID: "tokenhub.slow", HookID: "closed", Stage: StageContextOptimize, TimeoutMillis: 1, FailurePolicy: FailurePolicyFailClosed},
			wantErr:    IsGatewayHookTimeout,
			wantStatus: HookRunFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chain := NewGatewayChainRegistry()
			if err := chain.RegisterHook(test.hook); err != nil {
				t.Fatalf("register hook: %v", err)
			}
			runner := NewGatewayHookRunner(chain)
			if err := runner.RegisterHandler(test.hook, GatewayHookHandlerFunc(func(ctx context.Context, _ GatewayHookInput) (GatewayHookResult, error) {
				<-ctx.Done()
				return GatewayHookResult{}, ctx.Err()
			})); err != nil {
				t.Fatalf("register handler: %v", err)
			}

			report, err := runner.RunStage(context.Background(), test.hook.Stage, GatewayHookInput{})
			if test.wantErr == nil && err != nil {
				t.Fatalf("run stage: %v", err)
			}
			if test.wantErr != nil && !test.wantErr(err) {
				t.Fatalf("error = %v, want timeout", err)
			}
			if len(report.Results) != 1 || report.Results[0].Status != test.wantStatus {
				t.Fatalf("report = %+v, want status %s", report, test.wantStatus)
			}
		})
	}
}

func TestGatewayHookRunnerObserveOnlyCannotBlockOrMutate(t *testing.T) {
	chain := NewGatewayChainRegistry()
	observe := GatewayHookDescriptor{
		PluginID:      "tokenhub.observe",
		HookID:        "rank",
		Stage:         StageRouteRank,
		Priority:      100,
		Reads:         []GatewayDataClass{DataRouteCandidates},
		Writes:        []GatewayDataClass{DataRouteCandidates},
		FailurePolicy: FailurePolicyObserveOnly,
	}
	next := GatewayHookDescriptor{
		PluginID: "tokenhub.next",
		HookID:   "rank",
		Stage:    StageRouteRank,
		Priority: 200,
		Reads:    []GatewayDataClass{DataRouteCandidates},
	}
	for _, hook := range []GatewayHookDescriptor{observe, next} {
		if err := chain.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(observe, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{
			Decision:    HookDecisionDeny,
			AuditEvents: []json.RawMessage{json.RawMessage(`{"observation":"ranked"}`)},
			Writes: map[GatewayDataClass]RawPatch{
				DataRouteCandidates: {Value: json.RawMessage(`[{"route_id":"mutated"}]`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register observe handler: %v", err)
	}
	nextCalled := false
	if err := runner.RegisterHandler(next, GatewayHookHandlerFunc(func(_ context.Context, input GatewayHookInput) (GatewayHookResult, error) {
		nextCalled = true
		if string(input.Data[DataRouteCandidates]) != `[{"route_id":"original"}]` {
			t.Fatalf("route candidates = %s, want original", input.Data[DataRouteCandidates])
		}
		return GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register next handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StageRouteRank, GatewayHookInput{
		Data: GatewayHookData{
			DataRouteCandidates: json.RawMessage(`[{"route_id":"original"}]`),
		},
	})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if !nextCalled {
		t.Fatal("observe-only hook blocked later hooks")
	}
	if len(report.Results) != 2 || report.Results[0].Decision != HookDecisionContinue || len(report.Results[0].Writes) != 0 || report.TerminalDecision != "" {
		t.Fatalf("observe-only report = %+v", report)
	}
	if len(report.Results[0].AuditEvents) != 1 || string(report.Results[0].AuditEvents[0]) != `{"observation":"ranked"}` {
		t.Fatalf("observe-only audit events = %v, want preserved observation event", report.Results[0].AuditEvents)
	}
}

func TestGatewayHookRunnerSkipsHooksOutsideDeclaredScope(t *testing.T) {
	chain := NewGatewayChainRegistry()
	hook := GatewayHookDescriptor{
		PluginID: "tokenhub.project",
		HookID:   "admit",
		Stage:    StageAdmission,
		Scope:    GatewayHookScope{ProjectIDs: []string{"prj_allowed"}},
	}
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatalf("register hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	called := false
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		called = true
		return GatewayHookResult{Decision: HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	report, err := runner.RunStage(context.Background(), StageAdmission, GatewayHookInput{
		Data: GatewayHookData{
			DataProjectMetadata: json.RawMessage(`{"id":"prj_other"}`),
		},
	})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
	if called {
		t.Fatal("scoped hook ran for the wrong project")
	}
	if len(report.Results) != 1 || report.Results[0].Status != HookRunSkipped {
		t.Fatalf("report = %+v, want one skipped scoped hook", report)
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

func TestGatewayHookRunnerPreservesOriginalEnvelopeAcrossStageWrites(t *testing.T) {
	chain := NewGatewayChainRegistry()
	first := GatewayHookDescriptor{
		PluginID: "tokenhub.privacy",
		HookID:   "mask",
		Stage:    StagePrivacyPre,
		Priority: 100,
		Reads:    []GatewayDataClass{DataRequestBody},
		Writes:   []GatewayDataClass{DataRequestBody},
	}
	second := GatewayHookDescriptor{
		PluginID: "tokenhub.audit",
		HookID:   "compare",
		Stage:    StagePrivacyPre,
		Priority: 200,
		Reads:    []GatewayDataClass{DataRequestBody},
	}
	for _, hook := range []GatewayHookDescriptor{first, second} {
		if err := chain.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	runner := NewGatewayHookRunner(chain)
	if err := runner.RegisterHandler(first, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{
			Writes: map[GatewayDataClass]RawPatch{
				DataRequestBody: {Value: json.RawMessage(`{"masked":true}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register first handler: %v", err)
	}
	if err := runner.RegisterHandler(second, GatewayHookHandlerFunc(func(_ context.Context, input GatewayHookInput) (GatewayHookResult, error) {
		if string(input.Envelope.RequestBody) != `{"masked":true}` {
			t.Fatalf("current request body = %s, want masked body", input.Envelope.RequestBody)
		}
		if string(input.OriginalEnvelope.RequestBody) != `{"secret":"visible-to-stage"}` {
			t.Fatalf("original request body = %s, want immutable original", input.OriginalEnvelope.RequestBody)
		}
		if string(input.OriginalData[DataRequestBody]) != `{"secret":"visible-to-stage"}` {
			t.Fatalf("original data = %s, want immutable original data", input.OriginalData[DataRequestBody])
		}
		return GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register second handler: %v", err)
	}

	_, err := runner.RunStage(context.Background(), StagePrivacyPre, GatewayHookInput{
		RequestID: "req_original",
		Envelope: GatewayEnvelope{
			Version:     "v1",
			RequestBody: json.RawMessage(`{"secret":"visible-to-stage"}`),
		},
		Data: GatewayHookData{
			DataRequestBody: json.RawMessage(`{"secret":"visible-to-stage"}`),
		},
	})
	if err != nil {
		t.Fatalf("run stage: %v", err)
	}
}

func TestGatewayHookInputOriginalsAreInternalOnly(t *testing.T) {
	input := GatewayHookInput{
		RequestID: "req_secret",
		Envelope:  GatewayEnvelope{Version: "v1"},
		OriginalEnvelope: GatewayEnvelope{
			RequestBody: json.RawMessage(`{"secret":"original-envelope"}`),
		},
		OriginalData: GatewayHookData{
			DataRequestBody: json.RawMessage(`{"secret":"original-data"}`),
		},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if strings.Contains(string(encoded), "original-envelope") || strings.Contains(string(encoded), "original-data") || strings.Contains(string(encoded), "Original") {
		t.Fatalf("internal originals leaked to hook JSON: %s", encoded)
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
