package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func (s *Server) recordAdminAudit(r *http.Request, user AdminUser, action string, resourceType string, resourceID string, before any, after any) {
	s.recordAdminAuditWithStatus(r, user, action, resourceType, resourceID, "success", "", before, after)
}

func (s *Server) recordAdminAuditWithStatus(r *http.Request, user AdminUser, action string, resourceType string, resourceID string, status string, message string, before any, after any) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:    user.ID,
		ActorName:      user.Name,
		ActorRole:      user.Role,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Status:         status,
		Message:        message,
		BeforeSnapshot: snapshotJSON(before),
		AfterSnapshot:  snapshotJSON(after),
		IP:             s.clientIP(r),
		UserAgent:      r.UserAgent(),
	})
}

func snapshotJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func stringifyCSV(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.Trim(snapshotJSON(typed), `"`)
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	maxRequestBodyBytes := maxRequestBodyBytesFromContext(r.Context())
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(maxRequestBodyBytes)+1))
	if err != nil {
		return err
	}
	if len(data) > maxRequestBodyBytes {
		return fmt.Errorf("request body exceeds %d bytes", maxRequestBodyBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

const (
	// defaultMaxRequestBodyBytes caps JSON request bodies when no admin setting
	// is configured. It matches the historical hard-coded limit so the gateway
	// keeps its default posture unless an operator raises it.
	defaultMaxRequestBodyBytes = 4 << 20
	// minMaxRequestBodyBytes is the floor for an admin-configured limit. Below
	// this a deployment would reject ordinary requests, so the value clamps up.
	minMaxRequestBodyBytes = 1 << 20
	// maxMaxRequestBodyBytes is the ceiling for an admin-configured limit. It
	// matches the image generation response cap, bounding how much a single
	// request may buffer in memory.
	maxMaxRequestBodyBytes = 64 << 20
	// requestBodyLimitTTL bounds how long the cached limit stays fresh. Admin
	// edits to cfg_gateway take effect for new requests within this window; it
	// trades up-to-TTL staleness for not querying the store on every request.
	requestBodyLimitTTL = 10 * time.Second
)

type requestBodyLimitKey struct{}

// maxRequestBodyBytesFromContext returns the effective JSON request body limit
// injected by withRequestBodyLimit. Requests that bypass the middleware fall
// back to the default, so direct handler tests and non-HTTP callers keep
// working without a configured limit.
func maxRequestBodyBytesFromContext(ctx context.Context) int {
	if value, ok := ctx.Value(requestBodyLimitKey{}).(int); ok && value > 0 {
		return value
	}
	return defaultMaxRequestBodyBytes
}

// clampRequestBodyBytes keeps an admin-configured limit inside the safe range.
func clampRequestBodyBytes(value int) int {
	if value <= 0 {
		return defaultMaxRequestBodyBytes
	}
	if value < minMaxRequestBodyBytes {
		return minMaxRequestBodyBytes
	}
	if value > maxMaxRequestBodyBytes {
		return maxMaxRequestBodyBytes
	}
	return value
}

type requestBodyLimitSnapshot struct {
	value       int
	refreshedAt time.Time
}

// requestBodyLimitCache holds the effective limit with an atomic snapshot so
// the hot path reads without a lock; refreshes are occasional idempotent
// stores.
type requestBodyLimitCache struct {
	snapshot atomic.Pointer[requestBodyLimitSnapshot]
}

// withRequestBodyLimit injects the effective request body limit into each
// request context so decodeJSON can enforce a runtime-configurable cap without
// changing its call sites.
func (s *Server) withRequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestBodyLimitKey{}, s.currentMaxRequestBodyBytes())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) currentMaxRequestBodyBytes() int {
	if snap := s.requestBodyLimit.snapshot.Load(); snap != nil && time.Since(snap.refreshedAt) < requestBodyLimitTTL {
		return snap.value
	}
	value := s.readMaxRequestBodyBytesSetting()
	s.requestBodyLimit.snapshot.Store(&requestBodyLimitSnapshot{value: value, refreshedAt: time.Now()})
	return value
}

