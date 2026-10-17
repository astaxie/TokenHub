package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionHookOutputFailureRetainsDuration(t *testing.T) {
	for _, protocol := range []string{"audio/transcriptions", "audio/translations", "audio/speech"} {
		for _, tc := range []struct {
			name, evidence, explicitUsage string
			malformedEnvelope             bool
			wantSeconds                   *float64
		}{
			{name: "verbose overflow", evidence: `"duration":12.5`, wantSeconds: audioTestSeconds(12.5)},
			{name: "native overflow", evidence: `"usage":{"type":"duration","seconds":12.5}`, wantSeconds: audioTestSeconds(12.5)},
			{name: "malformed envelope", evidence: `"duration":12.5`, malformedEnvelope: true, wantSeconds: audioTestSeconds(12.5)},
			{name: "explicit duration overrides body", evidence: `"duration":12.5`, explicitUsage: `{"audio_duration_seconds":3,"audio_duration_unit":"audio_second"}`, wantSeconds: audioTestSeconds(3)},
			{name: "explicit zero overrides body", evidence: `"duration":12.5`, explicitUsage: `{"total_tokens":0}`},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				server := New(NewMemoryStore())
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				body := `{"text":"` + strings.Repeat("a", 256) + `",` + tc.evidence + `}`
				limit := 128
				if tc.malformedEnvelope {
					body = `{"data_base64":"!","content_type":"audio/mpeg",` + tc.evidence + `}`
					limit = 1024
				}
				hookCalls := 0
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "audio-output-failure", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(body)}}
					if tc.explicitUsage != "" {
						writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
				})
				call := gatewayPluginTestCall()
				call.Model = Model{Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.02"}}
				call.RouteProtocol = protocol
				response, usage, err := server.invokeMediaRouteWithResponseLimit(context.Background(), call, RouteSelection{}, "/"+protocol, mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"fixture"`)}}, limit)
				if err == nil || AsHTTPError(err).Code != "gateway_hook_response_invalid" || providerErrorDisposition(err) != ProviderErrorPolicy || len(response.Body) != 0 || hookCalls != 1 || !usage.MeteringInvalid {
					t.Fatalf("hook failure changed: calls=%d bytes=%d usage=%+v error=%v", hookCalls, len(response.Body), usage, err)
				}
				wantSeconds := tc.wantSeconds
				if protocol == "audio/speech" && tc.explicitUsage == "" && tc.name != "native overflow" {
					wantSeconds = nil
				}
				if (usage.AudioDurationSeconds != nil) != (wantSeconds != nil) || wantSeconds != nil && *usage.AudioDurationSeconds != *wantSeconds {
					t.Fatalf("duration lost or captured outside transcription: usage=%+v want=%v", usage, wantSeconds)
				}
				priced := priceUsage(call.Model, usage)
				wantCost := 0.0
				if wantSeconds != nil {
					wantCost = *wantSeconds * .02
				}
				if priced.CostUSD != wantCost || priced.TotalTokens != 0 {
					t.Fatalf("failure lost native pricing or invented tokens: usage=%+v want cost=%g", priced, wantCost)
				}
			})
		}
	}
}
