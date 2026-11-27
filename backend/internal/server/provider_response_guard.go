package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
)

const unexpectedProviderMessage = "Upstream provider returned an unexpected response"

// Keep diagnostic text off Error() and the public protocol error.
func unexpectedProviderResponse(provider Provider, data []byte, disposition ProviderErrorDisposition, status int) error {
	failure := NewHTTPError(http.StatusBadGateway, "provider_upstream_error", unexpectedProviderMessage)
	failure.UpstreamStatus = status
	if len(data) > providerErrorBodyPrefix {
		// A prefix ending in the middle of a credential cannot be safely redacted.
		failure.diagnostic = providerHeaderMask
	} else {
		safe := redactProviderErrorSecrets(data, provider)
		failure.diagnostic = string(safe[:min(len(safe), providerErrorBodyPrefix)])
	}
	return &ProviderInvocationError{Err: failure, Disposition: disposition}
}

func attemptDiagnostic(err error) string {
	if e := AsHTTPError(err); e != nil {
		return e.diagnostic
	}
	return ""
}

func checkProviderResult(body map[string]any, field string, provider Provider, status int) error {
	if _, exists := body[field]; exists {
		return nil
	}
	var code float64
	switch n := body["code"].(type) {
	case float64:
		code = n
	case json.Number:
		code, _ = n.Float64()
	}
	success, reported := body["success"].(bool)
	if body["error"] == nil && (!reported || success) && code < 400 {
		return nil
	}
	data, _ := json.Marshal(body)
	return unexpectedProviderResponse(provider, data, ProviderErrorPolicy, status)
}

type streamSecretPatterns [][]byte

func newStreamSecretPatterns(provider Provider) streamSecretPatterns {
	var patterns streamSecretPatterns
	for _, value := range providerErrorSecretValues(provider) {
		for _, representation := range providerSecretRepresentations(value) {
			patterns = append(patterns, []byte(representation))
		}
	}
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	return patterns
}

func (patterns streamSecretPatterns) redact(data []byte) []byte {
	for _, pattern := range patterns {
		if bytes.Contains(data, pattern) {
			data = bytes.ReplaceAll(data, pattern, []byte(providerHeaderMask))
		}
	}
	return data
}

// Conversions and plugin hooks may generate new bytes after the upstream
// stream has been checked. Their sinks write complete rendered lines or frames.
type providerStreamWriter struct {
	io.Writer
	patterns streamSecretPatterns
}

func redactStreamOutput(w io.Writer, provider Provider) io.Writer {
	patterns := newStreamSecretPatterns(provider)
	if len(patterns) == 0 {
		return w
	}
	return &providerStreamWriter{Writer: w, patterns: patterns}
}

func (w *providerStreamWriter) Write(p []byte) (int, error) {
	output := w.patterns.redact(p)
	n, err := w.Writer.Write(output)
	if err == nil && n != len(output) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *providerStreamWriter) Flush() {
	if flusher, ok := w.Writer.(streamFlusher); ok {
		flusher.Flush()
	}
}

// A provider can label JSON as SSE or SSE as JSON. Validate lines before a
// passthrough or conversion sees them, including an unterminated final line.
type providerSSEBody struct {
	io.Closer
	reader      *bufio.Reader
	provider    func() Provider
	patterns    streamSecretPatterns
	limit       int
	status      int
	disposition ProviderErrorDisposition
	started     bool
	pending     []byte
	err         error
}

func guardProviderSSE(response *http.Response, provider Provider, limit int) {
	response.Body = newProviderSSEBody(response.Body, provider, limit, response.StatusCode)
}

func newProviderSSEBody(body io.ReadCloser, provider Provider, limit, status int) *providerSSEBody {
	if limit <= 0 {
		limit = maxSSEEventBytes
	}
	return &providerSSEBody{
		Closer: body, reader: bufio.NewReader(body), provider: func() Provider { return provider },
		patterns: newStreamSecretPatterns(provider), limit: limit, status: status,
		disposition: ProviderErrorTransientSame,
	}
}

// Chat and native Messages passthrough used to accept an unframed body as
// an empty completion. Reject it without retrying or blaming the resource.
func passthroughSSEDecoder(body io.Reader, provider Provider, limit, status int) *sseDecoder {
	guard := newProviderSSEBody(io.NopCloser(body), provider, limit, status)
	guard.disposition = ProviderErrorPolicy
	decoder := newSSEDecoder(guard)
	decoder.assembler.limit = limit
	return decoder
}

func (r *providerSSEBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 && r.err == nil {
		line, err := r.readLine()
		r.err = err
		if !r.started {
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		}
		field := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if len(field) == 0 {
			if r.started {
				r.pending = line
			}
			continue
		}
		if !providerSSEField(field) {
			if !r.started && (err == nil || err == io.EOF) {
				r.err = unexpectedProviderResponse(r.provider(), field, r.disposition, r.status)
			}
			continue
		}
		r.started = true
		r.pending = r.patterns.redact(line)
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	if len(r.pending) > 0 {
		return n, nil
	}
	return n, r.err
}

func (r *providerSSEBody) readLine() ([]byte, error) {
	line, err := r.reader.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		buffer := bytes.Clone(line)
		for err == bufio.ErrBufferFull {
			line, err = r.reader.ReadSlice('\n')
			if len(buffer)+len(line) > r.limit {
				return nil, errSSEEventTooLarge()
			}
			buffer = append(buffer, line...)
		}
		line = buffer
	}
	if len(line) > r.limit {
		return nil, errSSEEventTooLarge()
	}
	return line, err
}

func providerSSEField(line []byte) bool {
	if bytes.IndexByte(line, '\r') >= 0 {
		return false
	}
	name, _, colon := bytes.Cut(line, []byte(":"))
	switch string(name) {
	case "data", "event", "id", "retry":
		return true
	}
	if !colon {
		return false
	}
	// Accept comments and token-shaped vendor fields. JSON object keys,
	// HTML and free-form diagnostics do not form field names.
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
