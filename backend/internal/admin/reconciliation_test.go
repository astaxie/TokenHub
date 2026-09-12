package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

type reconciliationApplicationFake struct {
	run        reconciliation.Run
	items      []reconciliation.Item
	lockBefore reconciliation.Run
	lockAfter  reconciliation.Run
	lockErr    error
}

func (f reconciliationApplicationFake) ListRules() []reconciliation.Rule { return nil }
func (f reconciliationApplicationFake) GetRuleForAdmin(string) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, nil
}
func (f reconciliationApplicationFake) CreateRule(reconciliation.RuleInput, string) (reconciliation.Rule, error) {
	return reconciliation.Rule{}, nil
}
func (f reconciliationApplicationFake) UpdateRule(string, reconciliation.RulePatch, string) (reconciliation.Rule, reconciliation.Rule, error) {
	return reconciliation.Rule{}, reconciliation.Rule{}, nil
}
func (f reconciliationApplicationFake) Run(context.Context, string, reconciliation.RunInput, string, string) (reconciliation.Run, error) {
	return reconciliation.Run{}, nil
}
func (f reconciliationApplicationFake) ListRuns(string, int) []reconciliation.Run {
	return []reconciliation.Run{f.run}
}
func (f reconciliationApplicationFake) GetRunDetail(string, string, int, int) (reconciliation.Run, []reconciliation.Item, int64, error) {
	return f.run, f.items, int64(len(f.items)), nil
}
func (f reconciliationApplicationFake) Lock(string, string) (reconciliation.Run, reconciliation.Run, error) {
	return f.lockBefore, f.lockAfter, f.lockErr
}
func (f reconciliationApplicationFake) RecalculateForAdmin(context.Context, string) (reconciliation.Run, reconciliation.Run, error) {
	return reconciliation.Run{}, reconciliation.Run{}, nil
}
func (f reconciliationApplicationFake) Export(_ string, _ string, _ int, start func(reconciliation.Run) error, each func([]reconciliation.Item) error) error {
	if err := start(f.run); err != nil {
		return err
	}
	return each(f.items)
}

var _ ReconciliationApplication = reconciliationApplicationFake{}

func testReconciliationTransport() ReconciliationTransport {
	return ReconciliationTransport{
		DecodeJSON: func(http.ResponseWriter, *http.Request, any) error { return fmt.Errorf("decode failed") },
		NewError:   func(status int, code, message string) error { return fmt.Errorf("%d:%s:%s", status, code, message) },
		MapError:   func(err error) error { return err },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		},
		IsPayloadTooLarge: func(error) bool { return false },
		ErrorCode:         func(error) string { return "" },
	}
}

func TestReconciliationAuditSnapshotsExcludeSensitiveFields(t *testing.T) {
	rule := reconciliation.Rule{
		ID: "rule-1", Name: "billing rule", ConnectorID: "connector-1", ConnectorType: "oneapi",
		ProviderID: "provider-1", ProviderResourceID: "resource-secret", Status: reconciliation.StatusActive,
		MatchDimensions: []string{"resource_account", "model"},
		DimensionMappings: map[string]map[string]string{
			"resource_account": {"secret-source": "secret-target"},
			"model":            {"upstream": "canonical"},
		},
	}
	ruleSnapshot := ruleAuditSnapshot(rule)
	runSnapshot := reconciliation.RunAuditSnapshot(reconciliation.Run{ID: "run-1", ProviderID: "provider-1", ProviderResourceID: "resource-secret", ErrorMessage: "internal details", StartedAt: time.Now().UTC()})
	if ruleSnapshot["provider_resource_scope_configured"] != true || ruleSnapshot["resource_account_mapping_count"] != 1 {
		t.Fatalf("rule snapshot lost redaction metadata: %#v", ruleSnapshot)
	}
	if _, ok := ruleSnapshot["provider_resource_id"]; ok {
		t.Fatal("rule snapshot exposed provider resource ID")
	}
	if strings.Contains(stringSnapshot(ruleSnapshot), "secret-source") || strings.Contains(stringSnapshot(ruleSnapshot), "secret-target") {
		t.Fatalf("rule snapshot exposed resource mapping: %#v", ruleSnapshot)
	}
	if strings.Contains(stringSnapshot(runSnapshot), "resource-secret") || strings.Contains(stringSnapshot(runSnapshot), "internal details") {
		t.Fatalf("run snapshot exposed sensitive fields: %#v", runSnapshot)
	}
}

func TestReconciliationCSVCellEscaping(t *testing.T) {
	for _, value := range []string{"=SUM(A1)", "+formula", "-formula", "@formula", "  =formula"} {
		if got := safeCSV(value); !strings.HasPrefix(got, "'") {
			t.Fatalf("CSV formula was not escaped: %q -> %q", value, got)
		}
	}
	if got := safeCSV("ordinary"); got != "ordinary" {
		t.Fatalf("ordinary CSV value changed: %q", got)
	}
}

