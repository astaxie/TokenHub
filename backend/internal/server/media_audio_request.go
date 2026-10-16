package server

import (
	"bytes"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

const maxTranscriptionAudioBytes = 25 << 20

// Validate the OpenAI multipart upload contract without restricting compatible
// providers' existing JSON transcription extensions or unrelated media files.
func (m mediaRequest) validateAudioFiles() error {
	if !m.Multipart || mediaCapabilityForProtocol(strings.TrimPrefix(m.Path, "/v1/")) != AdapterCapabilityTranscription {
		return nil
	}
	if _, present := m.Fields["file"]; present {
		return NewHTTPError(http.StatusBadRequest, "invalid_audio_file", "file must be a single audio file upload")
	}
	if len(m.Files) > 1 {
		return NewHTTPError(http.StatusBadRequest, "invalid_audio_file", "file must contain exactly one audio file")
	}
	var audio *mediaFile
	var filename string
	for index := range m.Files {
		file := &m.Files[index]
		_, disposition, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
		if err != nil || disposition["name"] != "file" {
			continue
		}
		audio = file
		filename = disposition["filename"]
		if !supportedTranscriptionAudioFile(disposition["filename"], file.Header.Get("Content-Type")) {
			return NewHTTPError(http.StatusUnsupportedMediaType, "unsupported_audio_format", "Audio files must use FLAC, MP3, MP4, MPEG, MPGA, M4A, OGG, WAV, or WebM format")
		}
	}
	if audio == nil || len(audio.Data) == 0 {
		return NewHTTPError(http.StatusBadRequest, "missing_audio_file", "A non-empty audio file is required")
	}
	if len(audio.Data) > maxTranscriptionAudioBytes {
		return NewHTTPError(http.StatusRequestEntityTooLarge, "audio_file_too_large", "Audio file exceeds the 25 MiB upload limit")
	}
	if !transcriptionAudioSignatureMatches(filename, audio.Data) {
		return NewHTTPError(http.StatusUnsupportedMediaType, "unsupported_audio_format", "Audio file content does not match its supported format")
	}
	return nil
}

func supportedTranscriptionAudioFile(filename, contentType string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".flac", ".mp3", ".mp4", ".mpeg", ".mpga", ".m4a", ".ogg", ".wav", ".webm":
	default:
		return false
	}
	// Standard multipart clients send application/octet-stream for local files.
	// Validate an explicit media type before checking the bounded container header.
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mediaType {
	case "application/octet-stream", "audio/flac", "audio/x-flac", "audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/ogg", "application/ogg", "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave", "audio/webm", "video/mp4", "video/mpeg", "video/webm":
		return true
	default:
		return false
	}
}

// Recognize the container without decoding audio, allocating another copy, or
// trusting a client-provided filename and MIME type alone. The provider remains
// responsible for codec support and full container integrity validation.
func transcriptionAudioSignatureMatches(filename string, data []byte) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".wav":
		return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	case ".flac":
		return bytes.HasPrefix(data, []byte("fLaC"))
	case ".ogg":
		return bytes.HasPrefix(data, []byte("OggS"))
	case ".mp3", ".mpga":
		return bytes.HasPrefix(data, []byte("ID3")) || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
	case ".mpeg":
		return bytes.HasPrefix(data, []byte{0, 0, 1, 0xba}) || bytes.HasPrefix(data, []byte{0, 0, 1, 0xb3}) || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
	case ".mp4", ".m4a":
		return len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp"))
	case ".webm":
		return bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
	default:
		return false
	}
}
