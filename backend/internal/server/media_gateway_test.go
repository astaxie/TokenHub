package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func newMediaGatewayFixture(t *testing.T, upstream http.HandlerFunc, modality string) (*Server, *GormStore, string) {
	t.Helper()
	remote := httptest.NewServer(upstream)
	t.Cleanup(remote.Close)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Media fixture", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Media key", Allowed: []string{"public-media"}, Status: StatusActive}, "thk_media_fixture")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "media-provider", Name: "Media upstream", Type: ProviderOpenAICompatible, BaseURL: remote.URL + "/v1", APIKey: "upstream-fixture-secret", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "public-media", Modality: modality, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "media-route", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "vendor-media", Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "media-admin", SecretKey: "media-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server, store, secret
}

func TestMediaSpeechPreservesBinaryAndProviderCredentials(t *testing.T) {
	binary := []byte{0, 255, 73, 68, 51, 0, 1}
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Header.Get("Authorization") != "Bearer upstream-fixture-secret" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected upstream request: %s %v", r.URL, r.Header)
		}
		var payload map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &payload)
		if string(payload["model"]) != `"vendor-media"` || string(payload["voice_setting"]) != `{"voice_id":"fixture"}` {
			t.Errorf("fields changed: %s", payload)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Set-Cookie", "upstream-only=secret")
		_, _ = w.Write(binary)
	}, "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"public-media","input":"hello","voice_setting":{"voice_id":"fixture"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Cookie", "client-only=private")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	response := responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
	if response.Code != 200 || response.Header.Get("Content-Type") != "audio/mpeg" || !bytes.Equal([]byte(response.Body), binary) {
		t.Fatalf("speech response: %d %q", response.Code, response.Body)
	}
	if response.Header.Get("Set-Cookie") != "" || response.Header.Get("x-request-id") == "" {
		t.Fatalf("unsafe or missing headers: %v", response.Header)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != 200 {
		t.Fatalf("request not accounted: %+v", logs)
	}
}

func TestMediaImagesPreserveVendorOptionsAndMultipleResults(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &payload)
		for field, want := range map[string]string{"model": `"vendor-media"`, "n": "2", "size": `"2K"`, "image": `["https://example.com/reference.png"]`, "seed": "9007199254740993", "watermark": "false"} {
			if string(payload[field]) != want {
				t.Errorf("%s = %s, want %s", field, payload[field], want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"url":"https://example.com/a.png"},{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10},"vendor_id":9007199254740993}`)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", json.RawMessage(`{"model":"public-media","prompt":"a landscape","n":2,"size":"2K","image":["https://example.com/reference.png"],"seed":9007199254740993,"watermark":false}`), key)
	if response.Code != 200 || !strings.Contains(response.Body, `"vendor_id":9007199254740993`) || !strings.Contains(response.Body, `"b64_json"`) || !strings.Contains(response.Body, `"url"`) {
		t.Fatalf("images response: %d %s", response.Code, response.Body)
	}
	logs := store.ListUsageRecords()
	if len(logs) != 1 || logs[0].TotalTokens != 10 {
		t.Fatalf("usage not recorded: %+v", logs)
	}
}

func TestMediaMultipartRetainsFilesRepeatedFieldsAndTextResponses(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations", "/v1/images/edits", "/v1/images/variations"} {
		t.Run(path, func(t *testing.T) {
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("path = %s", r.URL.Path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				if r.FormValue("model") != "vendor-media" || strings.Join(r.MultipartForm.Value["timestamp_granularities[]"], ",") != "word,segment" {
					t.Errorf("multipart fields: %v", r.MultipartForm.Value)
				}
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				data, _ := io.ReadAll(file)
				if header.Filename != "sample.wav" || !bytes.Equal(data, []byte{0, 1, 2, 255}) {
					t.Errorf("file changed: %s %v", header.Filename, data)
				}
				w.Header().Set("Content-Type", "text/vtt")
				_, _ = io.WriteString(w, "WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n")
			}, "audio")
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for _, field := range [][2]string{{"model", "public-media"}, {"timestamp_granularities[]", "word"}, {"timestamp_granularities[]", "segment"}} {
				if err := writer.WriteField(field[0], field[1]); err != nil {
					t.Fatal(err)
				}
			}
			file, err := writer.CreateFormFile("file", "sample.wav")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte{0, 1, 2, 255}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, path, &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			if recorder.Code != 200 || recorder.Header().Get("Content-Type") != "text/vtt" || !strings.HasPrefix(recorder.Body.String(), "WEBVTT\n") {
				t.Fatalf("multipart response: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestMediaRejectsUnauthorizedOversizedAndInvalidRequests(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(500) }, "audio")
	server.config.MaxMultimodalRequestBytes = 128
	for _, tc := range []struct {
		name, key, body string
		status          int
	}{
		{"authentication", "wrong", `{"model":"public-media"}`, 401},
		{"model permission", key, `{"model":"private-media"}`, 403},
		{"missing model", key, `{}`, 400},
		{"invalid JSON", key, `{"model":`, 400},
		{"trailing JSON", key, `{"model":"public-media"}{}`, 400},
		{"size limit", key, `{"model":"public-media","input":"` + strings.Repeat("a", 200) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tc.key)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected calls reached upstream: %d", calls)
	}
}

func TestMediaPrivacyHookRewritesPromptAndCannotChangeModel(t *testing.T) {
	for _, changeModel := range []bool{false, true} {
		t.Run(map[bool]string{false: "prompt rewrite", true: "model invariant"}[changeModel], func(t *testing.T) {
			called := false
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				called = true
				var payload map[string]any
				decodeFixtureRequest(t, r.Body, &payload)
				if payload["input"] != "masked" {
					t.Errorf("privacy patch missing: %v", payload)
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = w.Write([]byte("audio"))
			}, "audio")
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-media", HookID: "privacy", Stage: pluginmeta.StagePrivacyPre, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				var body map[string]any
				if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &body); err != nil {
					t.Fatal(err)
				}
				body["input"] = "masked"
				if changeModel {
					body["model"] = "private-media"
				}
				return rawRequestBodyPatch(t, body), nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "private prompt"}, key)
			if changeModel {
				if response.Code == 200 || called {
					t.Fatalf("model patch escaped admission: %d %s", response.Code, response.Body)
				}
			} else if response.Code != 200 || !called {
				t.Fatalf("rewrite failed: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestMediaDoesNotResubmitAfterAmbiguousUpstreamFailure(t *testing.T) {
	calls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"error":{"message":"generation outcome unknown"}}`)
	}, "image")
	store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "fixture"}, key)
	if response.Code == 200 || calls != 1 {
		t.Fatalf("ambiguous submission was retried: status=%d calls=%d", response.Code, calls)
	}
}

func TestMediaCatalogPreservesVideoAndMusicModalities(t *testing.T) {
	for input, want := range map[string]string{"video": "video", "audio": "audio", "music": "audio", "image": "image"} {
		if got := normalizeModelModality(input); got != want {
			t.Errorf("normalize %s = %s, want %s", input, got, want)
		}
	}
}

func TestMediaRejectsCrossOriginProviderRedirects(t *testing.T) {
	escaped := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { escaped++; w.WriteHeader(200) }))
	defer other.Close()
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/capture", http.StatusTemporaryRedirect)
	}, "audio")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "fixture"}, key)
	if response.Code == 200 || escaped != 0 {
		t.Fatalf("cross-origin credentials redirect escaped: status=%d calls=%d", response.Code, escaped)
	}
}
