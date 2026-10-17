package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func transcriptionEpilogueFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "public-media"); err != nil {
		t.Fatal(err)
	}
	writeTranscriptionFixtureFile(t, writer, "file", "sample.wav", "audio/wav", transcriptionWAVFixture("fixture audio"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func TestTranscriptionMultipartTotalLimitIncludesEpilogue(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, length := range []string{"known", "unknown"} {
			for _, tc := range []struct {
				name  string
				limit int
				extra int
			}{
				{"prefetched exact limit", 1024, 0},
				{"prefetched excess", 1024, 1},
				{"unread exact limit", 8192, 0},
				{"unread excess", 8192, 1},
			} {
				t.Run(path+"/"+length+"/"+tc.name, func(t *testing.T) {
					calls := 0
					server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
						calls++
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"text":"Synthetic transcript"}`)
					}, "audio")
					server.config.MaxMultimodalRequestBytes = int64(tc.limit)
					body, contentType := transcriptionEpilogueFixture(t)
					body = append(body, bytes.Repeat([]byte("x"), tc.limit+tc.extra-len(body))...)
					request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					if length == "unknown" {
						request.ContentLength = -1
						request.TransferEncoding = []string{"chunked"}
					}
					request.Header.Set("Authorization", "Bearer "+key)
					request.Header.Set("Content-Type", contentType)
					response := httptest.NewRecorder()
					server.Handler().ServeHTTP(response, request)
					wantStatus, wantCalls := http.StatusOK, 1
					if tc.extra > 0 {
						wantStatus, wantCalls = http.StatusRequestEntityTooLarge, 0
					}
					if response.Code != wantStatus || calls != wantCalls || tc.extra > 0 && !strings.Contains(response.Body.String(), `"code":"request_too_large"`) {
						t.Fatalf("epilogue limit: bytes=%d limit=%d status=%d upstream=%d body=%s", len(body), tc.limit, response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}

func TestTranscriptionMultipartEpilogueCheckPreservesDecodeErrors(t *testing.T) {
	valid, contentType := transcriptionEpilogueFixture(t)
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"malformed", []byte("invalid multipart framing")},
		{"truncated", valid[:len(valid)-8]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || calls != 0 || !strings.Contains(response.Body.String(), `"code":"invalid_media_request"`) {
				t.Fatalf("decode error changed: status=%d upstream=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestNonTranscriptionMultipartEpilogueBehaviorIsUnchanged(t *testing.T) {
	body, contentType := transcriptionEpilogueFixture(t)
	body = append(body, bytes.Repeat([]byte("x"), 8192)...)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/variations", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if _, err := decodeMediaRequest(httptest.NewRecorder(), request, 1024); err != nil {
		t.Fatalf("non-transcription multipart behavior changed: %v", err)
	}
}
