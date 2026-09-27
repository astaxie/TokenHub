package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMediaBackgroundResponsesPreserveVendorIDs(t *testing.T) {
	for _, tc := range []struct{ name, modality, vendorID, wantHeader string }{
		{"string video task", "video", `"vendor_task_fixture"`, "vendor_task_fixture"},
		{"numeric audio task", "audio", `9007199254740993`, "9007199254740993"},
		{"text model unchanged", "chat", `"vendor_text_response"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkBackgroundVendorIDs(t, tc.modality, tc.vendorID, tc.wantHeader)
		})
	}
}

func checkBackgroundVendorIDs(t *testing.T, modality, vendorID, wantHeader string) {
	t.Helper()
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &request)
		if string(request["input"]) != `[{"file_id":9007199254740993}]` {
			t.Errorf("background file input changed: %s", request["input"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":`+vendorID+`,"file":{"file_id":9007199254740993,"download_url":"https://example.com/media.mp4"},"metadata":{"tokenhub_upstream_response_id":"vendor_metadata"},"upstream_id":"vendor_field"}`)
	}, modality)
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media","input":[{"file_id":9007199254740993}],"background":true}`), key)
	if response.Code != http.StatusOK {
		t.Fatalf("background media submission: %d %s", response.Code, response.Body)
	}
	var submitted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
		t.Fatalf("invalid background submission: %s (error: %v)", response.Body, err)
	}
	waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, "completed")
	request := httptest.NewRequest(http.MethodGet, "/v1/responses/"+submitted.ID, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	response = responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"file_id":9007199254740993`) {
		t.Fatalf("background media result lost file ID precision: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"`+submitted.ID+`"`) {
		t.Fatalf("background result changed the gateway-owned job ID: %s", response.Body)
	}
	if got := response.Header.Get("x-tokenhub-upstream-response-id"); got != wantHeader {
		t.Errorf("upstream response ID = %q, want %q", got, wantHeader)
	}
	if wantHeader != "" && !strings.Contains(response.Header.Get("Access-Control-Expose-Headers"), "x-tokenhub-upstream-response-id") {
		t.Error("browser clients cannot read the upstream response ID header")
	}
	for _, preserved := range []string{`"tokenhub_upstream_response_id":"vendor_metadata"`, `"upstream_id":"vendor_field"`} {
		if !strings.Contains(response.Body, preserved) {
			t.Errorf("vendor field lost: missing %s in %s", preserved, response.Body)
		}
	}
}

func TestMediaBackgroundResponseIDRejectsUnsafeHeaderValues(t *testing.T) {
	for _, id := range []any{nil, true, map[string]any{"id": "nested"}, []string{"task"}, "task\r\nInjected: value", "task\x00", "task\t", "task\x7f", "task\u0085", " task ", strings.Repeat("a", 2049)} {
		data, err := json.Marshal(map[string]any{"id": id})
		if err != nil {
			t.Fatal(err)
		}
		if got := mediaBackgroundResponseID(data); got != "" {
			t.Errorf("unsafe or unsupported ID %q produced header %q", data, got)
		}
	}
}
