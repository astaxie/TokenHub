package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func jevDetailsModel(parameters ...string) ProviderModel {
	return ProviderModel{
		ProviderID:          "provider_test",
		UpstreamModel:       "model_test",
		Status:              StatusActive,
		SupportedParameters: parameters,
		Metadata:            map[string]string{"endpoints": "chat/completions"},
	}
}

func jevDetailsRaw(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestJevAdmissionCollectsEveryMissingParameter(t *testing.T) {
	model := jevDetailsModel("max_tokens")
	raw := jevDetailsRaw(t, `{"messages":[],"store":false,"top_p":0.5,"seed":7}`)
	admission := jevCheckAdmission(model, "chat/completions", raw, 10, 0, "task", false)
	if admission.Reason != "" {
		t.Fatalf("unexpected fatal rejection: %+v", admission.jevCompatibility)
	}
	want := []jevParameterRejection{
		{Reason: "parameter_not_declared", Parameter: "seed"},
		{Reason: "parameter_not_declared", Parameter: "store"},
		{Reason: "parameter_not_declared", Parameter: "top_p"},
	}
	if len(admission.Missing) != len(want) {
		t.Fatalf("missing=%+v want=%+v", admission.Missing, want)
	}
	for index := range want {
		if admission.Missing[index] != want[index] {
			t.Fatalf("missing[%d]=%+v want=%+v", index, admission.Missing[index], want[index])
		}
	}
	// The legacy single-reason surface keeps reporting the first rejection.
	if first := admission.first(); first.Reason != "parameter_not_declared" || first.Parameter != "seed" {
		t.Fatalf("first=%+v", first)
	}
}

func TestJevAdmissionFatalRejectionSkipsParameterCollection(t *testing.T) {
	model := jevDetailsModel("max_tokens")
	model.Status = StatusDisabled
	raw := jevDetailsRaw(t, `{"messages":[],"store":false,"top_p":0.5}`)
	admission := jevCheckAdmission(model, "chat/completions", raw, 10, 0, "task", false)
	if admission.Reason != "model_disabled" || len(admission.Missing) != 0 {
		t.Fatalf("admission=%+v", admission)
	}
}

func TestJevAdmissionCollapsesUnknownFields(t *testing.T) {
	model := jevDetailsModel("max_tokens")
	raw := jevDetailsRaw(t, `{"messages":[],"vendor_a":1,"vendor_b":2}`)
	admission := jevCheckAdmission(model, "chat/completions", raw, 10, 0, "task", false)
	if len(admission.Missing) != 1 || admission.Missing[0].Parameter != "unknown" {
		t.Fatalf("missing=%+v", admission.Missing)
	}
}

func TestSummarizeJevRejectionsAggregatesAndCaps(t *testing.T) {
	missing := []jevParameterRejection{
		{Reason: "parameter_not_declared", Parameter: "store"},
		{Reason: "parameter_not_declared", Parameter: "top_p"},
	}
	rejections := []jevCandidateRejection{
		{CandidateID: "c1", Missing: missing},
		{CandidateID: "c2", Missing: missing},
		{CandidateID: "c3", Missing: missing[:1]},
	}
	reasons, truncated := summarizeJevRejections(rejections)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	want := []jevAdmissionReason{
		{Reason: "parameter_not_declared", Parameter: "store", CandidateCount: 3},
		{Reason: "parameter_not_declared", Parameter: "top_p", CandidateCount: 2},
	}
	if len(reasons) != len(want) {
		t.Fatalf("reasons=%+v", reasons)
	}
	for index := range want {
		if reasons[index] != want[index] {
			t.Fatalf("reasons[%d]=%+v want=%+v", index, reasons[index], want[index])
		}
	}

	many := make([]jevCandidateRejection, 0, 12)
	for index := range 12 {
		many = append(many, jevCandidateRejection{Missing: []jevParameterRejection{{
			Reason:    "parameter_not_declared",
			Parameter: "p" + string(rune('a'+index)),
		}}})
	}
	reasons, truncated = summarizeJevRejections(many)
	if !truncated || len(reasons) != jevAdmissionReasonLimit {
		t.Fatalf("cap: truncated=%v len=%d", truncated, len(reasons))
	}
	if reasons[0].Parameter != "pa" || reasons[len(reasons)-1].Parameter != "pj" {
		t.Fatalf("ordering: first=%q last=%q", reasons[0].Parameter, reasons[len(reasons)-1].Parameter)
	}
}

func TestSummarizeJevRejectionsKeepsFatalReasons(t *testing.T) {
	reasons, _ := summarizeJevRejections([]jevCandidateRejection{
		{jevCompatibility: jevReject("model_disabled", "")},
		{jevCompatibility: jevReject("model_disabled", "")},
	})
	if len(reasons) != 1 || reasons[0].Reason != "model_disabled" || reasons[0].CandidateCount != 2 || reasons[0].Parameter != "" {
		t.Fatalf("reasons=%+v", reasons)
	}
}

// TestJevRejectionDetailsStayAnonymous drives the real routing path and checks
// that the public 503 explains the rejection without naming any candidate.
func TestJevRejectionDetailsStayAnonymous(t *testing.T) {
	server, routed, _, _ := jevFixture(t)
	var request ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"auto-chat","messages":[{"role":"user","content":"synthetic coding question"}],"store":false,"top_p":0.5}`), &request); err != nil {
		t.Fatal(err)
	}
	err := server.applySemanticRouting(context.Background(), &routed, request, nil)
	if err == nil {
		t.Fatal("expected jev_no_eligible_candidates")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "jev_no_eligible_candidates" || httpErr.Status != 503 {
		t.Fatalf("code=%s status=%d", httpErr.Code, httpErr.Status)
	}
	details, ok := httpErr.Details.(map[string]any)
	if !ok {
		t.Fatalf("details=%T %+v", httpErr.Details, httpErr.Details)
	}
	if details["stage"] != "jev_admission" || details["upstream_attempted"] != false {
		t.Fatalf("stage details=%+v", details)
	}
	reasons, ok := details["reasons"].([]jevAdmissionReason)
	if !ok || len(reasons) == 0 {
		t.Fatalf("reasons=%+v", details["reasons"])
	}
	byParameter := map[string]jevAdmissionReason{}
	for _, reason := range reasons {
		byParameter[reason.Parameter] = reason
	}
	for _, parameter := range []string{"store", "top_p"} {
		reason, ok := byParameter[parameter]
		if !ok || reason.CandidateCount != 3 || reason.Reason != "parameter_not_declared" {
			t.Fatalf("parameter %q: %+v (all=%+v)", parameter, reason, reasons)
		}
	}
	payload, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, leak := range []string{"model_0", "model_1", "model_2", "provider_", "candidate_id", "route_"} {
		if strings.Contains(string(payload), leak) {
			t.Fatalf("public details leak %q: %s", leak, payload)
		}
	}
}

func TestJevAdmissionCollectsDeniedAndUndeclaredParameters(t *testing.T) {
	model := jevDetailsModel("max_tokens")
	model.Metadata["request_parameter_denied"] = "top_p"
	raw := jevDetailsRaw(t, `{"messages":[],"store":false,"top_p":0.5}`)
	admission := jevCheckAdmission(model, "chat/completions", raw, 10, 0, "task", false)
	want := []jevParameterRejection{
		{Reason: "parameter_not_declared", Parameter: "store"},
		{Reason: "parameter_explicitly_denied", Parameter: "top_p"},
	}
	if len(admission.Missing) != len(want) {
		t.Fatalf("missing=%+v want=%+v", admission.Missing, want)
	}
	for index := range want {
		if admission.Missing[index] != want[index] {
			t.Fatalf("missing[%d]=%+v want=%+v", index, admission.Missing[index], want[index])
		}
	}
	// The established first audit reason stays the earliest rejected field.
	if first := admission.first(); first.Parameter != "store" {
		t.Fatalf("first=%+v", first)
	}
}

// TestJevRejectionDetailsKeepDeniedAndUndeclaredFields drives the HTTP path and
// checks that an explicit deny no longer hides earlier undeclared fields.
func TestJevRejectionDetailsKeepDeniedAndUndeclaredFields(t *testing.T) {
	server, routed, _, _ := jevFixture(t)
	store := server.store.(*GormStore)
	for _, model := range store.ListProviderModels() {
		model.Metadata = cloneStringMap(model.Metadata)
		model.Metadata["request_parameter_denied"] = "top_p"
		if _, err := store.UpdateProviderModel(model.ID, model); err != nil {
			t.Fatal(err)
		}
	}
	var request ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"auto-chat","messages":[{"role":"user","content":"synthetic coding question"}],"store":false,"top_p":0.5}`), &request); err != nil {
		t.Fatal(err)
	}
	err := server.applySemanticRouting(context.Background(), &routed, request, nil)
	if err == nil {
		t.Fatal("expected jev_no_eligible_candidates")
	}
	details, ok := AsHTTPError(err).Details.(map[string]any)
	if !ok {
		t.Fatalf("details=%+v", AsHTTPError(err).Details)
	}
	reasons, ok := details["reasons"].([]jevAdmissionReason)
	if !ok || len(reasons) != 2 {
		t.Fatalf("reasons=%+v", details["reasons"])
	}
	byParameter := map[string]jevAdmissionReason{}
	for _, reason := range reasons {
		byParameter[reason.Parameter] = reason
	}
	if got := byParameter["store"]; got.Reason != "parameter_not_declared" || got.CandidateCount != 3 {
		t.Fatalf("store=%+v (all=%+v)", got, reasons)
	}
	if got := byParameter["top_p"]; got.Reason != "parameter_explicitly_denied" || got.CandidateCount != 3 {
		t.Fatalf("top_p=%+v (all=%+v)", got, reasons)
	}
}
