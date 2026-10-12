package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMediaImageVendorResponseFormatMatchesOpenAPI(t *testing.T) {
	// DMXAPI Qwen Image documents "base64", unlike OpenAI's "b64_json".
	payload := map[string]any{"model": "public-media", "prompt": "A landscape", "response_format": "base64"}
	wantResponse := `{"data":[{"b64_json":"aW1hZ2U="}],"extra":{"output":{"task_status":"SUCCEEDED"}}}`
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &request)
		if string(request["response_format"]) != `"base64"` {
			t.Errorf("response_format changed: %s", request["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, wantResponse)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", payload, key)
	if response.Code != http.StatusOK || response.Body != wantResponse {
		t.Fatalf("vendor response changed: %d %s", response.Code, response.Body)
	}
	var document map[string]any
	if err := yaml.Unmarshal(gatewayOpenAPIYAML, &document); err != nil {
		t.Fatal(err)
	}
	operation := openAPIOperation(t, document, "/v1/images/generations", "post")
	content := operationRequestContent(t, operation, "image generation")
	media := asMap(t, content["application/json"], "image generation JSON")
	schema := asMap(t, media["schema"], "image generation schema")
	assertExampleMatchesSchema(t, document, schema, payload, "documented Qwen base64 format")
}

func TestMediaImageVendorFormatKeepsManagedValidation(t *testing.T) {
	for _, model := range []string{openAIImageModelName, codexImageModelName} {
		t.Run(model, func(t *testing.T) {
			for _, format := range []string{"url", "b64_json", "base64"} {
				request := imageGenerationRequest{Model: model, Prompt: "A landscape", ResponseFormat: format}
				err := normalizeImageGenerationRequest(&request)
				if format == "base64" {
					if err == nil || AsHTTPError(err).Code != "invalid_response_format" {
						t.Fatalf("managed model accepted vendor format: %v", err)
					}
				} else if err != nil {
					t.Fatalf("managed model rejected %s: %v", format, err)
				}
			}
		})
	}
}
