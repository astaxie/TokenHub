package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"tokenhub/backend/internal/guardrails"
)

func TestGatewaySystemOneNumericSensitiveData(t *testing.T) {
	for _, action := range []string{guardrails.ActionBlock, guardrails.ActionMask} {
		for _, location := range []string{"state", "instructions", "choice criteria", "noul criteria", "score criteria"} {
			for _, number := range []string{"13812345678", "1.3812345678e10"} {
				t.Run(action+"/"+location+"/"+number, func(t *testing.T) {
					var calls atomic.Int32
					server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						writeFixture(t, w, systemOneFixtureResponse)
					})
					if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
						Name:           "Protect numeric phone numbers",
						DetectionItems: []guardrails.DetectionItem{{Name: "Phone", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"phone"}}}},
						Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
					}); err != nil {
						t.Fatal(err)
					}
					req := systemOneTestRequest(t)
					value := `{"nested":[{"contact":` + number + `}]}`
					switch location {
					case "state":
						req.State = json.RawMessage(value)
					case "instructions":
						q := req.Questions["urgent"]
						q.Instructions = json.RawMessage(value)
						req.Questions["urgent"] = q
					default:
						id, criteria := "intent", `{"refund":`+value+`,"other":null}`
						if location == "noul criteria" {
							id, criteria = "urgent", `{"true":`+value+`}`
						} else if location == "score criteria" {
							id, criteria = "severity", `[`+value+`,"high"]`
						}
						q := req.Questions[id]
						q.Criteria = json.RawMessage(criteria)
						req.Questions[id] = q
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", req, "thk_systemone_test")
					if response.Code != http.StatusForbidden || calls.Load() != 0 || !strings.Contains(response.Body, `"guardrail_blocked"`) {
						t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
					}
				})
			}
		}
	}
}

func TestGatewaySystemOneNumericPrecisionAfterMasking(t *testing.T) {
	const state = `{"id":9007199254740993,"fraction":0.123456789012345678901,"exponent":1e999999999999999999999999,"zero":-0,"contact":"13812345678"}`
	var calls atomic.Int32
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		var got, original map[string]json.RawMessage
		if err := json.Unmarshal(req.State, &got); err != nil {
			t.Error(err)
		}
		if err := json.Unmarshal([]byte(state), &original); err != nil {
			t.Fatal(err)
		}
		for key, want := range original {
			if key == "contact" {
				want = json.RawMessage(`"[REDACTED]"`)
			}
			if string(got[key]) != string(want) {
				t.Errorf("%s=%s want=%s", key, got[key], want)
			}
		}
		writeFixture(t, w, systemOneFixtureResponse)
	})
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name:           "Mask string phone numbers",
		DetectionItems: []guardrails.DetectionItem{{Name: "Phone", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask, Config: map[string]any{"data_types": []string{"phone"}}}},
		Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	req := systemOneTestRequest(t)
	req.State = json.RawMessage(state)
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", req, "thk_systemone_test")
	if response.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
	}
}

func TestSystemOneGuardrailIntegerText(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"scientific phone", "1.3812345678e10", "13812345678"},
		{"decimal shift", "0.000013812345678e15", "13812345678"},
		{"negative exponent", "13812345678000e-3", "13812345678"},
		{"negative value", "-1.3812345678E+10", "-13812345678"},
		{"decimal integer", "13812345678.000", "13812345678"},
		{"fraction", "1.3812345678e-10", "1.3812345678e-10"},
		{"signed zero", "-0.00e10", "0"},
		{"maximum expansion", "1e63", "1" + strings.Repeat("0", 63)},
		{"oversized expansion", "1e64", "1e64"},
		{"positive overflow", "1e9223372036854775807", "1e9223372036854775807"},
		{"negative overflow", "1e-9223372036854775808", "1e-9223372036854775808"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := systemOneGuardrailIntegerText(json.Number(tc.input)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	// Large exponents must neither allocate their numeric value nor lose
	// equivalent phone numbers behind arbitrarily many leading zeros.
	for _, exponent := range []string{strings.Repeat("9", 1_000_000), "-" + strings.Repeat("9", 1_000_000)} {
		input := "1e" + exponent
		if got := systemOneGuardrailIntegerText(json.Number(input)); got != input {
			t.Fatal("oversized exponent was expanded")
		}
	}
	input := "1.3812345678e+" + strings.Repeat("0", 1_000_000) + "10"
	if got := systemOneGuardrailIntegerText(json.Number(input)); got != "13812345678" {
		t.Fatal("leading-zero exponent bypassed integer inspection")
	}
}
