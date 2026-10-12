package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemOneScoreLegendMatchesCriteria(t *testing.T) {
	for _, tt := range []struct {
		name, criterion, legend string
		valid                   bool
	}{
		{"changed string", `"low"`, `"different"`, false},
		{"changed value type", `"low"`, `null`, false},
		{"object order and whitespace", `{"a":1,"b":[true,null]}`, ` {"b":[true,null], "a":1.0} `, true},
		{"escaped string", `"low"`, `"l\u006fw"`, true},
		{"nested numeric representations", `[1,-2.5,0]`, `[1.00,-25e-1,-0.0]`, true},
		{"large integer preserved", `[9007199254740993]`, `[9007199254740993.0]`, true},
		{"large integer changed", `[9007199254740993]`, `[9007199254740992]`, false},
		{"array order changed", `["a","b"]`, `["b","a"]`, false},
		{"numeric string differs", `[1]`, `["1"]`, false},
		{"missing object field", `{"a":null}`, `{}`, false},
		{"nested value changed", `{"a":[1]}`, `{"a":[2]}`, false},
		{"large exponent equivalence", `[1e100000000000000000000]`, `[10e99999999999999999999]`, true},
		{"negative exponent equivalence", `[1e-100000000000000000000]`, `[10e-100000000000000000001]`, true},
		{"different exponent", `[1e100000000000000000000]`, `[1e100000000000000000001]`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := systemOneTestRequest(t)
			q := req.Questions["severity"]
			q.Criteria = json.RawMessage(`[` + tt.criterion + `,"high"]`)
			req.Questions["severity"] = q
			if err := req.validate(); err != nil {
				t.Fatal(err)
			}
			var response SystemOneResponse
			if err := json.Unmarshal([]byte(strings.Replace(systemOneFixtureResponse, `"0":"low"`, `"0":`+tt.legend, 1)), &response); err != nil {
				t.Fatal(err)
			}
			if err := response.validate(req); (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
	t.Run("reversed legend", func(t *testing.T) {
		var response SystemOneResponse
		if err := json.Unmarshal([]byte(strings.Replace(systemOneFixtureResponse, `"0":"low","1":"high"`, `"0":"high","1":"low"`, 1)), &response); err != nil {
			t.Fatal(err)
		}
		if err := response.validate(systemOneTestRequest(t)); err == nil {
			t.Fatal("reversed legend accepted")
		}
	})
}

func TestSystemOneEffectiveCriteriaPreservesAnswerStructure(t *testing.T) {
	for _, change := range []string{"question id", "answer type", "choice labels", "score level count"} {
		t.Run(change, func(t *testing.T) {
			req, effective := systemOneTestRequest(t), systemOneTestRequest(t)
			var response SystemOneResponse
			if err := json.Unmarshal([]byte(systemOneFixtureResponse), &response); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "question id":
				effective.Questions["renamed"] = effective.Questions["urgent"]
				delete(effective.Questions, "urgent")
				response.Answers["renamed"] = response.Answers["urgent"]
				delete(response.Answers, "urgent")
			case "answer type":
				effective.Questions["intent"] = SystemOneQuestion{Type: "noul"}
				response.Answers["intent"] = json.RawMessage(`{"type":"noul","noul":0.5}`)
			case "choice labels":
				q := effective.Questions["intent"]
				q.Criteria = json.RawMessage(`{"changed":null}`)
				effective.Questions["intent"] = q
				response.Answers["intent"] = json.RawMessage(`{"type":"choice","choice":"changed","probabilities":{"changed":1},"confidence":1}`)
			case "score level count":
				q := effective.Questions["severity"]
				q.Criteria = json.RawMessage(`["low","medium","high"]`)
				effective.Questions["severity"] = q
				response = systemOneResponseForCriteria(t, effective)
			}
			if err := response.validate(effective); err != nil {
				t.Fatalf("effective response is invalid: %v", err)
			}
			if err := response.validateWithLegendRequest(req, effective); err == nil {
				t.Fatal("structural answer change was accepted")
			}
		})
	}
}
