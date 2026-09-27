package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaHookFailurePreservesAttemptUsage(t *testing.T) {
	for _, tc := range []struct {
		name             string
		failureStage     pluginmeta.GatewayHookStage
		wantInputTokens  int64
		wantTotalTokens  int64
		wantAttributions int
	}{
		{name: "invalid binary after attribution", wantTotalTokens: 7, wantAttributions: 1},
		{name: "attribution failure", failureStage: pluginmeta.StageUsageAttribution, wantInputTokens: 3, wantTotalTokens: 10, wantAttributions: 1},
		{name: "response denial before attribution", failureStage: pluginmeta.StageResponsePost, wantInputTokens: 3, wantTotalTokens: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, attributions := 0, 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if tc.failureStage == "" {
					w.Header().Set("Content-Type", "audio/mpeg")
					_, _ = io.WriteString(w, "audio")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"fixture","usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
			}, "audio")
			if err := store.db.Model(&ProviderModel{}).Where("provider_id = ? AND upstream_model = ?", "media-provider", "vendor-media").Updates(ProviderModel{InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6}).Error; err != nil {
				t.Fatal(err)
			}
			store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageUsageAttribution} {
				writes := pluginmeta.DataProviderResponse
				if stage == pluginmeta.StageUsageAttribution {
					writes = pluginmeta.DataUsage
				}
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-attempt-usage", HookID: string(stage), Stage: stage, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{writes}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					if stage == pluginmeta.StageUsageAttribution {
						attributions++
					}
					if stage == tc.failureStage {
						if stage == pluginmeta.StageUsageAttribution {
							return pluginmeta.GatewayHookResult{}, errors.New("fixture attribution failure")
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
					}
					if stage == pluginmeta.StageResponsePost && tc.failureStage != "" {
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
					}
					value := json.RawMessage(`{"data_base64":"!invalid"}`)
					if stage == pluginmeta.StageUsageAttribution {
						value = json.RawMessage(`{"completion_tokens":7,"total_tokens":7}`)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{writes: {Value: value}}}, nil
				})); err != nil {
					t.Fatal(err)
				}
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "hello"}, key)
			wantStatus := http.StatusForbidden
			if tc.failureStage == "" {
				wantStatus = http.StatusBadGateway
			} else if tc.failureStage == pluginmeta.StageUsageAttribution {
				wantStatus = http.StatusInternalServerError
			}
			if response.Code != wantStatus || calls != 1 || attributions != tc.wantAttributions {
				t.Fatalf("unexpected hook outcome: status=%d calls=%d attributions=%d body=%s", response.Code, calls, attributions, response.Body)
			}
			records := store.ListUsageRecords()
			if len(records) != 1 || records[0].TotalTokens != tc.wantTotalTokens {
				t.Fatalf("request usage lost after hook failure: %+v", records)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].InputTokens != tc.wantInputTokens || attempts[0].OutputTokens != 7 || attempts[0].TotalTokens != tc.wantTotalTokens {
				t.Fatalf("attempt usage lost after hook failure: %+v", attempts)
			}
			rows, err := store.MeteringEvidence(attempts[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var settlement struct {
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			for _, row := range rows {
				if row.Kind == "shadow_settlement" {
					if err := json.Unmarshal(row.Data, &settlement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(settlement.Attempts) != 1 || settlement.Attempts[0].Charge.Units.Input != tc.wantInputTokens || settlement.Attempts[0].Charge.Units.Output != 7 {
				t.Fatalf("provider shadow usage lost after hook failure: %+v", settlement)
			}
		})
	}
}
