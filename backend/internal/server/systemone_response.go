package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"tokenhub/backend/internal/metering"
)

func decodeSystemOneResponse(data []byte) (SystemOneResponse, error) {
	var response SystemOneResponse
	if err := json.Unmarshal(data, &response); err != nil {
		// A type error can leave partially assigned token pointers. Read usage
		// independently so valid usage survives malformed answers without inventing tokens.
		response.Usage = SystemOneUsage{}
		var envelope struct {
			Usage json.RawMessage `json:"usage"`
		}
		var usage SystemOneUsage
		if json.Unmarshal(data, &envelope) == nil && json.Unmarshal(envelope.Usage, &usage) == nil {
			response.Usage = usage
		}
		return response, invalidSystemOneResponse()
	}
	return response, nil
}

func (response SystemOneResponse) meteredUsage() Usage {
	usage := Usage{ServedModel: response.Model, MeteringInvalid: true}
	if response.Usage.InputTokens == nil || response.Usage.OutputTokens == nil {
		return usage
	}
	input, output := *response.Usage.InputTokens, *response.Usage.OutputTokens
	if input < 0 || output < 0 || input > math.MaxInt64-output {
		return usage
	}
	usage.MeteringInvalid = false
	usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens = input, output, input+output
	usage.MeteringRaw = &metering.Units{Input: input, Output: output}
	return usage
}

func (response SystemOneResponse) validate(req SystemOneRequest) error {
	return response.validateWithLegendRequest(req, req)
}

func (response SystemOneResponse) validateWithLegendRequest(req, legendRequest SystemOneRequest) error {
	if strings.TrimSpace(response.Model) == "" || response.meteredUsage().MeteringInvalid || len(response.Answers) != len(req.Questions) {
		return invalidSystemOneResponse()
	}
	for id, question := range req.Questions {
		var answer struct {
			Type          string                     `json:"type"`
			Choice        *string                    `json:"choice"`
			Noul          *float64                   `json:"noul"`
			Score         *float64                   `json:"score"`
			Confidence    *float64                   `json:"confidence"`
			Probabilities map[string]*float64        `json:"probabilities"`
			Legend        map[string]json.RawMessage `json:"legend"`
		}
		if err := json.Unmarshal(response.Answers[id], &answer); err != nil || answer.Type != question.Type {
			return invalidSystemOneResponse()
		}
		if question.Type == "noul" {
			if !systemOneNumber(answer.Noul, 0, 1) {
				return invalidSystemOneResponse()
			}
			continue
		}
		if !systemOneNumber(answer.Confidence, 0, 1) {
			return invalidSystemOneResponse()
		}
		var expected map[string]json.RawMessage
		switch question.Type {
		case "choice":
			if err := json.Unmarshal(question.Criteria, &expected); err != nil || answer.Choice == nil {
				return invalidSystemOneResponse()
			}
			if _, ok := expected[*answer.Choice]; !ok {
				return invalidSystemOneResponse()
			}
		case "score":
			var levels []json.RawMessage
			if err := json.Unmarshal(question.Criteria, &levels); err != nil || !systemOneNumber(answer.Score, 0, float64(len(levels)-1)) || len(answer.Legend) != len(levels) {
				return invalidSystemOneResponse()
			}
			var effectiveLevels []json.RawMessage
			effectiveQuestion := legendRequest.Questions[id]
			if effectiveQuestion.Type != "score" || json.Unmarshal(effectiveQuestion.Criteria, &effectiveLevels) != nil || len(effectiveLevels) != len(levels) {
				return invalidSystemOneResponse()
			}
			expected = make(map[string]json.RawMessage, len(levels))
			for index := range levels {
				key := strconv.Itoa(index)
				if value, ok := answer.Legend[key]; !ok || !systemOneEntry(value, true) || !systemOneEntriesEqual(value, effectiveLevels[index]) {
					return invalidSystemOneResponse()
				}
				expected[key] = nil
			}
		}
		if len(answer.Probabilities) != len(expected) {
			return invalidSystemOneResponse()
		}
		var sum float64
		for key := range expected {
			value := answer.Probabilities[key]
			if !systemOneNumber(value, 0, 1) {
				return invalidSystemOneResponse()
			}
			sum += *value
		}
		// Keep the one-percentage-point tolerance inclusive despite floating-point rounding.
		const roundingEpsilon = 1e-12
		if math.Abs(sum-1) > 0.01+roundingEpsilon {
			return invalidSystemOneResponse()
		}
		if question.Type == "score" {
			var expectedScore float64
			for index := 0; index < len(expected); index++ {
				expectedScore += float64(index) * *answer.Probabilities[strconv.Itoa(index)]
			}
			// Gateway policy: allow one percent of the score range for reported rounding.
			// This is not an upstream precision guarantee; do not renormalize probabilities.
			if math.Abs(*answer.Score-expectedScore) > (0.01+roundingEpsilon)*float64(len(expected)-1) {
				return invalidSystemOneResponse()
			}
		} else if question.Type == "choice" {
			chosen := *answer.Probabilities[*answer.Choice]
			for _, probability := range answer.Probabilities {
				if *probability-chosen > 0.01+roundingEpsilon {
					return invalidSystemOneResponse()
				}
			}
		}
	}
	return nil
}

func systemOneNumber(value *float64, low, high float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= low && *value <= high
}

func invalidSystemOneResponse() error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Provider returned an invalid System One response"),
		Disposition: ProviderErrorTransientSame,
	}
}
