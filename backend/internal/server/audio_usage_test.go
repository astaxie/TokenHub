package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAudioDurationUsagePresenceAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body, source string
		seconds            float64
		present, invalid   bool
	}{
		{"duration usage", `{"usage":{"type":"duration","seconds":12.5}}`, "upstream", 12.5, true, false},
		{"measured zero", `{"usage":{"type":"duration","seconds":0}}`, "upstream", 0, true, false},
		{"verbose duration", `{"duration":0.125,"text":"fixture"}`, "upstream", 0.125, true, false},
		{"declared usage takes precedence", `{"duration":99,"usage":{"type":"duration","seconds":3}}`, "upstream", 3, true, false},
		{"missing duration", `{"text":"fixture"}`, "", 0, false, false},
		{"unreported native duration", `{"usage":{"type":"duration"}}`, "unreported", 0, false, false},
		{"null duration", `{"usage":{"type":"duration","seconds":null}}`, "invalid", 0, false, true},
		{"negative duration", `{"usage":{"type":"duration","seconds":-0.1}}`, "invalid", 0, false, true},
		{"string duration", `{"duration":"12.5"}`, "invalid", 0, false, true},
		{"overflow duration", `{"duration":1e309}`, "invalid", 0, false, true},
		{"underflow duration", `{"duration":1e-400}`, "invalid", 0, false, true},
		{"object duration", `{"duration":{}}`, "invalid", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := inspectMediaJSON([]byte(tc.body), "/audio/transcriptions")
			if (err != nil) != tc.invalid || usage.AudioDurationSource != tc.source || (usage.AudioDurationSeconds != nil) != tc.present {
				t.Fatalf("usage=%+v error=%v", usage, err)
			}
			if tc.present && *usage.AudioDurationSeconds != tc.seconds {
				t.Fatalf("seconds=%v want=%v", *usage.AudioDurationSeconds, tc.seconds)
			}
			if usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 || usage.CostUSD != 0 {
				t.Fatalf("native seconds invented token usage or cost: %+v", usage)
			}
			streamUsage, streamErr := inspectMediaStream([]byte("data: "+tc.body+"\n\n"), Provider{}, "/audio/transcriptions")
			if (streamErr != nil) != tc.invalid || streamUsage.AudioDurationSource != tc.source {
				t.Fatalf("stream usage=%+v error=%v", streamUsage, streamErr)
			}
		})
	}
}

