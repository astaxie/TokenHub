package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

const systemOneFixtureRequest = `{"model":"jev-test","state":{"messages":["Please refund this order"],"order_id":9007199254740993},"questions":{"intent":{"type":"choice","instructions":"Classify intent","criteria":{"refund":"Return funds","other":null}},"urgent":{"type":"noul","instructions":"Is immediate attention required?"},"severity":{"type":"score","instructions":null,"criteria":["low","high"]}}}`
const systemOneFixtureResponse = `{"model":"jev-1.13.0","answers":{"intent":{"type":"choice","choice":"refund","probabilities":{"refund":0.51,"other":0.49},"confidence":0.01},"urgent":{"type":"noul","noul":0.25},"severity":{"type":"score","score":0.75,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.25,"1":0.75},"confidence":0.5}},"usage":{"input_tokens":422,"output_tokens":69}}`

func systemOneTestRequest(t *testing.T) SystemOneRequest {
	t.Helper()
	var request SystemOneRequest
	if err := json.Unmarshal([]byte(systemOneFixtureRequest), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestSystemOneRequestValidation(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*SystemOneRequest)
		valid bool
	}{
		{"all primitives", func(*SystemOneRequest) {}, true},
		{"string state", func(r *SystemOneRequest) { r.State = json.RawMessage(`"hello"`) }, true},
		{"array state", func(r *SystemOneRequest) { r.State = json.RawMessage(`[null,42,true,{"text":"hi"}]`) }, true},
		{"missing model", func(r *SystemOneRequest) { r.Model = " " }, false},
		{"null state", func(r *SystemOneRequest) { r.State = json.RawMessage(`null`) }, true},
		{"missing state", func(r *SystemOneRequest) { r.State = nil }, false},
		{"boolean state", func(r *SystemOneRequest) { r.State = json.RawMessage(`false`) }, false},
		{"numeric state", func(r *SystemOneRequest) { r.State = json.RawMessage(`42`) }, false},
		{"missing questions", func(r *SystemOneRequest) { r.Questions = nil }, false},
		{"omitted noul instructions", func(r *SystemOneRequest) { r.Questions["urgent"] = SystemOneQuestion{Type: "noul"} }, true},
		{"null noul criteria", func(r *SystemOneRequest) {
			r.Questions["urgent"] = SystemOneQuestion{Type: "noul", Criteria: json.RawMessage(`null`)}
		}, true},
		{"omitted choice instructions", func(r *SystemOneRequest) {
			q := r.Questions["intent"]
			q.Instructions = nil
			r.Questions["intent"] = q
		}, true},
		{"omitted score instructions", func(r *SystemOneRequest) {
			q := r.Questions["severity"]
			q.Instructions = nil
			r.Questions["severity"] = q
		}, true},
		{"null choice criteria", func(r *SystemOneRequest) {
			q := r.Questions["intent"]
			q.Criteria = json.RawMessage(`null`)
			r.Questions["intent"] = q
		}, false},
		{"null score criteria", func(r *SystemOneRequest) {
			q := r.Questions["severity"]
			q.Criteria = json.RawMessage(`null`)
			r.Questions["severity"] = q
		}, false},
		{"unknown primitive", func(r *SystemOneRequest) {
			r.Questions["urgent"] = SystemOneQuestion{Type: "chat", Instructions: json.RawMessage(`null`)}
		}, false},
		{"single score level", func(r *SystemOneRequest) {
			q := r.Questions["severity"]
			q.Criteria = json.RawMessage(`["low"]`)
			r.Questions["severity"] = q
		}, false},
		{"invalid noul label", func(r *SystemOneRequest) {
			q := r.Questions["urgent"]
			q.Criteria = json.RawMessage(`{"yes":null}`)
			r.Questions["urgent"] = q
		}, false},
		{"empty choice", func(r *SystemOneRequest) {
			q := r.Questions["intent"]
			q.Criteria = json.RawMessage(`{}`)
			r.Questions["intent"] = q
		}, false},
		{"nested state limit", func(r *SystemOneRequest) {
			r.State = json.RawMessage(strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64))
		}, true},
		{"excessive nesting", func(r *SystemOneRequest) {
			r.State = json.RawMessage(strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65))
		}, false},
		{"too many questions", func(r *SystemOneRequest) {
			for i := 0; i < systemOneMaxQuestions; i++ {
				r.Questions[strings.Repeat("x", i+1)] = r.Questions["urgent"]
			}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := systemOneTestRequest(t)
			tt.alter(&r)
			if err := r.validate(); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
	for _, payload := range []string{strings.Replace(systemOneFixtureRequest, `"state":`, `"stream":true,"state":`, 1), strings.Replace(systemOneFixtureRequest, `"type":"noul"`, `"type":"noul","extra":true`, 1)} {
		var r SystemOneRequest
		if err := json.Unmarshal([]byte(payload), &r); err == nil {
			t.Fatal("unknown request field was accepted")
		}
	}
}

func TestSystemOneResponseValidation(t *testing.T) {
	tests := []struct{ name, old, next string }{
		{"missing answer", `"urgent":{"type":"noul","noul":0.25},`, ``},
		{"invalid answer type", `"type":"noul"`, `"type":"chat"`},
		{"invalid noul", `"noul":0.25`, `"noul":1.01`},
		{"invalid score", `"score":0.75`, `"score":2`},
		{"invalid choice", `"choice":"refund"`, `"choice":"unknown"`},
		{"missing confidence", `,"confidence":0.01`, ``},
		{"invalid probabilities", `"refund":0.51`, `"refund":0.1`},
		{"missing usage", `"input_tokens":422,`, ``},
		{"negative usage", `"input_tokens":422`, `"input_tokens":-1`},
		{"overflow usage", `"input_tokens":422`, `"input_tokens":9223372036854775807`},
	}
	req := systemOneTestRequest(t)
	var response SystemOneResponse
	if err := json.Unmarshal([]byte(systemOneFixtureResponse), &response); err != nil {
		t.Fatal(err)
	}
	if err := response.validate(req); err != nil {
		t.Fatalf("valid low-confidence response rejected: %v", err)
	}
	usage := response.meteredUsage()
	if usage.PromptTokens != 422 || usage.CompletionTokens != 69 || usage.TotalTokens != 491 || usage.MeteringInvalid {
		t.Fatalf("usage=%+v", usage)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r SystemOneResponse
			if err := json.Unmarshal([]byte(strings.Replace(systemOneFixtureResponse, tt.old, tt.next, 1)), &r); err != nil {
				t.Fatal(err)
			}
			if err := r.validate(req); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	nonfinite := math.Inf(1)
	if systemOneNumber(&nonfinite, 0, 1) {
		t.Fatal("nonfinite value accepted")
	}
}

func TestSystemOneResponseProbabilityTolerance(t *testing.T) {
	for _, primitive := range []struct{ name, old, format string }{
		{"choice", `"refund":0.51,"other":0.49`, `"refund":%s,"other":0.5`},
		{"score", `"0":0.25,"1":0.75`, `"0":%s,"1":0.5`},
	} {
		t.Run(primitive.name, func(t *testing.T) {
			for _, tt := range []struct {
				name, probability string
				valid             bool
			}{
				{"sum one", "0.5", true},
				{"lower boundary", "0.49", true},
				{"upper boundary", "0.51", true},
				{"below lower boundary", "0.489999999", false},
				{"above upper boundary", "0.510000001", false},
			} {
				t.Run(tt.name, func(t *testing.T) {
					payload := strings.Replace(systemOneFixtureResponse, primitive.old, fmt.Sprintf(primitive.format, tt.probability), 1)
					if primitive.name == "score" {
						payload = strings.Replace(payload, `"score":0.75`, `"score":0.5`, 1)
					}
					var response SystemOneResponse
					if err := json.Unmarshal([]byte(payload), &response); err != nil {
						t.Fatal(err)
					}
					if err := response.validate(systemOneTestRequest(t)); (err == nil) != tt.valid {
						t.Fatalf("valid=%v error=%v", tt.valid, err)
					}
				})
			}
		})
	}
}

func TestSystemOneScoreLegendValues(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		valid       bool
	}{
		{"string", `"description"`, true},
		{"empty string", `""`, true},
		{"object", `{"nested":[42,true,null]}`, true},
		{"array", `[42,true,"text"]`, true},
		{"null", `null`, true},
		{"number", `42`, false},
		{"boolean", `false`, false},
		{"nesting boundary", strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64), true},
		{"excessive nesting", strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for index, entry := range []struct{ key, old string }{{"0", `"0":"low"`}, {"1", `"1":"high"`}} {
				t.Run(entry.key, func(t *testing.T) {
					payload := strings.Replace(systemOneFixtureResponse, entry.old, fmt.Sprintf("%q:%s", entry.key, tt.value), 1)
					var response SystemOneResponse
					if err := json.Unmarshal([]byte(payload), &response); err != nil {
						if tt.valid {
							t.Fatal(err)
						}
						return
					}
					req := systemOneTestRequest(t)
					if tt.valid {
						levels := []json.RawMessage{json.RawMessage(`"low"`), json.RawMessage(`"high"`)}
						levels[index] = json.RawMessage(tt.value)
						question := req.Questions["severity"]
						question.Criteria, _ = json.Marshal(levels)
						req.Questions["severity"] = question
					}
					if err := response.validate(req); (err == nil) != tt.valid {
						t.Fatalf("valid=%v error=%v", tt.valid, err)
					}
				})
			}
		})
	}
}

func TestSystemOneGuardrailRedactionPreservesNumbersAndInspectsRubrics(t *testing.T) {
	req := systemOneTestRequest(t)
	batch, err := systemOneGuardrailTargets(&req)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range batch.targets {
		if target.fragment.Text == "Return funds" {
			target.replace("[masked]")
		}
		if target.fragment.Text == "Please refund this order" {
			target.replace("[masked state]")
		}
	}
	if err := batch.apply(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `9007199254740993`) || strings.Contains(string(data), "Return funds") || strings.Contains(string(data), "Please refund") {
		t.Fatalf("redacted request=%s", data)
	}
	batch, err = systemOneGuardrailTargets(&req)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range batch.targets {
		if target.fragment.Text == "refund" {
			target.replace("hidden")
		}
	}
	if !req.unsafeRedaction {
		t.Fatal("structural label redaction must fail closed")
	}
}
