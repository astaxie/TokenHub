package server

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerAudioUsageTraceCapture(t *testing.T, server *Server, captured *Usage) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.audio-trace-privacy", HookID: "capture", Stage: pluginmeta.StageTraceExport,
		Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if err := json.Unmarshal(input.Data[pluginmeta.DataUsage], captured); err != nil {
			t.Fatalf("invalid exported audio usage: %v", err)
		}
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
}

func TestAudioUsagePrivacyAtDirectTraceBoundaries(t *testing.T) {
	const private = "private-transcript-duration-metadata"
	precise := captureAudioDuration(Usage{}, map[string]any{"duration": json.Number("1.234567890123456789")}, true)
	for _, tc := range []struct {
		name                 string
		usage                Usage
		wantSource, wantUnit string
		wantRaw              string
		wantSeconds          *float64
	}{
		{name: "invalid source without duration", usage: Usage{AudioDurationSource: private}, wantSource: "invalid"},
		{name: "invalid unit without duration", usage: Usage{AudioDurationUnit: private}, wantSource: "invalid", wantUnit: "invalid"},
		{name: "invalid raw only", usage: Usage{AudioDurationRaw: private}},
		{name: "invalid raw with token unit", usage: Usage{AudioDurationRaw: private, AudioDurationUnit: "token"}, wantUnit: "token"},
		{name: "valid raw only", usage: Usage{AudioDurationRaw: "1.234567890123456789"}, wantRaw: "1.234567890123456789"},
		{name: "unreported native duration", usage: Usage{AudioDurationUnit: "audio_second", AudioDurationRaw: private}, wantUnit: "audio_second"},
		{name: "invalid negative seconds", usage: Usage{AudioDurationSeconds: audioTestSeconds(-1), AudioDurationUnit: "audio_second"}, wantSource: "invalid", wantUnit: "audio_second"},
		{name: "invalid nonfinite seconds", usage: Usage{AudioDurationSeconds: audioTestSeconds(math.NaN())}, wantSource: "invalid"},
		{name: "precise valid duration", usage: precise, wantSource: "upstream", wantUnit: "audio_second", wantRaw: "1.234567890123456789", wantSeconds: precise.AudioDurationSeconds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := New(NewMemoryStore())
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			traces := &recordingTraceEmitter{}
			server.traceEmitter = traces
			var exported Usage
			registerAudioUsageTraceCapture(t, server, &exported)
			input := GatewayCallCompletion{
				Call:  CallContext{Model: Model{Modality: "audio"}, RouteProtocol: "audio/transcriptions", RouteAttempts: []RouteAttempt{{Usage: tc.usage}}},
				Usage: tc.usage, Attempts: []RouteAttempt{{Usage: tc.usage}},
			}
			assertSafeUsage := func(t *testing.T, usage Usage) {
				t.Helper()
				if usage.AudioDurationSource != tc.wantSource || usage.AudioDurationUnit != tc.wantUnit || usage.AudioDurationRaw != tc.wantRaw || !reflect.DeepEqual(usage.AudioDurationSeconds, tc.wantSeconds) {
					t.Fatalf("audio evidence changed unexpectedly: %+v", usage)
				}
			}
			for _, boundary := range []string{"completion exports", "trace emitter", "trace hook"} {
				t.Run(boundary, func(t *testing.T) {
					exported = Usage{}
					switch boundary {
					case "completion exports":
						server.emitGatewayCompletionTraceExports(input)
					case "trace emitter":
						server.emitGatewayTrace(input)
					case "trace hook":
						server.runGatewayTraceExportHooks(context.Background(), input)
					}
					completed := traces.take()
					if boundary != "trace hook" {
						if len(completed) != 1 || len(completed[0].Attempts) != 1 || len(completed[0].Call.RouteAttempts) != 1 {
							t.Fatalf("missing trace attempt evidence: %+v", completed)
						}
						assertSafeUsage(t, completed[0].Usage)
						assertSafeUsage(t, completed[0].Attempts[0].Usage)
						assertSafeUsage(t, completed[0].Call.RouteAttempts[0].Usage)
					}
					if boundary != "trace emitter" {
						assertSafeUsage(t, exported)
					}
					encoded, err := json.Marshal([]any{completed, exported})
					if err != nil || strings.Contains(string(encoded), private) {
						t.Fatalf("private audio evidence reached trace export: %s, %v", encoded, err)
					}
				})
			}
			for _, usage := range []Usage{input.Usage, input.Attempts[0].Usage, input.Call.RouteAttempts[0].Usage} {
				if usage.AudioDurationSource != tc.usage.AudioDurationSource || usage.AudioDurationUnit != tc.usage.AudioDurationUnit || usage.AudioDurationRaw != tc.usage.AudioDurationRaw || usage.AudioDurationSeconds != tc.usage.AudioDurationSeconds {
					t.Fatal("sanitization mutated caller-owned evidence")
				}
			}
		})
	}
}

func TestAudioUsageSanitizationPreservesPricingClassification(t *testing.T) {
	const precise = "1.234567890123456789"
	for _, usage := range []Usage{
		captureAudioDuration(Usage{}, map[string]any{"duration": json.Number(precise)}, true),
		{AudioDurationSeconds: audioTestSeconds(12.5), AudioDurationSource: "private", AudioDurationUnit: "audio_second"},
		{AudioDurationSeconds: audioTestSeconds(12.5), AudioDurationSource: "plugin", AudioDurationUnit: "private"},
		{AudioDurationSeconds: audioTestSeconds(12.5), AudioDurationRaw: "private", AudioDurationUnit: "audio_second"},
		{AudioDurationUnit: "audio_second", AudioDurationRaw: "private", PromptTokens: 100, TotalTokens: 100},
		{AudioDurationRaw: "private", PromptTokens: 100, TotalTokens: 100},
	} {
		clean := sanitizeAudioUsage(usage)
		if audioDurationBillable(clean) != audioDurationBillable(usage) || (validateAudioDuration(clean) == nil) != (validateAudioDuration(usage) == nil) {
			t.Fatalf("sanitization changed billing unit or validity: before=%+v after=%+v", usage, clean)
		}
		before, beforeErr := priceAudioEvidence(audioDurationEvidence(usage), "0.03")
		after, afterErr := priceAudioEvidence(audioDurationEvidence(clean), "0.03")
		if (beforeErr == nil) != (afterErr == nil) || !reflect.DeepEqual(before, after) {
			t.Fatalf("sanitization changed exact pricing: before=%+v after=%+v", before, after)
		}
		if usage.AudioDurationRaw == precise && (afterErr != nil || after.Lines[0].Quantity != precise) {
			t.Fatalf("original duration precision lost: %+v, %v", after, afterErr)
		}
	}
}