func TestAudioDurationAttributionPreservesIndependentEvidence(t *testing.T) {
	server := New(NewMemoryStore())
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.audio-usage", HookID: "attribute", Stage: pluginmeta.StageUsageAttribution, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var seen Usage
		if err := json.Unmarshal(input.Data[pluginmeta.DataUsage], &seen); err != nil {
			t.Fatal(err)
		}
		if seen.AudioDurationSeconds == nil || *seen.AudioDurationSeconds != 12.5 {
			t.Fatalf("missing hook input evidence: %+v", seen)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataUsage: {Value: json.RawMessage(`{"prompt_tokens":3,"total_tokens":3,"document_count":99,"estimated_cost_usd":999}`)}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	original := captureAudioDuration(Usage{}, map[string]any{"duration": 12.5}, true)
	original.DocumentCount = 2
	usage, err := server.runGatewayUsageAttributionHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, nil, original, "audio/transcriptions")
	if err != nil || usage.AudioDurationSeconds == nil || *usage.AudioDurationSeconds != 12.5 || usage.DocumentCount != 2 || usage.PromptTokens != 3 {
		t.Fatalf("attribution lost evidence: %+v, %v", usage, err)
	}
	priced := priceUsage(Model{Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.02"}}, usage)
	if priced.CostUSD != 0.25 {
		t.Fatalf("plugin overrode authoritative duration price: %+v", priced)
	}
}

func TestAudioDurationDoesNotRepriceOtherMediaOrDeclaredTokens(t *testing.T) {
	for _, tc := range []struct {
		name, body, endpoint, modality string
		wantSeconds                    bool
	}{
		{"video duration", `{"duration":12.5,"usage":{"input_tokens":100,"total_tokens":100}}`, "/videos", "video", false},
		{"video string duration", `{"duration":"12s","usage":{"input_tokens":100,"total_tokens":100}}`, "/videos", "video", false},
		{"token transcription", `{"text":"fixture","duration":12.5,"usage":{"type":"tokens","input_tokens":100,"total_tokens":100}}`, "/audio/transcriptions", "audio", true},
		{"zero token transcription", `{"text":"fixture","duration":12.5,"usage":{"type":"tokens","input_tokens":0,"total_tokens":0}}`, "/audio/transcriptions", "audio", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, err := inspectMediaJSON([]byte(tc.body), tc.endpoint)
			if err != nil || (usage.AudioDurationSeconds != nil) != tc.wantSeconds {
				t.Fatalf("incorrect duration scope: %+v, %v", usage, err)
			}
			model := Model{Modality: tc.modality, InputPriceUSDPer1M: 1, Metadata: map[string]string{audioSecondPriceKey: "1"}}
			priced := priceUsage(model, usage)
			want := float64(usage.PromptTokens) / 1_000_000
			if priced.CostUSD != want {
				t.Fatalf("audio metadata changed token billing: %+v want %g", priced, want)
			}
			price := legacyMeteringPrice(model, time.Now().UTC(), false)
			shadow := shadowPrice(&price, priced, priced.CostUSD)
			if shadow.Charge != nil {
				for _, line := range shadow.Charge.Lines {
					if line.Kind == "audio_second" {
						t.Fatalf("token evidence became audio billing: %+v", shadow)
					}
				}
			}
		})
	}
	for _, modality := range []string{"rerank", "chat", "video"} {
		usage := Usage{PromptTokens: 100, TotalTokens: 100, AudioDurationSeconds: audioTestSeconds(12.5), AudioDurationUnit: "audio_second"}
		model := Model{Modality: modality, InputPriceUSDPer1M: 1, Metadata: map[string]string{audioSecondPriceKey: "1"}}
		if priced := priceUsage(model, usage); priced.CostUSD != .0001 {
			t.Fatalf("plugin audio injection changed %s billing: %+v", modality, priced)
		}
	}
}

func TestAudioDurationKeepsOriginalDecimalEvidence(t *testing.T) {
	const body = `{"usage":{"type":"duration","seconds":1.234567890123456789}}`
	for _, stream := range []bool{false, true} {
		var usage Usage
		var err error
		if stream {
			usage, err = inspectMediaStream([]byte("data: "+body+"\n\n"), Provider{}, "/audio/transcriptions")
		} else {
			usage, err = inspectMediaJSON([]byte(body), "/audio/transcriptions")
		}
		if err != nil || usage.AudioDurationRaw != "1.234567890123456789" {
			t.Fatalf("original duration lost: %+v, %v", usage, err)
		}
		charge, err := priceAudioEvidence(audioDurationEvidence(usage), "1")
		if err != nil || charge.Lines[0].Quantity != "1.234567890123456789" {
			t.Fatalf("native precision lost: %+v, %v", charge, err)
		}
	}
}

func TestAudioDurationAttributionRejectsInvalidPluginQuantity(t *testing.T) {
	for _, patch := range []string{`{"audio_duration_seconds":-1}`, `{"audio_duration_seconds":null}`, `{"audio_duration_source":"invalid"}`} {
		t.Run(patch, func(t *testing.T) {
			server := New(NewMemoryStore())
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.audio-invalid", HookID: "attribute", Stage: pluginmeta.StageUsageAttribution, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataUsage: {Value: json.RawMessage(patch)}}}, nil
			})); err != nil {
				t.Fatal(err)
			}
			original := captureAudioDuration(Usage{}, map[string]any{"duration": 12.5}, true)
			call := gatewayPluginTestCall()
			call.Model.Modality = "audio"
			usage, err := server.runGatewayUsageAttributionHooks(context.Background(), call, RouteSelection{}, nil, original, "audio/transcriptions")
			if err == nil || AsHTTPError(err).Code != "invalid_provider_usage" || usage.AudioDurationSeconds == nil || *usage.AudioDurationSeconds != 12.5 {
				t.Fatalf("invalid patch erased original usage: %+v, %v", usage, err)
			}
		})
	}
}
