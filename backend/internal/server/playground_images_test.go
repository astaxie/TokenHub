package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func playgroundImagePart(dataURI string) map[string]any {
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": dataURI},
	}
}

func TestValidatePlaygroundImagesAcceptsSupportedDataURIs(t *testing.T) {
	message := ChatMessage{Role: "user", Content: []any{
		map[string]any{"type": "text", "text": "describe"},
		playgroundImagePart("data:image/png;base64,YWJj"),
		playgroundImagePart("https://example.com/campus.jpg"),
	}}
	if err := validatePlaygroundImages([]ChatMessage{message}); err != nil {
		t.Fatalf("expected valid images, got %v", err)
	}
}

func TestValidatePlaygroundImagesEnforcesTypeCountAndEncoding(t *testing.T) {
	tests := []struct {
		name    string
		content []any
		code    string
	}{
		{name: "type", content: []any{playgroundImagePart("data:image/gif;base64,YWJj")}, code: "unsupported_image_type"},
		{name: "base64", content: []any{playgroundImagePart("data:image/png;base64,not-valid")}, code: "invalid_image"},
		{name: "count", content: []any{
			playgroundImagePart("https://example.com/1.jpg"), playgroundImagePart("https://example.com/2.jpg"),
			playgroundImagePart("https://example.com/3.jpg"), playgroundImagePart("https://example.com/4.jpg"),
			playgroundImagePart("https://example.com/5.jpg"),
		}, code: "too_many_images"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePlaygroundImages([]ChatMessage{{Role: "user", Content: test.content}})
			if got := AsHTTPError(err).Code; got != test.code {
				t.Fatalf("error code = %q, want %q: %v", got, test.code, err)
			}
		})
	}
}

func TestPlaygroundAuditRequestRedactsImageData(t *testing.T) {
	dataURI := " \tdata:image/png;base64,YWJj\n"
	req := ChatCompletionRequest{Model: "qwen3-vl", Messages: []ChatMessage{{
		Role: "user", Content: []any{playgroundImagePart(dataURI)},
	}}}
	if err := validatePlaygroundImages(req.Messages); err != nil {
		t.Fatalf("expected normalized data URI to pass validation, got %v", err)
	}
	raw, err := json.Marshal(playgroundAuditRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "YWJj") || !strings.Contains(text, "image data redacted") {
		t.Fatalf("image payload was not redacted: %s", text)
	}
}
