package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaProviderHookBase64UsesDecodedSize(t *testing.T) {
	const limit = 8
	for _, size := range []int{0, 1, 2, 3, limit - 1, limit, limit + 1, limit + 2, 2 * limit} {
		original := bytes.Repeat([]byte{255}, size)
		encoded := base64.StdEncoding.EncodeToString(original)
		for _, separator := range []string{"", "\n", "\r\n", strings.Repeat("\r\n", 1024)} {
			wrapped := separator + strings.Join(strings.Split(encoded, ""), separator) + separator
			result, err := mediaProviderHookResponse(map[string]any{"data_base64": wrapped, "content_type": "audio/mpeg"}, limit)
			if size > limit {
				if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || len(result.Body) != 0 {
					t.Errorf("oversized output accepted: size=%d separator bytes=%d result=%+v error=%v", size, len(separator), result, err)
				}
			} else if err != nil || !bytes.Equal(result.Body, original) || result.ContentType != "audio/mpeg" {
				t.Errorf("valid binary changed: size=%d separator bytes=%d result=%+v error=%v", size, len(separator), result, err)
			}
		}
	}
}

func TestMediaProviderHookRejectsDataAfterBase64Padding(t *testing.T) {
	for _, separator := range []string{"", "\n", strings.Repeat("\r\n", 1024)} {
		for _, tail := range []string{"AAAA", "Yg==", "=", "!"} {
			result, err := mediaProviderHookResponse(map[string]any{"data_base64": "YQ==" + separator + tail, "content_type": "audio/mpeg"}, 64)
			if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || len(result.Body) != 0 {
				t.Errorf("trailing base64 data accepted: separator bytes=%d tail=%q result=%+v error=%v", len(separator), tail, result, err)
			}
		}
	}
}

func TestMediaProviderHookMultilineBinaryRetainsDeliveryAndUsage(t *testing.T) {
	server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	hookCalls := 0
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "multiline-binary", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/speech"}}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		hookCalls++
		return rawProviderCallResult(t, map[string]any{"data_base64": "A\r\nP\r\n8\r\nq", "content_type": "audio/mpeg"}, Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	project, apiKey, err := server.authenticate(request)
	if err != nil {
		t.Fatal(err)
	}
	call, err := server.admitRoutedCall(httptest.NewRecorder(), request, project, apiKey, "classified-model", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	routed, err := server.prepareAdmittedRoutedCall(request.Context(), call, call.Model.Name)
	if err != nil {
		t.Fatal(err)
	}
	result, route, usage, attempts, err := executeRoutedWithStore(request.Context(), store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (mediaResponse, Usage, error) {
		return server.invokeMediaRouteWithResponseLimit(ctx, call, route, "/audio/speech", mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"classified-model"`)}}, 3)
	})
	if err != nil || hookCalls != 1 || !bytes.Equal(result.Body, []byte{0, 255, 42}) || result.ContentType != "audio/mpeg" || usage.TotalTokens != 5 {
		t.Fatalf("multiline hook response changed: calls=%d result=%+v usage=%+v error=%v", hookCalls, result, usage, err)
	}
	server.finishSuccessfulRoutedCall(request, routed, route, usage, attempts, nil, nil)
	records := store.ListUsageRecords()
	var logged []RouteAttemptLog
	if err := store.db.Find(&logged).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].TotalTokens != 5 || len(logged) != 1 || logged[0].TotalTokens != 5 || logged[0].StatusCode != http.StatusOK {
		t.Fatalf("multiline hook accounting changed: records=%+v attempts=%+v", records, logged)
	}
}
