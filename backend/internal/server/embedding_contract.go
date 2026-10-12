package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// EmbeddingsRequest keeps semantic options across gateway hooks and adapters.
// Unknown fields are rejected instead of silently changing the vector contract.
type EmbeddingsRequest struct {
	Stream         bool   `json:"stream,omitempty"`
	Model          string `json:"model"`
	Input          any    `json:"input"`
	Dimensions     *int   `json:"dimensions,omitempty"`
	EncodingFormat string `json:"encoding_format,omitempty"`
	InputType      string `json:"input_type,omitempty"`
	Task           string `json:"task,omitempty"`
	Normalized     *bool  `json:"normalized,omitempty"`
	Truncation     *bool  `json:"truncation,omitempty"`
	LateChunking   *bool  `json:"late_chunking,omitempty"`
	User           string `json:"user,omitempty"`
}

func (r *EmbeddingsRequest) UnmarshalJSON(data []byte) error {
	type wire EmbeddingsRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*r = EmbeddingsRequest(value)
	return nil
}

func embeddingRequestError(message string) error {
	return NewHTTPError(400, "invalid_embedding_request", message)
}

// embeddingInputCount distinguishes independent inputs from tokenized text.
func embeddingInputCount(input any) (int, bool, error) {
	switch values := input.(type) {
	case string:
		if strings.TrimSpace(values) == "" {
			return 0, false, embeddingRequestError("input must not be empty")
		}
		return 1, false, nil
	case []string:
		if len(values) == 0 {
			return 0, false, embeddingRequestError("input must not be empty")
		}
		for _, v := range values {
			if strings.TrimSpace(v) == "" {
				return 0, false, embeddingRequestError("input contains empty text")
			}
		}
		return len(values), false, nil
	case []int:
		if len(values) == 0 {
			return 0, true, embeddingRequestError("input must not be empty")
		}
		for _, v := range values {
			if v < 0 {
				return 0, true, embeddingRequestError("token IDs must be nonnegative integers")
			}
		}
		return 1, true, nil
	case []any:
		if len(values) == 0 {
			return 0, false, embeddingRequestError("input must not be empty")
		}
		if _, ok := values[0].(float64); ok {
			for _, v := range values {
				n, ok := v.(float64)
				if !ok || n < 0 || math.Trunc(n) != n || n > math.MaxInt32 {
					return 0, true, embeddingRequestError("token IDs must be nonnegative integers")
				}
			}
			return 1, true, nil
		}
		kind := ""
		for _, v := range values {
			k := "text"
			switch v.(type) {
			case string:
			case []any, []int:
				k = "tokens"
			default:
				return 0, false, embeddingRequestError("only text or token IDs are supported")
			}
			if kind != "" && kind != k {
				return 0, false, embeddingRequestError("input types must not be mixed")
			}
			kind = k
			count, tokens, err := embeddingInputCount(v)
			if err != nil {
				return 0, false, err
			}
			if count != 1 || (k == "tokens" && !tokens) {
				return 0, false, embeddingRequestError("nested text batches are unsupported")
			}
		}
		return len(values), kind == "tokens", nil
	default:
		return 0, false, embeddingRequestError("only text or token IDs are supported")
	}
}

func validateEmbeddingRequest(r EmbeddingsRequest) error {
	if r.Stream {
		return embeddingRequestError("streaming embeddings are unsupported")
	}
	if strings.TrimSpace(r.Model) == "" {
		return embeddingRequestError("model is required")
	}
	count, _, err := embeddingInputCount(r.Input)
	if err != nil {
		return err
	}
	if count > 2048 {
		return embeddingRequestError("input exceeds the gateway batch limit of 2048")
	}
	if r.Dimensions != nil && (*r.Dimensions < 1 || *r.Dimensions > 65536) {
		return embeddingRequestError("dimensions must be between 1 and 65536")
	}
	if r.EncodingFormat != "" && r.EncodingFormat != "float" && r.EncodingFormat != "base64" {
		return embeddingRequestError("encoding_format must be float or base64")
	}
	if r.InputType != "" && r.InputType != "query" && r.InputType != "document" {
		return embeddingRequestError("input_type must be query or document")
	}
	if r.Task != "" && r.InputType != "" {
		return embeddingRequestError("specify either task or input_type")
	}
	return nil
}

func embeddingTexts(input any) ([]string, error) {
	_, tokens, err := embeddingInputCount(input)
	if err != nil {
		return nil, err
	}
	if tokens {
		return nil, embeddingRequestError("this protocol requires text rather than token IDs")
	}
	switch value := input.(type) {
	case string:
		return []string{value}, nil
	case []string:
		return value, nil
	case []any:
		result := make([]string, len(value))
		for i, v := range value {
			result[i] = v.(string)
		}
		return result, nil
	}
	return nil, embeddingRequestError("invalid text input")
}

func embeddingVector(raw any) ([]float32, error) {
	fail := func() ([]float32, error) {
		return nil, NewHTTPError(502, "invalid_embedding_response", "Upstream returned an invalid dense vector")
	}
	if encoded, ok := raw.(string); ok {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(data) == 0 || len(data)%4 != 0 {
			return fail()
		}
		vector := make([]float32, len(data)/4)
		for i := range vector {
			vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
			if math.IsNaN(float64(vector[i])) || math.IsInf(float64(vector[i]), 0) {
				return fail()
			}
		}
		return vector, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fail()
	}
	if bytes.Contains(data, []byte("null")) {
		return fail()
	}
	var vector []float32
	if json.Unmarshal(data, &vector) != nil || len(vector) == 0 {
		return fail()
	}
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fail()
		}
	}
	return vector, nil
}

func encodeEmbeddingVector(vector []float32, format string) any {
	if format != "base64" {
		return vector
	}
	data := make([]byte, len(vector)*4)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(data)
}

func normalizeEmbeddingResponse(body map[string]any, req EmbeddingsRequest) (map[string]any, error) {
	count, _, err := embeddingInputCount(req.Input)
	if err != nil {
		return nil, err
	}
	raw, ok := body["data"].([]any)
	if !ok {
		data, e := json.Marshal(body["data"])
		if e == nil {
			_ = json.Unmarshal(data, &raw)
		}
	}
	fail := func() (map[string]any, error) {
		return nil, NewHTTPError(502, "invalid_embedding_response", "Upstream embedding count, index or dimension does not match the request")
	}
	if len(raw) != count {
		return fail()
	}
	result := make([]any, count)
	dimension := 0
	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			return fail()
		}
		idx := -1
		switch n := item["index"].(type) {
		case float64:
			if math.Trunc(n) == n && n >= 0 && n < float64(count) {
				idx = int(n)
			}
		case int:
			idx = n
		}
		if idx < 0 || idx >= count || result[idx] != nil {
			return fail()
		}
		vector, e := embeddingVector(item["embedding"])
		if e != nil {
			return nil, e
		}
		if dimension == 0 {
			dimension = len(vector)
		}
		if len(vector) != dimension || (req.Dimensions != nil && len(vector) != *req.Dimensions) {
			return fail()
		}
		result[idx] = map[string]any{"object": "embedding", "index": idx, "embedding": encodeEmbeddingVector(vector, req.EncodingFormat)}
	}
	normalized := map[string]any{"object": "list", "model": req.Model, "data": result}
	if usage, ok := body["usage"]; ok {
		normalized["usage"] = usage
	}
	return normalized, nil
}

func embeddingUnsupportedParameter(profile, field string) error {
	return embeddingRequestError(fmt.Sprintf("%s is not supported by the %s embedding protocol", field, profile))
}
