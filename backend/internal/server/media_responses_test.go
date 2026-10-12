package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

// Synthetic examples cover each distinct request shape in the DMXAPI media
// documentation, including submit/query/download and voice asset operations.
func TestMediaResponsesDocumentedRequestShapes(t *testing.T) {
	cases := []struct{ name, fields, response string }{
		{"seedance video submit", `"input":[{"type":"text","text":"a landscape"},{"type":"image_url","image_url":{"url":"https://example.com/frame.png"}}],"duration":4,"ratio":"16:9","generate_audio":true`, `{"id":"task_fixture","usage":{"output_tokens":10,"total_tokens":10}}`},
		{"seedance video query", `"input":"task_fixture"`, `{"output":[{"content":[{"type":"output_text","text":"{\"status\":\"succeeded\",\"content\":{\"video_url\":\"https://example.com/video.mp4\"}}"}]}]}`},
		{"hailuo video query", `"input":{"task_id":"task_fixture"}`, `{"status":"Success","file_id":9007199254740993}`},
		{"hailuo file download", `"input":{"file_id":9007199254740993}`, `{"file":{"file_id":9007199254740993,"download_url":"https://example.com/video.mp4"}}`},
		{"wan image edit", `"input":{"messages":[{"role":"user","content":[{"text":"edit this"},{"image":"https://example.com/input.png"}]}]},"parameters":{"size":"1280*1280","n":4,"watermark":false}`, `{"output":[{"content":[{"type":"image","text":"https://example.com/out.png"}]}]}`},
		{"seedream image generation", `"input":"a landscape","size":"2K","sequential_image_generation":"auto","sequential_image_generation_options":{"max_images":4}`, `{"output":[{"content":[{"type":"image","text":"https://example.com/out.png"}]}]}`},
		{"kling video", `"input":"a landscape","negative_prompt":"blur","cfg_scale":0.5,"mode":"pro","duration":"5"`, `{"id":"task_fixture"}`},
		{"pixverse asset upload", `"input":"aW1hZ2U=","image_url":"https://example.com/input.png"`, `{"image_id":9007199254740993}`},
		{"voice upload", `"input":[{"file":"YXVkaW8=","purpose":"voice_clone"}]`, `{"file":{"file_id":9007199254740993}}`},
		{"voice clone", `"input":[{"file_id":9007199254740993}],"voice_id":"fixture_voice","clone_prompt":{"prompt_text":"sample"}`, `{"voice_id":"fixture_voice"}`},
		{"advanced speech", `"input":"hello","voice_setting":{"voice_id":"fixture","speed":1},"audio_setting":{"format":"mp3"}`, `{"data":{"audio":"010203"}}`},
		{"music and lyrics", `"input":"folk","lyrics":"fixture lyrics","audio_setting":{"format":"mp3"},"output_format":"url"`, `{"data":{"audio":"https://example.com/music.mp3"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				var actual map[string]json.RawMessage
				decodeFixtureRequest(t, r.Body, &actual)
				var expected map[string]json.RawMessage
				if err := json.Unmarshal([]byte(`{`+tc.fields+`}`), &expected); err != nil {
					t.Fatal(err)
				}
				for field, want := range expected {
					var wantValue, actualValue any
					if err := decodeResponsesJSON(want, &wantValue); err != nil {
						t.Fatal(err)
					}
					if err := decodeResponsesJSON(actual[field], &actualValue); err != nil {
						t.Fatal(err)
					}
					wantJSON, _ := json.Marshal(wantValue)
					actualJSON, _ := json.Marshal(actualValue)
					if string(wantJSON) != string(actualJSON) {
						t.Errorf("%s = %s, want %s", field, actualJSON, wantJSON)
					}
				}
				if string(actual["model"]) != `"vendor-media"` {
					t.Errorf("model mapping: %s", actual["model"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}, "video")
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media",`+tc.fields+`}`), key)
			if response.Code != 200 {
				t.Fatalf("media responses: %d %s", response.Code, response.Body)
			}
			var expected, actual any
			_ = decodeResponsesJSON([]byte(tc.response), &expected)
			_ = decodeResponsesJSON([]byte(response.Body), &actual)
			wantJSON, _ := json.Marshal(expected)
			gotJSON, _ := json.Marshal(actual)
			if string(wantJSON) != string(gotJSON) {
				t.Fatalf("response changed: %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestMediaResponsesBypassTaskCaches(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fresh-task"}`)
	}, "video")
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageCacheLookup, pluginmeta.StageCacheWrite} {
		hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-media-cache", HookID: string(stage), Stage: stage, Priority: 1000, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatal(err)
		}
		if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			t.Error("media task reached a response cache")
			return pluginmeta.GatewayHookResult{}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "task_fixture"}, key)
		if response.Code != 200 {
			t.Fatalf("request: %d %s", response.Code, response.Body)
		}
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestMediaChatPreservesSpeechAndCaptionFields(t *testing.T) {
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &body)
		for _, field := range []string{"messages", "audio", "modalities"} {
			if len(body[field]) == 0 {
				t.Errorf("missing media field %s", field)
			}
		}
		if !strings.Contains(string(body["messages"]), "input_audio") {
			t.Errorf("audio input lost: %s", body["messages"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","audio":{"data":"YXVkaW8=","format":"mp3"}}}],"usage":{"total_tokens":4,"completion_tokens":4}}`)
	}, "audio")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", json.RawMessage(`{"model":"public-media","messages":[{"role":"user","content":[{"type":"text","text":"Speak this"},{"type":"input_audio","input_audio":{"data":"YXVkaW8=","format":"wav"}}]}],"modalities":["audio"],"audio":{"voice":"fixture","format":"mp3"}}`), key)
	if response.Code != 200 || !strings.Contains(response.Body, `"data":"YXVkaW8="`) {
		t.Fatalf("chat audio: %d %s", response.Code, response.Body)
	}
}

func TestMediaResponsesStreamPreservesAudioEvents(t *testing.T) {
	stream := "event: response.audio.delta\ndata: {\"type\":\"response.audio.delta\",\"delta\":\"YXVkaW8=\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_media\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":4,\"total_tokens\":6}}}\n\n"
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}, "audio")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "a song", "lyrics": "fixture lyrics", "stream": true}, key)
	if response.Code != 200 || !strings.Contains(response.Body, `"delta":"YXVkaW8="`) || !strings.Contains(response.Body, "response.completed") {
		t.Fatalf("audio stream: %d %s", response.Code, response.Body)
	}
	usage := store.ListUsageRecords()
	if len(usage) != 1 || usage[0].TotalTokens != 6 {
		t.Fatalf("stream usage: %+v", usage)
	}
}
