package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

func NewID(prefix string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buf[:])
}

func GenerateAPIKeyWithOptions(prefix string, randomLength int) string {
	prefix = NormalizeAPIKeyPrefix(prefix)
	randomLength = NormalizeAPIKeyRandomLength(randomLength)
	byteLen := (randomLength*3 + 3) / 4
	buf := make([]byte, byteLen+2)
	if _, err := rand.Read(buf); err != nil {
		randomPart := ""
		for len(randomPart) < randomLength {
			randomPart += strings.TrimPrefix(NewID("live"), "live_")
		}
		return prefix + randomPart[:randomLength]
	}
	randomPart := base64.RawURLEncoding.EncodeToString(buf)
	for len(randomPart) < randomLength {
		var extra [12]byte
		if _, err := rand.Read(extra[:]); err != nil {
			randomPart += strings.TrimPrefix(NewID("live"), "live_")
			continue
		}
		randomPart += base64.RawURLEncoding.EncodeToString(extra[:])
	}
	return prefix + randomPart[:randomLength]
}

func NormalizeAPIKeyPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return DefaultAPIKeyPrefix
	}
	var builder strings.Builder
	for _, char := range prefix {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		}
		if builder.Len() >= MaxAPIKeyPrefixLength {
			break
		}
	}
	if builder.Len() == 0 {
		return DefaultAPIKeyPrefix
	}
	return builder.String()
}

func NormalizeAPIKeyRandomLength(length int) int {
	if length <= 0 {
		return DefaultAPIKeyRandomLength
	}
	if length < MinAPIKeyRandomLength {
		return MinAPIKeyRandomLength
	}
	if length > MaxAPIKeyRandomLength {
		return MaxAPIKeyRandomLength
	}
	return length
}

func GenerateAdminSessionToken() string {
	return "tha_" + NewID("session")
}

func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func PrefixSuffix(secret string) (string, string) {
	if len(secret) <= 12 {
		return secret, secret
	}
	return secret[:8], secret[len(secret)-6:]
}

func EstimateTextTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	words := int64(len(strings.Fields(text)))
	chars := int64(math.Ceil(float64(len([]rune(text))) / 4.0))
	if words > chars {
		return words
	}
	return chars
}

func ChatPromptText(messages []ChatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		parts = append(parts, contentToText(msg.Content))
	}
	return strings.Join(parts, "\n")
}

func ResponsesInputText(input any) string {
	return contentToText(input)
}

func EmbeddingInputText(input any) string {
	return contentToText(input)
}

func contentToText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			parts = append(parts, contentToText(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		if value, ok := typed["content"]; ok {
			return contentToText(value)
		}
		var parts []string
		for _, item := range typed {
			parts = append(parts, contentToText(item))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(value)
	}
}
