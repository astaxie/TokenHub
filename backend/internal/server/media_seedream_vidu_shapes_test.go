package server

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestMediaSeedreamAndViduShapesMatchSynchronousAndBackgroundSchemas(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, tc := range []struct{ name, modality, request, response string }{
		{
			"seedream image URL output", "image",
			`{"model":"public-media","input":"Create two landscape variations","image":"https://example.com/reference.png","sequential_image_generation":"auto"}`,
			`{"id":"vendor_seedream_fixture","status":"completed","output":[{"type":"image_url","image_url":{"url":"https://example.com/first.png","image_id":9007199254740993}},{"type":"image_url","image_url":{"url":"https://example.com/second.png"}}],"usage":{"input_tokens":2,"output_tokens":15,"total_tokens":17}}`,
		},
		{
			"vidu start and end frame input", "video",
			`{"model":"public-media","input":["https://example.com/start.png","https://example.com/end.png"],"prompt":"A bird flying between two trees","duration":10,"audio":true}`,
			`{"task_id":"vendor_vidu_fixture","state":"created","seed":9007199254740993,"usage":{"input_tokens":2,"output_tokens":15,"total_tokens":17}}`,
		},
	} {
		for _, background := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "synchronous", true: "background"}[background], func(t *testing.T) {
				calls := 0
				var request map[string]any
				if err := decodeResponsesJSON([]byte(tc.request), &request); err != nil {
					t.Fatal(err)
				}
				request["background"] = background
				encoded, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				assertMediaOperationSchema(t, document, "/v1/responses", "post", "", string(encoded))
				server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					var actual map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&actual); err != nil {
						t.Fatal(err)
					}
					for name, want := range request {
						if name == "background" {
							continue
						}
						if name == "model" {
							want = "vendor-media"
						}
						if !reflect.DeepEqual(actual[name], want) {
							t.Errorf("request field %s changed: got %v, want %v", name, actual[name], want)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.response)
				}, tc.modality)
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(encoded), key)
				if response.Code != http.StatusOK {
					t.Fatalf("media submission failed: %d %s", response.Code, response.Body)
				}
				assertMediaOperationSchema(t, document, "/v1/responses", "post", "200", response.Body)
				if background {
					var submitted struct{ ID string }
					if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
						t.Fatalf("invalid background job: %s (error: %v)", response.Body, err)
					}
					waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, "completed")
					response = doJSON(t, server.Handler(), http.MethodGet, "/v1/responses/"+submitted.ID, nil, key)
					if response.Code != http.StatusOK {
						t.Fatalf("media polling failed: %d %s", response.Code, response.Body)
					}
					assertMediaOperationSchema(t, document, "/v1/responses/{response_id}", "get", "200", response.Body)
				}
				var got, want map[string]any
				if err := decodeResponsesJSON([]byte(response.Body), &got); err != nil {
					t.Fatal(err)
				}
				if err := decodeResponsesJSON([]byte(tc.response), &want); err != nil {
					t.Fatal(err)
				}
				for name, value := range want {
					if background && (name == "id" || name == "status") {
						continue
					}
					if !reflect.DeepEqual(got[name], value) {
						t.Errorf("response field %s changed: got %v, want %v", name, got[name], value)
					}
				}
				if calls != 1 {
					t.Errorf("upstream calls = %d, want 1", calls)
				}
			})
		}
	}
}