// readMaxRequestBodyBytesSetting reads max_request_body_bytes from the
// cfg_gateway system setting and clamps it. It mirrors apiKeyGenerationConfig:
// cfg_gateway is preferred, the first active settings record is a fallback,
// and a missing or malformed value falls back to the default rather than
// rejecting every request.
func (s *Server) readMaxRequestBodyBytesSetting() int {
	var fields map[string]any
	for _, item := range s.store.ListResources("settings") {
		if item.Status != StatusActive {
			continue
		}
		if item.ID == "cfg_gateway" {
			fields = item.Fields
			break
		}
		if fields == nil {
			fields = item.Fields
		}
	}
	parsed, ok := intField(fields, "max_request_body_bytes")
	if !ok || parsed <= 0 {
		return defaultMaxRequestBodyBytes
	}
	return clampRequestBodyBytes(parsed)
}

// intField reads an integer from a settings Fields map regardless of how the
// value was stored. The GORM JSON serializer round-trips numbers through
// encoding/json, which turns every numeric literal into a float64; for large
// values (such as a multi-miB byte count) fmt.Sprint renders that float in
// scientific notation and strconv.Atoi then fails. Handling int, int64,
// float64, and string directly avoids that latent trap.
func intField(fields map[string]any, key string) (int, bool) {
	if fields == nil {
		return 0, false
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(math.Round(typed)), true
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	httpErr := AsHTTPError(err)
	requestID := errorResponseHeaders(w, err)
	writeJSON(w, httpErr.Status, map[string]any{
		"error": map[string]any{
			"message": httpErr.Message,
			"type":    httpErr.Code,
			"code":    httpErr.Code,
		},
		"request_id": requestID,
	})
}

func writeRateLimitHeaders(header http.Header, values map[string]string) {
	for key, value := range values {
		if strings.TrimSpace(value) != "" {
			header.Set(key, value)
		}
	}
}

// errorResponseHeaders installs the headers every error response carries and
// returns the request ID its body has to repeat. Retry-After among them: the
// upstream already said how long to wait, and answering 429 without it leaves the
// caller guessing.
func errorResponseHeaders(w http.ResponseWriter, err error) string {
	requestID := strings.TrimSpace(w.Header().Get("x-request-id"))
	if requestID == "" {
		requestID = NewID("req")
	}
	w.Header().Set("x-request-id", requestID)
	writeRateLimitHeaders(w.Header(), AsHTTPError(err).Headers)
	if retryAfter := providerErrorRetryAfter(err); retryAfter > 0 &&
		AsHTTPError(err).Status == http.StatusTooManyRequests &&
		w.Header().Get("retry-after") == "" {
		w.Header().Set("retry-after", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	}
	return requestID
}

const auditPayloadMaxChars = 64 * 1024

func (s *Server) recordRequestPayload(requestID string, requestPayload any, responsePayload any) {
	requestBody, requestTruncated := auditPayloadBody(requestPayload)
	responseBody, responseTruncated := auditPayloadBody(responsePayload)
	s.store.RecordRequestPayload(requestID, requestBody, requestTruncated, responseBody, responseTruncated)
}

func auditPayloadBody(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	raw, err := json.MarshalIndent(redactAuditPayload(value), "", "  ")
	if err != nil {
		raw, _ = json.MarshalIndent(map[string]any{"error": "payload_not_serializable"}, "", "  ")
	}
	text := string(raw)
	runes := []rune(text)
	if len(runes) <= auditPayloadMaxChars {
		return text, false
	}
	return string(runes[:auditPayloadMaxChars]) + "\n... truncated", true
}

func redactAuditPayload(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return value
	}
	return redactAuditValue(decoded)
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveAuditKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactAuditValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactAuditValue(item))
		}
		return out
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	switch normalized {
	case "authorization", "apikey", "apitoken", "accesstoken", "refreshtoken", "clientsecret", "secretkey", "password", "token", "secret":
		return true
	default:
		return strings.Contains(normalized, "authorization") || strings.Contains(normalized, "password") || strings.Contains(normalized, "secret")
	}
}

