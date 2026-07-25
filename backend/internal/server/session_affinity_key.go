package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// sessionIdentifierMaxLength bounds the session identifier so unbounded client
// input never reaches key derivation or logs.
const sessionIdentifierMaxLength = 512

// validateSessionIdentifier rejects identifiers that are too long or contain
// control characters. The caller supplies errorCode and label so each protocol
// keeps its own externally visible error contract.
func validateSessionIdentifier(canonical string, errorCode string, label string) error {
	if len(canonical) > sessionIdentifierMaxLength {
		return NewHTTPError(http.StatusBadRequest, errorCode, label+" is too long")
	}
	for _, character := range canonical {
		if character < 0x20 || character == 0x7f {
			return NewHTTPError(http.StatusBadRequest, errorCode, label+" contains invalid characters")
		}
	}
	return nil
}

// deriveSessionAffinityKey turns a session identifier into a tenant-isolated
// affinity key.
//
// Mixing in apiKeyID is required: without it two tenants using the same session
// identifier string would land on the same upstream account, sharing quota and
// leaking routing information.
//
// Note that the fallback path used when no secret is configured derives the MAC
// key from apiKeyID alone, so anyone who knows the apiKeyID can recompute the
// affinity key offline. It provides tenant isolation, not resistance to
// enumeration; configure SecretKey when the latter matters.
func deriveSessionAffinityKey(secret string, apiKeyID string, canonical string) string {
	key := []byte(strings.TrimSpace(secret))
	if len(key) == 0 {
		fallback := sha256.Sum256([]byte("tokenhub-affinity\x00" + strings.TrimSpace(apiKeyID)))
		key = fallback[:]
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.TrimSpace(apiKeyID)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// sessionAffinityKey validates and derives an affinity key for the protocol
// paths other than Codex.
func sessionAffinityKey(secret string, apiKeyID string, canonical string) (string, error) {
	if err := validateSessionIdentifier(canonical, "session_id_invalid", "Session identifier"); err != nil {
		return "", err
	}
	return deriveSessionAffinityKey(secret, apiKeyID, canonical), nil
}

// sessionIdentifierScope describes how coarse an identifier is.
//
// User-scoped identifiers (Anthropic metadata.user_id, OpenAI user) name an end
// user rather than a single conversation: all of that user's concurrent sessions
// share the value, so using one as an affinity key pins their entire traffic to a
// single account. That is still better than pure randomness (prefixes overlap more
// within one user), but whether to accept it depends on the deployment. The scope
// is therefore returned to the caller instead of being silently downgraded here.
type sessionIdentifierScope int

const (
	sessionScopeNone sessionIdentifierScope = iota
	// sessionScopeSession comes from an explicit session identifier and is
	// directly usable as an affinity key.
	sessionScopeSession
	// sessionScopeUser comes from an end-user identifier: coarser, and prone to
	// creating account hotspots.
	sessionScopeUser
)

// anthropicSessionIdentifier extracts the session identifier and its scope from
// an Anthropic Messages request.
func anthropicSessionIdentifier(headers http.Header, raw map[string]any) (string, sessionIdentifierScope) {
	for _, value := range []string{
		headers.Get("x-tokenhub-session-id"),
		headers.Get("session-id"),
		// Claude Code sends this header on every request, carrying its own session
		// UUID (observed with claude-cli/2.1.220).
		headers.Get("x-claude-code-session-id"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value, sessionScopeSession
		}
	}
	// metadata.user_id is an official Anthropic field. Claude Code stores a JSON
	// string in it containing device_id / account_uuid / session_id, in which case
	// the embedded session_id is the session-scoped identifier we want.
	userID := anthropicMetadataUserID(raw)
	if userID == "" {
		return "", sessionScopeNone
	}
	if sessionID := embeddedSessionID(userID); sessionID != "" {
		return sessionID, sessionScopeSession
	}
	// Other clients: treat it as a literal user identifier. Concurrent sessions of
	// one user share the value, so it only qualifies as user scope.
	return userID, sessionScopeUser
}

func anthropicMetadataUserID(raw map[string]any) string {
	metadata, ok := raw["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	userID, _ := metadata["user_id"].(string)
	return strings.TrimSpace(userID)
}

// embeddedSessionID parses identifiers shaped like
// {"device_id":..,"session_id":..} and returns the session_id. It returns an
// empty string when there is none, leaving the caller to fall back to the
// literal value.
func embeddedSessionID(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return ""
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return ""
	}
	sessionID, _ := decoded["session_id"].(string)
	return strings.TrimSpace(sessionID)
}

// chatCompletionSessionIdentifier extracts the session identifier and its scope
// from an OpenAI Chat Completions request.
//
// prompt_cache_key exists specifically for cache routing and is session scoped;
// user also carries abuse-detection semantics, so it only counts as user scope.
func chatCompletionSessionIdentifier(headers http.Header, request ChatCompletionRequest) (string, sessionIdentifierScope) {
	for _, value := range []string{
		headers.Get("x-tokenhub-session-id"),
		headers.Get("session-id"),
		stringFieldValue(request.PromptCacheKey),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value, sessionScopeSession
		}
	}
	if value := strings.TrimSpace(stringFieldValue(request.User)); value != "" {
		return value, sessionScopeUser
	}
	return "", sessionScopeNone
}

// stringFieldValue accepts string values only. Other types are still forwarded
// upstream verbatim, they just do not take part in affinity derivation.
func stringFieldValue(value any) string {
	text, _ := value.(string)
	return text
}
