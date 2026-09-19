package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const systemOneMalformedAnswers = `{"model":"jev-1.13.0","answers":[],"usage":{"input_tokens":422,"output_tokens":69}}`

func TestTypeSafeMalformedResponsePreservesOnlyValidUsage(t *testing.T) {
	for _, tt := range []struct {
		name, payload string
		validUsage    bool
	}{
		{"wrong answers type", systemOneMalformedAnswers, true},
		{"wrong model type", strings.Replace(systemOneMalformedAnswers, `"model":"jev-1.13.0"`, `"model":false`, 1), true},
		{"wrong token type", strings.Replace(systemOneMalformedAnswers, `"input_tokens":422`, `"input_tokens":"bad"`, 1), false},
		{"overflow token", strings.Replace(systemOneMalformedAnswers, `"input_tokens":422`, `"input_tokens":9223372036854775808`, 1), false},
		{"negative token", strings.Replace(systemOneMalformedAnswers, `"input_tokens":422`, `"input_tokens":-1`, 1), false},
		{"missing token", strings.Replace(systemOneMalformedAnswers, `"input_tokens":422,`, ``, 1), false},
		{"null usage", `{"model":"jev-test","answers":[],"usage":null}`, false},
		{"wrong usage type", `{"model":"jev-test","answers":[],"usage":[]}`, false},
		{"truncated JSON", systemOneMalformedAnswers[:len(systemOneMalformedAnswers)-1], false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeFixture(t, w, tt.payload)
			}))
			defer upstream.Close()
			_, usage, err := (TypeSafeAdapter{Client: upstream.Client()}).SystemOne(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "synthetic-key"}, "jev-latest", systemOneTestRequest(t))
			if err == nil || AsHTTPError(err).Status != http.StatusBadGateway {
				t.Fatalf("malformed response error=%v", err)
			}
			if usage.MeteringInvalid == tt.validUsage || (tt.validUsage && usage.TotalTokens != 491) || (!tt.validUsage && usage.TotalTokens != 0) {
				t.Fatalf("valid usage=%v actual=%+v", tt.validUsage, usage)
			}
		})
	}
}

func TestGatewaySystemOneMalformedEnvelopeRetainsAttemptUsage(t *testing.T) {
	for _, hookProvider := range []bool{false, true} {
		name := "adapter"
		if hookProvider {
			name = "provider hook"
		}
		t.Run(name, func(t *testing.T) {
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if hookProvider {
					t.Error("provider hook was bypassed")
				}
				writeFixture(t, w, systemOneMalformedAnswers)
			})
			if hookProvider {
				registerSystemOneTestHook(t, server, pluginmeta.StageProviderCall, pluginmeta.DataProviderRequest, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					result := rawProviderCallResult(t, json.RawMessage(systemOneMalformedAnswers), Usage{})
					delete(result.Writes, pluginmeta.DataUsage)
					return result, nil
				})
			}
			emitter := &recordingTraceEmitter{}
			server.traceEmitter = emitter
			response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "provider_invalid_response") || strings.Contains(response.Body, "answers") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			completions := emitter.take()
			if len(completions) != 1 || len(completions[0].Attempts) != 1 {
				t.Fatalf("expected one completed attempt, got %d completions", len(completions))
			}
			if usage := completions[0].Attempts[0].Usage; usage.TotalTokens != 491 || usage.MeteringInvalid {
				t.Fatalf("valid usage lost from malformed envelope: %+v", usage)
			}
		})
	}
}
