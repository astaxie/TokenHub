package server

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaUsageOutputAliasDoesNotDoubleCountOrChangeTextPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, raw                 string
		output, total, textOutput int64
	}{
		{"documented Seedream aliases", `{"prompt_tokens":0,"completion_tokens":0,"output_tokens":16464,"total_tokens":16464}`, 16464, 16464, 0},
		{"derive absent total", `{"prompt_tokens":3,"completion_tokens":0,"output_tokens":7}`, 7, 10, 0},
		{"retain reported total", `{"prompt_tokens":3,"completion_tokens":0,"output_tokens":7,"total_tokens":12}`, 7, 12, 0},
		{"derive reported zero total", `{"prompt_tokens":3,"completion_tokens":0,"output_tokens":7,"total_tokens":0}`, 7, 10, 0},
		{"derive null total", `{"prompt_tokens":3,"completion_tokens":0,"output_tokens":7,"total_tokens":null}`, 7, 10, 0},
		{"derive total without overflow", `{"prompt_tokens":9223372036854775807,"completion_tokens":0,"output_tokens":7}`, 7, 9223372036854775807, 0},
		{"positive completion keeps precedence", `{"completion_tokens":4,"output_tokens":9,"total_tokens":13}`, 4, 13, 4},
		{"standard output only", `{"output_tokens":7}`, 7, 7, 7},
		{"standard completion only", `{"completion_tokens":4}`, 4, 4, 4},
		{"zero output", `{"completion_tokens":0,"output_tokens":0,"total_tokens":0}`, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if err := decodeResponsesJSON([]byte(`{"usage":`+tc.raw+`}`), &body); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			usage := mediaUsageFromMap(body)
			if usage.CompletionTokens != tc.output || usage.TotalTokens != tc.total || usageFromMap(body).CompletionTokens != tc.textOutput {
				t.Fatalf("media alias accounting or text precedence changed: media=%+v text=%+v", usage, usageFromMap(body))
			}
			after, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("accounting normalization changed the provider payload")
			}
		})
	}
}

func TestMediaSeedreamUsageAliasesPreserveDeliveryAndCosts(t *testing.T) {
	const payload = `{"created":1765372616,"data":[{"url":"https://example.com/image.jpeg"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":16464,"prompt_tokens_details":{"cached_tokens_details":{}},"completion_tokens_details":{},"output_tokens":16464}}`
	for _, tc := range []struct {
		name, explicitUsage             string
		stream, nested, hook, omitTotal bool
		responses                       bool
		wantOutput                      int64
	}{
		{name: "direct JSON", wantOutput: 16464},
		{name: "direct JSON without total", omitTotal: true, wantOutput: 16464},
		{name: "direct SSE", stream: true, wantOutput: 16464},
		{name: "nested SSE", stream: true, nested: true, wantOutput: 16464},
		{name: "media Responses SSE", stream: true, responses: true, wantOutput: 16464},
		{name: "provider hook JSON", hook: true, wantOutput: 16464},
		{name: "provider hook override", hook: true, explicitUsage: `{"completion_tokens":3,"total_tokens":3}`, wantOutput: 3},
		{name: "provider hook explicit zero", hook: true, explicitUsage: `{"total_tokens":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := payload
			if tc.omitTotal {
				body = strings.ReplaceAll(body, `"total_tokens":16464,`, "")
			}
			if tc.nested {
				body = `{"response":` + body + `}`
			}
			contentType := "application/json"
			if tc.stream {
				contentType = "text/event-stream"
				body = "data: " + body + "\n\ndata: [DONE]\n\n"
			}
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, body)
			}, "image")
			if err := store.db.Model(&Model{}).Where("name = ?", "public-media").Updates(Model{OutputPriceUSDPer1M: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&ProviderModel{}).Where("provider_id = ? AND upstream_model = ?", "media-provider", "vendor-media").Updates(ProviderModel{OutputPriceUSDPer1M: 2}).Error; err != nil {
				t.Fatal(err)
			}
			if tc.hook {
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "seedream-usage", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(body)}}
					if tc.explicitUsage != "" {
						writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
				})
			}
			path := "/v1/images/generations"
			request := map[string]any{"model": "public-media", "stream": tc.stream}
			if tc.responses {
				path = "/v1/responses"
				request["input"] = "A landscape"
			} else {
				request["prompt"] = "A landscape"
			}
			recorder := doReasoningJSON(t, server.Handler(), path, request, key)
			response := responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
			if response.Code != http.StatusOK || response.Header.Get("Content-Type") != contentType || calls != map[bool]int{false: 1, true: 0}[tc.hook] {
				t.Fatalf("media delivery changed: status=%d content_type=%q calls=%d body=%s", response.Code, response.Header.Get("Content-Type"), calls, response.Body)
			}
			if tc.stream {
				if response.Body != body {
					t.Fatal("media accounting rewrote SSE bytes")
				}
			} else {
				var got, want any
				if err := decodeResponsesJSON([]byte(response.Body), &got); err != nil {
					t.Fatal(err)
				}
				if err := decodeResponsesJSON([]byte(body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("media accounting rewrote provider JSON")
				}
			}
			var output, total int64
			var cost, outputCost, providerCost float64
			for _, record := range store.ListUsageRecords() {
				output += record.OutputTokens
				total += record.TotalTokens
				cost += record.CostUSD
				outputCost += record.OutputCostUSD
				providerCost += record.ProviderCostUSD
			}
			wantCost := float64(tc.wantOutput) / 1_000_000
			if output != tc.wantOutput || total != tc.wantOutput || math.Abs(cost-wantCost) > 1e-12 || math.Abs(outputCost-wantCost) > 1e-12 || math.Abs(providerCost-2*wantCost) > 1e-12 {
				t.Fatalf("image accounting lost usage or price: output=%d total=%d cost=%g output_cost=%g provider_cost=%g", output, total, cost, outputCost, providerCost)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].OutputTokens != tc.wantOutput || attempts[0].TotalTokens != tc.wantOutput || math.Abs(attempts[0].CostUSD-wantCost) > 1e-12 {
				t.Fatalf("attempt accounting lost usage or price: %+v", attempts)
			}
		})
	}
}
