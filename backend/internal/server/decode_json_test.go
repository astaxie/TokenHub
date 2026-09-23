package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newDecodeTestServer builds a Server with small, explicit body limits so the
// tests can exercise the boundaries without megabyte-sized payloads.
func newDecodeTestServer(jsonLimit, multimodalLimit int64) *Server {
	return NewWithConfig(NewMemoryStore(), Config{
		AdminToken:                "dev_admin_token",
		MaxJSONRequestBytes:       jsonLimit,
		MaxMultimodalRequestBytes: multimodalLimit,
	})
}

func decodeRequest(body string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	return httptest.NewRecorder(), r
}

func TestDecodeJSONUnderLimit(t *testing.T) {
	s := newDecodeTestServer(1<<10, 4<<10)
	w, r := decodeRequest(`{"name":"ok"}`)
	var payload struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, &payload); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if payload.Name != "ok" {
		t.Fatalf("decoded %q, want ok", payload.Name)
	}
}

func TestDecodeJSONOverLimitReturns413(t *testing.T) {
	s := newDecodeTestServer(1<<10, 4<<10)
	big := `{"data":"` + strings.Repeat("a", 4096) + `"}`
	w, r := decodeRequest(big)
	var payload map[string]any
	err := s.decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatal("expected an error for an over-limit body")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", httpErr.Status)
	}
	if httpErr.Code != "payload_too_large" {
		t.Fatalf("code = %q, want payload_too_large", httpErr.Code)
	}
}

func TestDecodeJSONMalformedReturns400(t *testing.T) {
	s := newDecodeTestServer(1<<10, 4<<10)
	w, r := decodeRequest(`{"name":`)
	var payload map[string]any
	err := s.decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadRequest || httpErr.Code != "invalid_request" {
		t.Fatalf("got %d/%q, want 400/invalid_request", httpErr.Status, httpErr.Code)
	}
}

func TestDecodeJSONTrailingDataReturns400(t *testing.T) {
	s := newDecodeTestServer(1<<10, 4<<10)
	w, r := decodeRequest(`{"a":1}{"b":2}`)
	var payload map[string]any
	err := s.decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatal("expected an error for trailing data")
	}
	if httpErr := AsHTTPError(err); httpErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", httpErr.Status)
	}
}

func TestDecodeJSONEmptyBody(t *testing.T) {
	s := newDecodeTestServer(1<<10, 4<<10)

	// Strict: an empty body is rejected with 400.
	w, r := decodeRequest("")
	var payload map[string]any
	err := s.decodeJSON(w, r, &payload)
	if err == nil {
		t.Fatal("decodeJSON should reject an empty body")
	}
	if httpErr := AsHTTPError(err); httpErr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", httpErr.Status)
	}

	// Optional: an empty body is accepted, leaving the target at its zero value.
	w, r = decodeRequest("")
	var optional map[string]any
	if err := s.decodeJSONOptional(w, r, &optional); err != nil {
		t.Fatalf("decodeJSONOptional should accept an empty body, got %v", err)
	}
}

// TestDecodeJSONMultimodalLimit verifies the multimodal ceiling is independent of
// the default: a body that exceeds the JSON limit still decodes under the higher
// multimodal limit.
func TestDecodeJSONMultimodalLimit(t *testing.T) {
	s := newDecodeTestServer(1<<10, 8<<10)
	body := `{"data":"` + strings.Repeat("a", 4096) + `"}` // ~4 KiB: over 1 KiB, under 8 KiB

	// Default JSON limit rejects it.
	w, r := decodeRequest(body)
	var rejected map[string]any
	if err := s.decodeJSON(w, r, &rejected); AsHTTPError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 under the default limit, got %v", err)
	}

	// Multimodal limit accepts it.
	w, r = decodeRequest(body)
	var accepted map[string]any
	if err := s.decodeJSONLimit(w, r, &accepted, s.config.MaxMultimodalRequestBytes); err != nil {
		t.Fatalf("expected success under the multimodal limit, got %v", err)
	}
}

// TestIsPayloadTooLarge locks the predicate that handlers use to let the typed 413
// pass through instead of collapsing it into a bespoke 400.
func TestIsPayloadTooLarge(t *testing.T) {
	if !isPayloadTooLarge(NewHTTPError(http.StatusRequestEntityTooLarge, "payload_too_large", "too big")) {
		t.Fatal("expected a 413 error to be recognized")
	}
	if isPayloadTooLarge(NewHTTPError(http.StatusBadRequest, "invalid_request", "bad")) {
		t.Fatal("did not expect a 400 error to be recognized as payload-too-large")
	}
	if isPayloadTooLarge(nil) {
		t.Fatal("did not expect nil to be recognized as payload-too-large")
	}
}

// TestAnthropicErrorTypeForPayloadTooLarge verifies a 413 maps to the Anthropic
// request_too_large type rather than the generic api_error.
func TestAnthropicErrorTypeForPayloadTooLarge(t *testing.T) {
	if got := anthropicErrorType(http.StatusRequestEntityTooLarge); got != "request_too_large" {
		t.Fatalf("anthropicErrorType(413) = %q, want request_too_large", got)
	}
}

// TestBillingConnectorOverLimitReturns413 is an end-to-end regression: the billing
// connector handler must surface payload_too_large from an over-limit body instead
// of replacing it with its bespoke invalid_billing_connector 400.
func TestBillingConnectorOverLimitReturns413(t *testing.T) {
	handler := NewWithConfig(NewMemoryStore(), Config{
		AdminToken:                "dev_admin_token",
		MaxJSONRequestBytes:       1 << 10,
		MaxMultimodalRequestBytes: 4 << 10,
	}).Handler()

	big, err := json.Marshal(map[string]any{"name": strings.Repeat("a", 4096)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/billing/connectors", bytes.NewReader(big))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer dev_admin_token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "payload_too_large") {
		t.Fatalf("expected payload_too_large in body, got %s", rr.Body.String())
	}
}