func auditErrorPayload(err error, requestID string) map[string]any {
	httpErr := AsHTTPError(err)
	return map[string]any{
		"error": map[string]any{
			"message": httpErr.Message,
			"type":    httpErr.Code,
			"code":    httpErr.Code,
		},
		"request_id": requestID,
	}
}

func auditStreamPayload(status int, code string, err error) map[string]any {
	payload := map[string]any{
		"stream":      true,
		"captured":    false,
		"status_code": status,
	}
	if err != nil {
		payload["error"] = map[string]any{
			"message": errorMessage(err),
			"type":    code,
			"code":    code,
		}
	}
	return payload
}

func statusAndCode(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	httpErr := AsHTTPError(err)
	return httpErr.Status, httpErr.Code
}

func (s *Server) clientIP(r *http.Request) string {
	remoteIP := requestRemoteIP(r)
	if !ipMatchesTrustedProxy(remoteIP, s.config.TrustedProxyCIDRs) {
		return remoteIP
	}
	forwarded := strings.Split(r.Header.Get("x-forwarded-for"), ",")
	parsed := make([]string, 0, len(forwarded))
	for _, value := range forwarded {
		value = strings.TrimSpace(value)
		ip := net.ParseIP(value)
		if value == "" || ip == nil {
			return remoteIP
		}
		parsed = append(parsed, ip.String())
	}
	for index := len(parsed) - 1; index >= 0; index-- {
		if !ipMatchesTrustedProxy(parsed[index], s.config.TrustedProxyCIDRs) {
			return parsed[index]
		}
	}
	if len(parsed) > 0 {
		return parsed[0]
	}
	return remoteIP
}

func requestRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
}

func ipMatchesTrustedProxy(rawIP string, trusted []string) bool {
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil {
		return false
	}
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func isDevEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "dev", "development", "local", "test":
		return true
	}
	return false
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Determine allowed origins: use explicit config if set, otherwise derive from PublicBaseURL
		allowedOrigins := s.config.CORSAllowedOrigins
		if len(allowedOrigins) == 0 && s.config.PublicBaseURL != "" {
			allowedOrigins = []string{s.config.PublicBaseURL}
		}

		// If request has Origin header and we have an allowed origins list, validate it
		if origin != "" && len(allowedOrigins) > 0 {
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if origin == strings.TrimSpace(allowedOrigin) {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("access-control-allow-origin", origin)
				w.Header().Set("access-control-allow-credentials", "true")
				w.Header().Add("Vary", "Origin")
			} else {
				// Origin not in allowlist, deny credentials but allow simple requests
				w.Header().Set("access-control-allow-origin", "*")
			}
		} else if origin != "" && isDevEnvironment(s.config.Environment) {
			// Dev environment without an allowlist: echo origin with credentials
			// for convenience. Never do this in production, where an explicit
			// allowlist (or PublicBaseURL) is required to authorize credentials.
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("access-control-allow-credentials", "true")
			w.Header().Add("Vary", "Origin")
		} else {
			// No Origin header, or production with no configured allowlist:
			// allow simple cross-origin requests but never credentials.
			w.Header().Set("access-control-allow-origin", "*")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
			}
		}

		w.Header().Set("access-control-allow-methods", "GET,POST,PATCH,DELETE,OPTIONS")
		allowHeaders := "authorization,content-type"
		if reqHeaders := r.Header.Get("access-control-request-headers"); reqHeaders != "" {
			seen := map[string]bool{"authorization": true, "content-type": true}
			for _, h := range strings.Split(reqHeaders, ",") {
				h = strings.ToLower(strings.TrimSpace(h))
				if h != "" && !seen[h] {
					allowHeaders += "," + h
					seen[h] = true
				}
			}
		}
		w.Header().Set("access-control-allow-headers", allowHeaders)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAdminSystemDBStatus(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify administrator permissions.
	if _, ok := s.requireAdmin(w, r, "system", r.Method); !ok {
		return
	}

	// Retrieve the database status.
	status, err := s.store.GetDatabaseStatus()
	if err != nil {
		log.Printf("[tokenhub] failed to get database status: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return the JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("[tokenhub] failed to encode database status response: %v", err)
	}
}
