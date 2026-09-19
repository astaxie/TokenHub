package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewaySystemOnePreservesSDKOptionalAndNullInputs(t *testing.T) {
	for _, explicitNull := range []bool{false, true} {
		name := "omitted optional fields"
		if explicitNull {
			name = "explicit null fields"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					State     json.RawMessage                       `json:"state"`
					Questions map[string]map[string]json.RawMessage `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body.State) != "null" {
					t.Errorf("state=%s", body.State)
				}
				for id, question := range body.Questions {
					value, present := question["instructions"]
					if present != explicitNull || (present && string(value) != "null") {
						t.Errorf("question=%s instructions=%s present=%v", id, value, present)
					}
				}
				value, present := body.Questions["urgent"]["criteria"]
				if present != explicitNull || (present && string(value) != "null") {
					t.Errorf("noul criteria=%s present=%v", value, present)
				}
				writeFixture(t, w, systemOneFixtureResponse)
			})
			req := systemOneTestRequest(t)
			req.State = json.RawMessage(`null`)
			for id, q := range req.Questions {
				q.Instructions = nil
				if explicitNull {
					q.Instructions = json.RawMessage(`null`)
					if q.Type == "noul" {
						q.Criteria = json.RawMessage(`null`)
					}
				}
				req.Questions[id] = q
			}
			response := doGuardrailProtocolRequest(t, server.Handler(), "/v1/systemone", req, "thk_systemone_test")
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			if response.Header().Get("x-typesafe-request-id") != "" {
				t.Fatalf("missing upstream request ID was invented: %v", response.Header())
			}
		})
	}
}

func TestTypeSafeAdapterRequestIDFallback(t *testing.T) {
	for _, requestID := range []string{"", "generic-request-123"} {
		t.Run("fallback "+requestID, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("x-request-id", requestID)
				writeFixture(t, w, systemOneFixtureResponse)
			}))
			defer upstream.Close()
			_, usage, err := (TypeSafeAdapter{Client: upstream.Client()}).SystemOne(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "synthetic-key"}, "jev-latest", systemOneTestRequest(t))
			if err != nil || usage.UpstreamRequestID != requestID {
				t.Fatalf("request ID=%q error=%v", usage.UpstreamRequestID, err)
			}
		})
	}
}
