package server

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func TestMediaDocumentedShapesMatchOpenAPIAndRuntime(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, tc := range []struct{ name, path, request, response string }{
		{"lyrics without input", "/v1/responses", `{"model":"public-media","mode":"write_full_song"}`, `{"song_title":"A fixture song","lyrics":"A quiet morning"}`},
		{"music without input", "/v1/responses", `{"model":"public-media","lyrics":"A quiet morning","output_format":"url"}`, `{"data":{"audio":"https://example.com/music.mp3","status":2}}`},
		{"wan image", "/v1/responses", `{"model":"public-media","input":{"messages":[{"role":"user","content":[{"text":"A landscape"}]}]}}`, `{"output":[{"type":"message","content":[{"type":"image","text":"https://example.com/image.png"}]}]}`},
		{"minimax voice upload", "/v1/responses", `{"model":"public-media","input":[{"purpose":"prompt_audio"}],"dataf":"YXVkaW8="}`, `{"file":{"file_id":9007199254740993,"purpose":"prompt_audio"},"base_resp":{"status_code":0}}`},
		{"seedance video", "/v1/responses", `{"model":"public-media","input":[{"type":"text","text":"A landscape"},{"type":"image_url","image_url":{"url":"https://example.com/frame.png"}}]}`, `{"id":"vendor_task_fixture"}`},
		{"hailuo task query", "/v1/responses", `{"model":"public-media","input":{"task_id":"vendor_task_fixture"}}`, `{"status":"Success","file_id":9007199254740993}`},
		{"hailuo file query", "/v1/responses", `{"model":"public-media","input":{"file_id":9007199254740993}}`, `{"file":{"file_id":9007199254740993,"download_url":"https://example.com/video.mp4"}}`},
		{"numeric media task ID", "/v1/responses", `{"model":"public-media","input":"A landscape"}`, `{"id":9007199254740993}`},
		{"minimax music", "/v1/responses", `{"model":"public-media","input":"Folk music","lyrics":"A quiet morning"}`, `{"output":[{"type":"message","content":[{"type":"output_audio","audio":"https://example.com/music.mp3"}]}]}`},
		{"scidraw image", "/v1/responses", `{"model":"public-media","input":"vendor_task_fixture"}`, `{"output":[{"type":"image_generation_call","result":"https://example.com/image.png"}],"success":true}`},
		{"standard response", "/v1/responses", `{"model":"public-media","input":[{"type":"message","role":"user","content":"A landscape"}]}`, `{"id":"resp_fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A landscape"}]}]}`},
		{"qwen audio URL", "/v1/chat/completions", `{"model":"public-media","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"https://example.com/audio.wav"}}]}]}`, `{"choices":[{"index":0,"message":{"role":"assistant","content":"Audio caption"}}]}`},
		{"qwen audio data URL", "/v1/chat/completions", `{"model":"public-media","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"data:audio/wav;base64,YXVkaW8="}}]}]}`, `{"choices":[{"index":0,"message":{"role":"assistant","content":"Audio caption"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var expected map[string]any
			if err := decodeResponsesJSON([]byte(tc.request), &expected); err != nil {
				t.Fatal(err)
			}
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				var actual map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&actual); err != nil {
					t.Fatal(err)
				}
				if _, present := expected["input"]; tc.path == "/v1/responses" && !present {
					if _, introduced := actual["input"]; introduced {
						t.Errorf("omitted input was introduced: %v", actual["input"])
					}
				}
				for name, want := range expected {
					if name == "model" {
						want = "vendor-media"
					}
					if !reflect.DeepEqual(actual[name], want) {
						t.Errorf("request field %s changed: got %v, want %v", name, actual[name], want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}, "audio")
			response := doJSON(t, server.Handler(), http.MethodPost, tc.path, json.RawMessage(tc.request), key)
			if response.Code != http.StatusOK {
				t.Fatalf("media request failed: %d %s", response.Code, response.Body)
			}
			var want, got any
			if err := decodeResponsesJSON([]byte(tc.response), &want); err != nil {
				t.Fatal(err)
			}
			if err := decodeResponsesJSON([]byte(response.Body), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("provider response changed: got %v, want %v", got, want)
			}
			assertMediaOperationSchema(t, document, tc.path, "post", "", tc.request)
			assertMediaOperationSchema(t, document, tc.path, "post", "200", response.Body)
		})
	}
}

func TestMediaBackgroundOutputMatchesOpenAPI(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":9007199254740993,"output":[{"type":"message","content":[{"type":"image","text":"https://example.com/image.png"}]}]}`)
	}, "image")
	request := `{"model":"public-media","input":{"messages":[{"role":"user","content":[{"text":"A landscape"}]}]},"background":true}`
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(request), key)
	if response.Code != http.StatusOK {
		t.Fatalf("background request failed: %d %s", response.Code, response.Body)
	}
	assertMediaOperationSchema(t, document, "/v1/responses", "post", "", request)
	assertMediaOperationSchema(t, document, "/v1/responses", "post", "200", response.Body)
	var submitted struct{ ID string }
	if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
		t.Fatalf("invalid background job: %s (error: %v)", response.Body, err)
	}
	waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, "completed")
	response = doJSON(t, server.Handler(), http.MethodGet, "/v1/responses/"+submitted.ID, nil, key)
	if response.Code != http.StatusOK {
		t.Fatalf("background polling failed: %d %s", response.Code, response.Body)
	}
	assertMediaOperationSchema(t, document, "/v1/responses/{response_id}", "get", "200", response.Body)
}

func TestMediaOpenAPIPreservesStandardItemValidation(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, tc := range []struct{ schema, value string }{
		{"ResponsesRequest", `{"input":"Missing model"}`},
		{"ResponsesRequest", `{"model":"public-media","input":true}`},
		{"ResponsesRequest", `{"model":"public-media","input":[{"type":"message","content":"Missing role"}]}`},
		{"ResponseInputItem", `{"type":"message","content":"Missing role"}`},
		{"ResponseInputItem", `{"type":"function_call","name":"Missing call ID"}`},
		{"ResponseOutputItem", `{"type":"message","role":"user","content":[{"type":"output_text","text":"Wrong role"}]}`},
		{"ChatContentPart", `{"type":"input_audio","input_audio":{}}`},
		{"ResponseJob", `{"id":9007199254740993,"status":"completed"}`},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			components := asMap(t, document["components"], "components")
			schemas := asMap(t, components["schemas"], "schemas")
			resource := map[string]any{"$ref": "#/$defs/" + tc.schema, "$defs": rewriteSchemaRefs(jsonRoundTrip(t, schemas))}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource("https://tokenhub.local/media-negative.json", resource); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile("https://tokenhub.local/media-negative.json")
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(value); err == nil {
				t.Fatalf("invalid %s value accepted: %s", tc.schema, tc.value)
			}
		})
	}
}

func mediaOpenAPIDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(gatewayOpenAPIYAML, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func assertMediaOperationSchema(t *testing.T, document map[string]any, path, method, status, raw string) {
	t.Helper()
	operation := openAPIOperation(t, document, path, method)
	var content map[string]any
	if status == "" {
		content = operationRequestContent(t, operation, path)
	} else {
		responses := asMap(t, operation["responses"], path)
		response := asMap(t, responses[status], status)
		content = asMap(t, response["content"], path)
	}
	media := asMap(t, content["application/json"], path)
	schema := asMap(t, media["schema"], path)
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	assertExampleMatchesSchema(t, document, schema, value, method+" "+path+" "+status)
}
