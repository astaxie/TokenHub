package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestSystemOneScoreProbabilityConsistency(t *testing.T) {
	for _, tt := range []struct {
		name            string
		levels          int
		score           float64
		lastProbability float64
		valid           bool
	}{
		{"exact expectation", 2, .5, .5, true},
		{"contradictory expectation", 2, 0, 1, false},
		{"lower tolerance boundary", 2, .49, .5, true},
		{"upper tolerance boundary", 2, .51, .5, true},
		{"below lower tolerance", 2, .489999999, .5, false},
		{"above upper tolerance", 2, .510000001, .5, false},
		{"numeric indices", 11, 10, 1, true},
		{"wrong multi-digit index", 11, 1, 1, false},
		{"maximum levels", 4096, 4095, 1, true},
		{"maximum levels scaled tolerance", 4096, 4054.05, 1, true},
		{"maximum levels outside tolerance", 4096, 4054.04999, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := systemOneTestRequest(t)
			levels := make([]json.RawMessage, tt.levels)
			probabilities := make(map[string]float64, tt.levels)
			for i := range levels {
				levels[i] = json.RawMessage(`null`)
				probabilities[strconv.Itoa(i)] = 0
			}
			probabilities["0"] = 1 - tt.lastProbability
			probabilities[strconv.Itoa(tt.levels-1)] = tt.lastProbability
			q := req.Questions["severity"]
			q.Criteria, _ = json.Marshal(levels)
			req.Questions["severity"] = q
			response := systemOneResponseForCriteria(t, req)
			var answer map[string]json.RawMessage
			if err := json.Unmarshal(response.Answers["severity"], &answer); err != nil {
				t.Fatal(err)
			}
			answer["score"], _ = json.Marshal(tt.score)
			answer["probabilities"], _ = json.Marshal(probabilities)
			response.Answers["severity"], _ = json.Marshal(answer)
			if err := response.validate(req); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
}

func TestSystemOneChoiceProbabilityConsistency(t *testing.T) {
	for _, tt := range []struct {
		name, choice string
		probability  float64
		valid        bool
	}{
		{"winner", "refund", 1, true},
		{"contradictory choice", "other", 1, false},
		{"first tied maximum", "refund", .5, true},
		{"second tied maximum", "other", .5, true},
		{"inclusive tolerance", "refund", .495, true},
		{"outside tolerance", "refund", .494999999, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var response SystemOneResponse
			if err := json.Unmarshal([]byte(systemOneFixtureResponse), &response); err != nil {
				t.Fatal(err)
			}
			response.Answers["intent"], _ = json.Marshal(map[string]any{"type": "choice", "choice": tt.choice, "probabilities": map[string]float64{"refund": tt.probability, "other": 1 - tt.probability}, "confidence": .5})
			if err := response.validate(systemOneTestRequest(t)); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
}

func TestGatewaySystemOneRejectsInconsistentProbabilities(t *testing.T) {
	for _, tt := range []struct{ name, old, next string }{
		{"score", `"score":0.75`, `"score":0`},
		{"choice", `"refund":0.51,"other":0.49`, `"refund":0,"other":1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeFixture(t, w, strings.Replace(systemOneFixtureResponse, tt.old, tt.next, 1))
			})
			emitter := &recordingTraceEmitter{}
			server.traceEmitter = emitter
			response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "provider_invalid_response") || strings.Contains(response.Body, "probabilities") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			completions := emitter.take()
			if len(completions) != 1 || len(completions[0].Attempts) != 1 || completions[0].Attempts[0].Usage.TotalTokens != 491 {
				t.Fatalf("valid usage from the rejected provider attempt was lost: %+v", completions)
			}
		})
	}
}
