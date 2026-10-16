package server

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

func writeTranscriptionFixtureFile(t *testing.T, writer *multipart.Writer, name, filename, contentType string, data []byte) {
	t.Helper()
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": name, "filename": filename}))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	file, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
}

func transcriptionFixtureRequest(t *testing.T, key string, fields [][2]string, files []mediaFile) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreatePart(file.Header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func transcriptionFixtureFile(name, filename, contentType string, data []byte) mediaFile {
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": name, "filename": filename}))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return mediaFile{Header: header, Data: data}
}

func transcriptionWAVFixture(payload string) []byte {
	return append([]byte("RIFF\x04\x00\x00\x00WAVE"), []byte(payload)...)
}

func TestTranscriptionMultipartRejectsInvalidAudioBeforeUpstream(t *testing.T) {
	valid := transcriptionFixtureFile("file", "sample.wav", "audio/wav", transcriptionWAVFixture("fixture audio"))
	for _, tc := range []struct {
		name   string
		fields [][2]string
		files  []mediaFile
		status int
		code   string
	}{
		{name: "missing file", status: http.StatusBadRequest, code: "missing_audio_file"},
		{name: "wrong field", files: []mediaFile{transcriptionFixtureFile("audio", "sample.wav", "audio/wav", valid.Data)}, status: http.StatusBadRequest, code: "missing_audio_file"},
		{name: "text file", fields: [][2]string{{"file", "sample.wav"}}, status: http.StatusBadRequest, code: "invalid_audio_file"},
		{name: "text and uploaded file", fields: [][2]string{{"file", "sample.wav"}}, files: []mediaFile{valid}, status: http.StatusBadRequest, code: "invalid_audio_file"},
		{name: "empty file", files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", nil)}, status: http.StatusBadRequest, code: "missing_audio_file"},
		{name: "multiple files", files: []mediaFile{valid, valid}, status: http.StatusBadRequest, code: "invalid_audio_file"},
		{name: "extra file", files: []mediaFile{valid, transcriptionFixtureFile("other", "extra.wav", "audio/wav", valid.Data)}, status: http.StatusBadRequest, code: "invalid_audio_file"},
		{name: "mislabeled bytes", files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", []byte("not an audio container"))}, status: http.StatusUnsupportedMediaType, code: "unsupported_audio_format"},
		{name: "different audio container", files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", []byte("fLaCfixture"))}, status: http.StatusUnsupportedMediaType, code: "unsupported_audio_format"},
		{name: "unsupported extension", files: []mediaFile{transcriptionFixtureFile("file", "sample.exe", "audio/wav", valid.Data)}, status: http.StatusUnsupportedMediaType, code: "unsupported_audio_format"},
		{name: "unsupported content type", files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "text/plain", valid.Data)}, status: http.StatusUnsupportedMediaType, code: "unsupported_audio_format"},
		{name: "invalid content type", files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "invalid", valid.Data)}, status: http.StatusUnsupportedMediaType, code: "unsupported_audio_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
			fields := append([][2]string{{"model", "public-media"}}, tc.fields...)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, transcriptionFixtureRequest(t, key, fields, tc.files))
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) || calls != 0 {
				t.Fatalf("invalid audio escaped validation: status=%d upstream=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestTranscriptionMultipartAudioLimits(t *testing.T) {
	data := make([]byte, maxTranscriptionAudioBytes+1)
	copy(data, transcriptionWAVFixture(""))
	request := mediaRequest{Multipart: true, Path: "/v1/audio/transcriptions", Files: []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", data)}}
	if err := request.validateAudioFiles(); err == nil || AsHTTPError(err).Code != "audio_file_too_large" || AsHTTPError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized audio accepted: %v", err)
	}
	request.Files[0].Data = data[:maxTranscriptionAudioBytes]
	if err := request.validateAudioFiles(); err != nil {
		t.Fatalf("audio at size limit rejected: %v", err)
	}
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
	server.config.MaxMultimodalRequestBytes = 1024
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, transcriptionFixtureRequest(t, key, [][2]string{{"model", "public-media"}}, []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", data[:2048])}))
	if response.Code != http.StatusRequestEntityTooLarge || calls != 0 || !strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatalf("configured multipart limit bypassed: status=%d upstream=%d body=%s", response.Code, calls, response.Body)
	}
}

func TestTranscriptionMultipartPreservesOptionsAndKeepsPayloadsPrivate(t *testing.T) {
	const transcript = `{"text":"private transcript fixture","language":"en","duration":1.25,"segments":[{"start":0,"end":1.25,"text":"private transcript fixture"}]}`
	audio := transcriptionWAVFixture("private audio fixture content")
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		for name, want := range map[string]string{"model": "vendor-media", "language": "en", "prompt": "private prompt fixture", "response_format": "verbose_json", "temperature": "0.2"} {
			if got := r.FormValue(name); got != want {
				t.Errorf("field %s = %q, want %q", name, got, want)
			}
		}
		if got := strings.Join(r.MultipartForm.Value["timestamp_granularities[]"], ","); got != "word,segment" {
			t.Errorf("timestamp options changed: %s", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil || header.Filename != "private-recording.wav" || !bytes.Equal(data, audio) {
			t.Errorf("uploaded audio changed: filename=%s data=%q error=%v", header.Filename, data, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, transcript)
	}, "audio")
	fields := [][2]string{{"model", "public-media"}, {"language", "en"}, {"prompt", "private prompt fixture"}, {"response_format", "verbose_json"}, {"temperature", "0.2"}, {"timestamp_granularities[]", "word"}, {"timestamp_granularities[]", "segment"}}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, transcriptionFixtureRequest(t, key, fields, []mediaFile{transcriptionFixtureFile("file", "private-recording.wav", "audio/wav", audio)}))
	if response.Code != http.StatusOK || response.Body.String() != transcript || response.Header().Get("x-request-id") == "" {
		t.Fatalf("transcription response changed: status=%d body=%s", response.Code, response.Body)
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("payload audits=%d, want 1", len(payloads))
	}
	for _, secret := range []string{"private audio fixture content", "private-recording.wav", "private transcript fixture", "private prompt fixture", "upstream-fixture-secret", key} {
		if strings.Contains(payloads[0].RequestBody+payloads[0].ResponseBody, secret) {
			t.Errorf("private transcription data persisted in payload audit: %q", secret)
		}
	}
}

func TestTranscriptionMultipartRecognizesSupportedContainerHeaders(t *testing.T) {
	for _, tc := range []struct {
		filename, contentType string
		data                  []byte
	}{
		{"sample.WAV", "audio/wav", transcriptionWAVFixture("")},
		{"sample.flac", "audio/flac", []byte("fLaC")},
		{"sample.ogg", "audio/ogg", []byte("OggS")},
		{"sample.mp3", "audio/mpeg", []byte("ID3")},
		{"sample.mpga", "application/octet-stream", []byte{0xff, 0xfb}},
		{"sample.mpeg", "video/mpeg", []byte{0, 0, 1, 0xba}},
		{"sample.mp4", "video/mp4", []byte("\x00\x00\x00\x14ftypisom\x00\x00\x00\x00isom")},
		{"sample.m4a", "audio/mp4", []byte("\x00\x00\x00\x14ftypM4A \x00\x00\x00\x00M4A ")},
		{"sample.webm", "video/webm", []byte{0x1a, 0x45, 0xdf, 0xa3}},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			request := mediaRequest{Multipart: true, Path: "/v1/audio/transcriptions", Files: []mediaFile{transcriptionFixtureFile("file", tc.filename, tc.contentType, tc.data)}}
			if err := request.validateAudioFiles(); err != nil {
				t.Fatalf("supported container rejected: %v", err)
			}
		})
	}
}