func TestReconciliationHandlerExportsPublicProjection(t *testing.T) {
	fake := reconciliationApplicationFake{
		run:   reconciliation.Run{ID: "run-1", ProviderID: "provider-1", ProviderResourceID: "resource-secret", Status: reconciliation.RunSucceeded},
		items: []reconciliation.Item{{ID: "item-1", Status: reconciliation.ProviderOnly, ResourceAccount: "secret-account", ResourceAccountMasked: "se****nt", Model: "=formula"}},
	}
	h := NewReconciliationHandler(fake, testReconciliationTransport())
	request := httptest.NewRequest(http.MethodGet, "/api/admin/billing/reconciliations/run-1/export", nil)
	response := httptest.NewRecorder()
	h.Export(response, request, AdminActor{ID: "admin-1"}, "run-1")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "se****nt") || strings.Contains(response.Body.String(), "secret-account") || !strings.Contains(response.Body.String(), "'=formula") {
		t.Fatalf("public export projection changed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReconciliationLockKnownRunFailureKeepsHTTPErrorAndDoesNotAudit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "policy failure",
			err:  reconciliation.NewError(reconciliation.ErrorConflict, "reconciliation_run_not_complete", "Only successful reconciliation runs can be locked"),
			want: "409:reconciliation_run_not_complete",
		},
		{
			name: "persistence failure",
			err:  fmt.Errorf("save reconciliation lock: unavailable"),
			want: "500:internal_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := reconciliationApplicationFake{
				lockBefore: reconciliation.Run{ID: "run-1", Status: reconciliation.RunFailed},
				lockErr:    test.err,
			}
			var audits []AdminAudit
			var mapped error
			var written error
			h := NewReconciliationHandler(service, ReconciliationTransport{
				MapError: func(err error) error {
					mapped = err
					status := http.StatusInternalServerError
					if reconciliationErrorCode(err) == "reconciliation_run_not_complete" {
						status = http.StatusConflict
					}
					return fmt.Errorf("%d:%s", status, reconciliationErrorCode(err))
				},
				WriteError: func(_ http.ResponseWriter, _ *http.Request, err error) { written = err },
				Audit:      func(_ *http.Request, _ AdminActor, audit AdminAudit) { audits = append(audits, audit) },
			})

			h.LockRun(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), AdminActor{ID: "admin-1"}, "run-1")
			if mapped != test.err || written == nil || written.Error() != test.want {
				t.Fatalf("lock failure mapping changed: mapped=%v written=%v want=%s", mapped, written, test.want)
			}
			if len(audits) != 0 {
				t.Fatalf("lock failure created audit events: %#v", audits)
			}
		})
	}
}

func TestReconciliationMalformedJSONKeepsEndpointMessages(t *testing.T) {
	var got error
	h := &ReconciliationHandler{transport: ReconciliationTransport{
		IsPayloadTooLarge: func(error) bool { return false },
		NewError:          func(status int, code, message string) error { return fmt.Errorf("%d:%s:%s", status, code, message) },
		WriteError:        func(_ http.ResponseWriter, _ *http.Request, err error) { got = err },
	}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	h.badJSON(w, r, fmt.Errorf("malformed"), "invalid_reconciliation_rule")
	if got == nil || !strings.Contains(got.Error(), "Invalid reconciliation rule payload") {
		t.Fatalf("rule payload message changed: %v", got)
	}
	h.badJSON(w, r, fmt.Errorf("malformed"), "invalid_reconciliation_run")
	if got == nil || !strings.Contains(got.Error(), "Invalid reconciliation run payload") {
		t.Fatalf("run payload message changed: %v", got)
	}
}

func TestReconciliationRunDecodeAuditPreservesHTTPErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		tooLarge bool
		code     string
	}{
		{name: "malformed", err: fmt.Errorf("malformed"), code: "invalid_reconciliation_run"},
		{name: "payload too large", err: fmt.Errorf("payload_too_large"), tooLarge: true, code: "payload_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var event AdminAudit
			var writeErr error
			transport := ReconciliationTransport{
				DecodeJSON:        func(http.ResponseWriter, *http.Request, any) error { return test.err },
				IsPayloadTooLarge: func(error) bool { return test.tooLarge },
				ErrorCode:         func(error) string { return test.code },
				NewError:          func(status int, code, message string) error { return fmt.Errorf("%d:%s:%s", status, code, message) },
				WriteError:        func(_ http.ResponseWriter, _ *http.Request, err error) { writeErr = err },
				Audit:             func(_ *http.Request, _ AdminActor, got AdminAudit) { event = got },
			}
			h := NewReconciliationHandler(nil, transport)
			h.RunRule(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), AdminActor{ID: "admin-1"}, "rule-1")
			if writeErr == nil || !strings.Contains(writeErr.Error(), test.code) {
				t.Fatalf("error code was not returned: %v", writeErr)
			}
			after, ok := event.After.(map[string]any)
			if !ok || event.Message != test.code || after["error_code"] != test.code {
				t.Fatalf("audit code = %q/%v, want %q", event.Message, event.After, test.code)
			}
		})
	}
}

func stringSnapshot(value map[string]any) string {
	var builder strings.Builder
	for key, item := range value {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(toString(item))
	}
	return builder.String()
}

func toString(value any) string {
	return fmt.Sprintf("%v", value)
}
