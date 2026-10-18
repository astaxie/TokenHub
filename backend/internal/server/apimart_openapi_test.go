package server

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Keep the executable public contract aligned with the provider's documented
// submission envelopes, reference media fields, and task status responses.
func TestAPIMartMediaOpenAPIContract(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, tc := range []struct {
		name, path, modality, request string
	}{
		{
			name: "video references and exact seed",
			path: "/v1/videos/generations", modality: "video",
			request: `{"model":"public-media","prompt":"A calm ocean at sunrise","duration":5,"size":"16:9","resolution":"720p","generate_audio":true,"seed":9007199254740993,"image_urls":["https://example.com/reference.png"],"video_urls":["https://example.com/reference.mp4"],"audio_urls":["https://example.com/reference.wav"]}`,
		},
		{
			name: "first and last frame video",
			path: "/v1/videos/generations", modality: "video",
			request: `{"model":"public-media","prompt":"A smooth transition from day to night","duration":5,"image_with_roles":[{"url":"https://example.com/day.png","role":"first_frame"},{"url":"https://example.com/night.png","role":"last_frame"}]}`,
		},
		{
			name: "asynchronous image references",
			path: "/v1/images/generations", modality: "image",
			request: `{"model":"public-media","prompt":"An ocean illustration","size":"16:9","resolution":"2K","n":1,"image_urls":["https://example.com/reference.png"]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var expected map[string]any
			if err := decodeResponsesJSON([]byte(tc.request), &expected); err != nil {
				t.Fatal(err)
			}
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					if r.URL.Path != tc.path {
						t.Errorf("submission path = %s, want %s", r.URL.Path, tc.path)
					}
					var actual map[string]any
					decoder := json.NewDecoder(r.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&actual); err != nil {
						t.Fatal(err)
					}
					expected["model"] = "vendor-media"
					if !reflect.DeepEqual(actual, expected) {
						t.Errorf("provider fields changed: got %#v, want %#v", actual, expected)
					}
					_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_task_fixture"}],"provider_extension":9007199254740993}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/v1/tasks/vendor_task_fixture" || r.URL.Query().Get("language") != "ja" {
					t.Errorf("unexpected query: %s %s", r.Method, r.URL)
				}
				_, _ = io.WriteString(w, `{"code":200,"data":{"id":"vendor_task_fixture","status":"completed","progress":100,"result":{"videos":[{"url":["https://example.com/result.mp4"]}]},"provider_extension":9007199254740993}}`)
			}, tc.modality)
			submitted := doJSON(t, server.Handler(), http.MethodPost, tc.path, json.RawMessage(tc.request), key)
			if submitted.Code != http.StatusOK {
				t.Fatalf("submission failed: %d %s", submitted.Code, submitted.Body)
			}
			assertMediaOperationSchema(t, document, tc.path, "post", "", tc.request)
			assertMediaOperationSchema(t, document, tc.path, "post", "200", submitted.Body)
			var envelope struct {
				Data []struct {
					TaskID string `json:"task_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(submitted.Body), &envelope); err != nil || len(envelope.Data) != 1 {
				t.Fatalf("invalid submission envelope: %s (%v)", submitted.Body, err)
			}
			id := envelope.Data[0].TaskID
			if id == "" || id == "vendor_task_fixture" {
				t.Fatalf("submission did not return an isolated task ID: %q", id)
			}
			query := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id+"?language=ja", nil, key)
			if query.Code != http.StatusOK {
				t.Fatalf("query failed: %d %s", query.Code, query.Body)
			}
			assertMediaOperationSchema(t, document, "/v1/tasks/{task_id}", "get", "200", query.Body)
			for _, response := range []string{submitted.Body, query.Body} {
				if strings.Contains(response, "vendor_task_fixture") || !strings.Contains(response, "9007199254740993") {
					t.Errorf("task identity leaked or provider precision lost: %s", response)
				}
			}
		})
	}
}

func TestAPIMartImageTaskEnvelopeSchemas(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/images/variations"} {
		for _, envelope := range []string{
			`{"code":200,"data":[{"status":"submitted","task_id":"task_example"}]}`,
			`{"code":200,"data":{"status":"submitted","task_id":"task_example"}}`,
			`{"task_id":"task_example","status":"submitted"}`,
		} {
			assertMediaOperationSchema(t, document, path, "post", "200", envelope)
		}
	}
}

func TestAPIMartVersionedTaskEnvelopeSchemas(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/images/variations", "/v1/videos/generations"} {
		for _, status := range []string{"200", "202"} {
			assertMediaOperationSchema(t, document, path, "post", status, `{"code":202,"request_id":"provider-request","data":{"id":"task_example","object":"generation.task","type":"image","status":"pending","progress":0,"poll_url":"/v1/tasks/task_example"}}`)
		}
	}
}
