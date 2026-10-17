package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMultipartTranscriptionHookFailureRetainsIndependentUsage(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("multipart hook response reached the native adapter")
	}, "audio")
	store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
	store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
	server.store = audioNoPayloadStore{GormStore: store}
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{
		HookID: "multipart-usage", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}},
		Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
	}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"text":"private hook transcript"}`)},
			pluginmeta.DataUsage:            {Value: json.RawMessage(`{"audio_duration_seconds":12.5,"audio_duration_source":"plugin","audio_duration_unit":"audio_second"}`)},
		}}, nil
	})

	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "multipart_transcription_hook_unsupported") || strings.Contains(response.Body.String(), "private hook transcript") {
		t.Fatalf("multipart hook failure contract changed: status=%d body=%s", response.Code, response.Body)
	}
	rows := store.ListUsageRecords()
	if len(rows) != 1 || rows[0].CostUSD != 0.25 || rows[0].ProviderCostUSD != 0.125 || rows[0].TotalTokens != 0 {
		t.Fatalf("independent duration usage was lost: %+v", rows)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].CostUSD != 0.25 || attempts[0].TotalTokens != 0 {
		t.Fatalf("attempt usage was lost: %+v", attempts)
	}
	var payloads int64
	if err := store.db.Model(&RequestPayloadLog{}).Count(&payloads).Error; err != nil || payloads != 0 {
		t.Fatalf("multipart payload was retained: count=%d error=%v", payloads, err)
	}
}

func TestTranscriptionProviderHookResponseFailureRetainsIndependentUsage(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("malformed provider hook response reached the native adapter")
	}, "audio")
	store.AddModel(Model{Name: "public-media", Modality: "audio", Status: StatusActive, Metadata: map[string]string{audioSecondPriceKey: "0.02"}})
	store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{audioSecondPriceKey: "0.01"}})
	server.store = audioNoPayloadStore{GormStore: store}
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{
		HookID: "malformed-transcription-response", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}},
		Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
	}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"text":`)},
			pluginmeta.DataUsage:            {Value: json.RawMessage(`{"audio_duration_seconds":12.5,"audio_duration_source":"plugin","audio_duration_unit":"audio_second"}`)},
		}}, nil
	})

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_response_invalid") {
		t.Fatalf("malformed hook response contract changed: status=%d body=%s", response.Code, response.Body)
	}
	rows := store.ListUsageRecords()
	if len(rows) != 1 || rows[0].CostUSD != 0.25 || rows[0].ProviderCostUSD != 0.125 || rows[0].TotalTokens != 0 {
		t.Fatalf("independent duration usage was lost: %+v", rows)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].CostUSD != 0.25 || attempts[0].TotalTokens != 0 {
		t.Fatalf("attempt usage was lost: %+v", attempts)
	}
}

func TestMalformedTranscriptionHookResponseRetainsTokensWithInvalidAudioEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage string
	}{
		{name: "negative seconds", usage: `{"prompt_tokens":200,"total_tokens":200,"audio_duration_seconds":-1,"audio_duration_source":"plugin","audio_duration_unit":"audio_second"}`},
		{name: "invalid source", usage: `{"prompt_tokens":200,"total_tokens":200,"audio_duration_seconds":12.5,"audio_duration_source":"provider-private","audio_duration_unit":"audio_second"}`},
		{name: "invalid unit", usage: `{"prompt_tokens":200,"total_tokens":200,"audio_duration_seconds":12.5,"audio_duration_source":"plugin","audio_duration_unit":"milliseconds"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Fatal("malformed provider hook response reached the native adapter")
			}, "audio")
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{
				HookID: "malformed-transcription-invalid-usage", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}},
				Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
			}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"text":`)},
					pluginmeta.DataUsage:            {Value: json.RawMessage(tc.usage)},
				}}, nil
			})

			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "invalid_provider_usage") {
				t.Fatalf("malformed hook response contract changed: status=%d body=%s", response.Code, response.Body)
			}
			rows := store.ListUsageRecords()
			if len(rows) != 1 || rows[0].InputTokens != 200 || rows[0].TotalTokens != 200 {
				t.Fatalf("valid token evidence was lost: %+v", rows)
			}
			var counters []QuotaBucket
			if err := store.db.Where("scope IN ?", []string{"day", "month", "minute"}).Find(&counters).Error; err != nil {
				t.Fatal(err)
			}
			for _, counter := range counters {
				if counter.TotalTokens != 200 {
					t.Fatalf("%s quota tokens=%d want=200", counter.Scope, counter.TotalTokens)
				}
			}
		})
	}
}
