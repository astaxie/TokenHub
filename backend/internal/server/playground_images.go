package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	maxPlaygroundImagesPerMessage       = 4
	maxPlaygroundImageBytes             = 5 << 20
	maxPlaygroundConversationImageBytes = 12 << 20
)

var playgroundImageMediaTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

func validatePlaygroundImages(messages []ChatMessage) error {
	totalBytes := 0
	for _, message := range messages {
		parts, ok := message.Content.([]any)
		if !ok {
			continue
		}
		imageCount := 0
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok || stringFromPayload(part, "type") != "image_url" {
				continue
			}
			imageCount++
			if imageCount > maxPlaygroundImagesPerMessage {
				return NewHTTPError(http.StatusBadRequest, "too_many_images", "each playground message supports at most 4 images")
			}
			imageURL, isDataURI := normalizePlaygroundImageURL(part["image_url"])
			if imageURL == "" {
				return NewHTTPError(http.StatusBadRequest, "invalid_image", "image_url content requires a URL")
			}
			if !isDataURI {
				continue
			}
			mediaType, encoded, err := parseDataURI(imageURL)
			if err != nil {
				return NewHTTPError(http.StatusBadRequest, "invalid_image", errorMessage(err))
			}
			if !playgroundImageMediaTypes[strings.ToLower(mediaType)] {
				return NewHTTPError(http.StatusBadRequest, "unsupported_image_type", "playground images must be JPEG, PNG, or WebP")
			}
			decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil {
				return NewHTTPError(http.StatusBadRequest, "invalid_image", "image data is not valid base64")
			}
			if len(decoded) > maxPlaygroundImageBytes {
				return NewHTTPError(http.StatusBadRequest, "image_too_large", "each playground image must not exceed 5 MiB")
			}
			totalBytes += len(decoded)
			if totalBytes > maxPlaygroundConversationImageBytes {
				return NewHTTPError(http.StatusBadRequest, "images_too_large", "playground conversation images must not exceed 12 MiB")
			}
		}
	}
	return nil
}

func normalizePlaygroundImageURL(value any) (string, bool) {
	var imageURL string
	switch typed := value.(type) {
	case string:
		imageURL = typed
	case map[string]any:
		imageURL = stringFromPayload(typed, "url")
	}
	imageURL = strings.TrimSpace(imageURL)
	return imageURL, strings.HasPrefix(imageURL, "data:")
}

func playgroundAuditRequest(req ChatCompletionRequest) any {
	raw, err := json.Marshal(req)
	if err != nil {
		return map[string]any{"model": req.Model, "messages": "[unavailable]"}
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]any{"model": req.Model, "messages": "[unavailable]"}
	}
	return redactPlaygroundImages(payload)
}

func redactPlaygroundImages(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if stringFromPayload(typed, "type") == "image_url" {
			normalizedURL, isDataURI := normalizePlaygroundImageURL(typed["image_url"])
			switch imageURL := typed["image_url"].(type) {
			case map[string]any:
				if isDataURI {
					imageURL["url"] = playgroundImageAuditMarker(normalizedURL)
				}
			case string:
				if isDataURI {
					typed["image_url"] = playgroundImageAuditMarker(normalizedURL)
				}
			}
		}
		for key, item := range typed {
			typed[key] = redactPlaygroundImages(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = redactPlaygroundImages(item)
		}
	}
	return value
}

func playgroundImageAuditMarker(dataURI string) string {
	mediaType, encoded, err := parseDataURI(dataURI)
	if err != nil {
		return "[image data redacted]"
	}
	bytes := base64.StdEncoding.DecodedLen(len(encoded))
	return fmt.Sprintf("[image data redacted: %s, approximately %d bytes]", mediaType, bytes)
}
