package server

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const responsesRequestCompressionOption = "responses_request_compression"

// namedOptionSwitch reads an on/off provider option. Any other value is ignored
// so a typo cannot silently change routing.
func namedOptionSwitch(options map[string]string, key string) (string, bool) {
	if options == nil {
		return "", false
	}
	raw, ok := options[key]
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "off":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		return "", false
	}
}

func responsesEndpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// responsesRequestCompressionEnabled is off until an operator sets
// responses_request_compression=on. Official Codex also leaves zstd off unless
// request compression is enabled, and a default-on frame is not valid JSON for
// upstreams or tests that do not advertise zstd.
func responsesRequestCompressionEnabled(options map[string]string, host string) bool {
	value, ok := namedOptionSwitch(options, responsesRequestCompressionOption)
	return ok && value == "on" && strings.TrimSpace(host) != ""
}

// Level 3 is the Codex default. Higher levels do not shrink base64 images further.
var responsesZstdEncoder = sync.OnceValue(func() *zstd.Encoder {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(3)))
	if err != nil {
		return nil
	}
	return encoder
})

// encodeResponsesUpstreamBody compresses a finished upstream JSON body.
// Compression never changes the bytes TokenHub already read from the client.
// A frame that does not shrink the payload is skipped.
func encodeResponsesUpstreamBody(options map[string]string, host string, payload []byte) ([]byte, string) {
	if len(payload) == 0 || !responsesRequestCompressionEnabled(options, host) {
		return payload, ""
	}
	encoder := responsesZstdEncoder()
	if encoder == nil {
		return payload, ""
	}
	encoded := encoder.EncodeAll(payload, make([]byte, 0, len(payload)))
	if len(encoded) == 0 || len(encoded) >= len(payload) {
		return payload, ""
	}
	return encoded, "zstd"
}

// responsesCompressionRejected reports an upstream that refused the encoding.
// Other 400s are left alone so a bad request is not sent twice.
func responsesCompressionRejected(status int, body []byte) bool {
	if status == http.StatusUnsupportedMediaType {
		return true
	}
	if status != http.StatusBadRequest {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, "content-encoding") ||
		strings.Contains(text, "content_encoding") ||
		strings.Contains(text, "zstd") ||
		strings.Contains(text, "unsupported encoding")
}

// stampResponsesRequestCompression copies a resource switch onto the provider
// seen by the adapter. A resource value wins over the provider value.
func stampResponsesRequestCompression(provider *Provider, resource *ProviderResource) {
	if provider == nil || resource == nil {
		return
	}
	value, ok := namedOptionSwitch(resource.Options, responsesRequestCompressionOption)
	if !ok {
		return
	}
	provider.Options = cloneStringMap(provider.Options)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options[responsesRequestCompressionOption] = value
}
