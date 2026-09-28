package server

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

// Qwen Image 3.0 returns a native output.choices object through /v1/responses.
func TestMediaQwenOutputObjectMatchesSynchronousAndBackgroundSchemas(t *testing.T) {
	const responseBody = `{"output":{"choices":[{"finish_reason":"stop","message":{"content":[{"image":"https://example.com/image.png","type":"image"}],"role":"assistant"}}],"rewrite_status":"success","vendor_id":9007199254740993},"usage":{"output_image_count":1,"output_width":2048,"output_height":2048},"request_id":"native_qwen_fixture"}`
	document := mediaOpenAPIDocument(t)
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "synchronous", true: "background"}[background], func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]json.RawMessage
				decodeFixtureRequest(t, r.Body, &payload)
				if string(payload["model"]) != `"vendor-media"` || string(payload["input"]) != `{"messages":[{"content":[{"text":"A family portrait"}],"role":"user"}]}` {
					t.Errorf("native request changed: %s", payload)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, responseBody)
			}, "image")
			request := map[string]any{"model": "public-media", "input": map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"text": "A family portrait"}}}}}, "parameters": map[string]any{"n": 1, "enable_thinking": true, "prompt_extend": true}, "background": background}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", request, key)
			if response.Code != http.StatusOK {
				t.Fatalf("Qwen image request failed: %d %s", response.Code, response.Body)
			}
			assertMediaOperationSchema(t, document, "/v1/responses", "post", "200", response.Body)
			if background {
				var submitted struct{ ID string }
				if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
					t.Fatalf("invalid background response: %s error=%v", response.Body, err)
				}
				waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, "completed")
				response = doJSON(t, server.Handler(), http.MethodGet, "/v1/responses/"+submitted.ID, nil, key)
				if response.Code != http.StatusOK {
					t.Fatalf("Qwen image polling failed: %d %s", response.Code, response.Body)
				}
				assertMediaOperationSchema(t, document, "/v1/responses/{response_id}", "get", "200", response.Body)
			}
			var got, want map[string]any
			if err := decodeResponsesJSON([]byte(response.Body), &got); err != nil {
				t.Fatal(err)
			}
			if err := decodeResponsesJSON([]byte(responseBody), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got["output"], want["output"]) || calls != 1 {
				t.Fatalf("native output changed: calls=%d got=%v want=%v", calls, got["output"], want["output"])
			}
			for _, usage := range store.ListUsageRecords() {
				if usage.TotalTokens != 0 {
					t.Fatalf("native image count became token usage: %+v", usage)
				}
			}
		})
	}
}
