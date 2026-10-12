package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func providerEmbeddingProtocol(p Provider) string {
	if v := strings.TrimSpace(p.Options["embedding_protocol"]); v != "" {
		return v
	}
	switch p.Options["catalog_id"] {
	case "cohere":
		return "cohere"
	case "voyage", "voyageai":
		return "voyage"
	case "jina":
		return "jina"
	}
	return "openai"
}
func providerEmbeddingPath(p Provider) string {
	if path := strings.TrimSpace(p.Options["embedding_path"]); path != "" {
		return path
	}
	switch providerEmbeddingProtocol(p) {
	case "cohere", "tei":
		return "/embed"
	case "dashscope":
		return "/services/embeddings/text-embedding/text-embedding"
	default:
		return "/embeddings"
	}
}
func embeddingTask(r EmbeddingsRequest, p string) string {
	if r.Task != "" {
		return r.Task
	}
	if r.InputType == "" {
		return ""
	}
	switch p {
	case "cohere":
		if r.InputType == "query" {
			return "search_query"
		}
		return "search_document"
	case "jina":
		if r.InputType == "query" {
			return "retrieval.query"
		}
		return "retrieval.passage"
	case "gemini":
		if r.InputType == "query" {
			return "RETRIEVAL_QUERY"
		}
		return "RETRIEVAL_DOCUMENT"
	}
	return r.InputType
}
func embeddingPayload(p Provider, model string, r EmbeddingsRequest) (string, any, error) {
	profile := providerEmbeddingProtocol(p)
	up := r
	up.Model = model
	up.EncodingFormat = "float"
	body := map[string]any{}
	raw, err := json.Marshal(up)
	if err != nil {
		return "", nil, err
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return "", nil, err
	}
	path := providerEmbeddingPath(p)
	if profile != "jina" && (r.Normalized != nil || r.LateChunking != nil) {
		return "", nil, embeddingUnsupportedParameter(profile, "normalized/late_chunking")
	}
	switch profile {
	case "openai":
		if r.InputType != "" || r.Task != "" || r.Truncation != nil {
			return "", nil, embeddingUnsupportedParameter(profile, "input_type/task/truncation")
		}
	case "jina":
		delete(body, "encoding_format")
		body["embedding_type"] = "float"
		delete(body, "input_type")
		if task := embeddingTask(r, profile); task != "" {
			body["task"] = task
		}
		if r.Truncation != nil {
			return "", nil, embeddingUnsupportedParameter(profile, "truncation")
		}
	case "voyage":
		delete(body, "encoding_format")
		body["output_dtype"] = "float"
		if r.User != "" {
			return "", nil, embeddingUnsupportedParameter(profile, "user")
		}
		if r.Task != "" {
			return "", nil, embeddingUnsupportedParameter(profile, "task")
		}
		delete(body, "dimensions")
		if r.Dimensions != nil {
			body["output_dimension"] = *r.Dimensions
		}
	case "cohere", "dashscope", "tei":
		texts, e := embeddingTexts(r.Input)
		if e != nil {
			return "", nil, e
		}
		body = map[string]any{}
		if r.User != "" {
			return "", nil, embeddingUnsupportedParameter(profile, "user")
		}
		switch profile {
		case "cohere":
			body["model"] = model
			body["texts"] = texts
			body["embedding_types"] = []string{"float"}
			task := embeddingTask(r, profile)
			if task == "" {
				return "", nil, embeddingRequestError("Cohere requires input_type or task")
			}
			body["input_type"] = task
			if r.Dimensions != nil {
				body["output_dimension"] = *r.Dimensions
			}
			if r.Truncation != nil {
				if *r.Truncation {
					body["truncate"] = "END"
				} else {
					body["truncate"] = "NONE"
				}
			}
		case "dashscope":
			if len(texts) > 10 {
				return "", nil, embeddingRequestError("DashScope native text embeddings accept at most 10 inputs per request")
			}
			body["model"] = model
			body["input"] = map[string]any{"texts": texts}
			params := map[string]any{"output_type": "dense"}
			if r.Dimensions != nil {
				params["dimension"] = *r.Dimensions
			}
			if task := embeddingTask(r, profile); task != "" {
				params["text_type"] = task
			}
			body["parameters"] = params
			if r.Truncation != nil {
				return "", nil, embeddingUnsupportedParameter(profile, "truncation")
			}
		case "tei":
			body["inputs"] = texts
			if r.Dimensions != nil {
				body["dimensions"] = *r.Dimensions
			}
			if r.Truncation != nil {
				body["truncate"] = *r.Truncation
			}
			if r.InputType != "" || r.Task != "" {
				return "", nil, embeddingUnsupportedParameter(profile, "input_type/task")
			}
		}
	default:
		return "", nil, embeddingRequestError("unknown embedding_protocol")
	}
	if v := strings.TrimSpace(p.Options["embedding_path"]); v != "" {
		if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.ContainsAny(v, "?#") || strings.Contains(v, "..") {
			return "", nil, embeddingRequestError("embedding_path must be an absolute path without query or traversal")
		}
		path = v
	}
	return path, body, nil
}
func embeddingNativeResponse(raw any, profile string) (map[string]any, error) {
	body, ok := raw.(map[string]any)
	if !ok {
		if profile != "tei" {
			return nil, NewHTTPError(502, "invalid_embedding_response", "Expected an embedding response object")
		}
		body = map[string]any{}
	}
	var vectors []any
	switch profile {
	case "cohere":
		emb, _ := body["embeddings"].(map[string]any)
		vectors, _ = emb["float"].([]any)
		if meta, ok := body["meta"].(map[string]any); ok {
			if billed, ok := meta["billed_units"].(map[string]any); ok {
				if n, exists := billed["input_tokens"]; exists {
					body["usage"] = map[string]any{"prompt_tokens": n, "total_tokens": n}
				}
			}
		}
	case "dashscope":
		output, _ := body["output"].(map[string]any)
		items, _ := output["embeddings"].([]any)
		data := make([]any, 0, len(items))
		for _, v := range items {
			item, ok := v.(map[string]any)
			if !ok {
				return nil, NewHTTPError(502, "invalid_embedding_response", "Invalid native embedding item")
			}
			data = append(data, map[string]any{"index": item["text_index"], "embedding": item["embedding"]})
		}
		body["data"] = data
	case "tei":
		vectors, _ = raw.([]any)
	}
	if profile == "cohere" || profile == "tei" {
		data := make([]any, len(vectors))
		for i, v := range vectors {
			data[i] = map[string]any{"index": i, "embedding": v}
		}
		body["data"] = data
	}
	return body, nil
}
func (c openAICompatibleCore) textEmbeddings(ctx context.Context, p Provider, model string, r EmbeddingsRequest) (any, Usage, error) {
	if err := validateEmbeddingRequest(r); err != nil {
		return nil, Usage{}, err
	}
	path, payload, err := embeddingPayload(p, model, r)
	if err != nil {
		return nil, Usage{}, err
	}
	var raw any
	if err = c.doJSON(ctx, p, http.MethodPost, model, path, payload, &raw); err != nil {
		return nil, Usage{}, err
	}
	body, err := embeddingNativeResponse(raw, providerEmbeddingProtocol(p))
	if err != nil {
		return nil, Usage{}, err
	}
	usage := retrievalUsage(body, false)
	if err := validateRetrievalUsageResult(usage); err != nil {
		return nil, usage, err
	}
	body, err = normalizeEmbeddingResponse(body, r)
	return body, usage, err
}
func (a GeminiAdapter) textEmbeddings(ctx context.Context, p Provider, model string, r EmbeddingsRequest) (any, Usage, error) {
	if err := validateEmbeddingRequest(r); err != nil {
		return nil, Usage{}, err
	}
	texts, err := embeddingTexts(r.Input)
	if err != nil {
		return nil, Usage{}, err
	}
	if r.Normalized != nil || r.LateChunking != nil || r.Truncation != nil || r.User != "" {
		return nil, Usage{}, embeddingUnsupportedParameter("gemini", "normalized/late_chunking/truncation/user")
	}
	requests := make([]any, len(texts))
	for i, text := range texts {
		item := map[string]any{"model": "models/" + strings.TrimPrefix(model, "models/"), "content": map[string]any{"parts": []any{map[string]any{"text": text}}}}
		if r.Dimensions != nil {
			item["outputDimensionality"] = *r.Dimensions
		}
		if task := embeddingTask(r, "gemini"); task != "" {
			item["taskType"] = task
		}
		requests[i] = item
	}
	action := ":batchEmbedContents"
	payload := any(map[string]any{"requests": requests})
	if len(texts) == 1 {
		action = ":embedContent"
		payload = requests[0]
	}
	var body map[string]any
	if err = a.doJSON(ctx, p, model, action, payload, &body); err != nil {
		return nil, Usage{}, err
	}
	var vectors []any
	if len(texts) == 1 {
		vectors = []any{body["embedding"]}
	} else {
		vectors, _ = body["embeddings"].([]any)
	}
	data := make([]any, len(vectors))
	for i, v := range vectors {
		item, ok := v.(map[string]any)
		if !ok {
			return nil, Usage{}, NewHTTPError(502, "invalid_embedding_response", "Invalid Gemini embedding")
		}
		data[i] = map[string]any{"index": i, "embedding": item["values"]}
	}
	response := map[string]any{"data": data}
	if metadata, ok := body["usageMetadata"].(map[string]any); ok {
		if n, exists := metadata["promptTokenCount"]; exists {
			response["usage"] = map[string]any{"prompt_tokens": n, "total_tokens": n}
		}
	}
	usage := retrievalUsage(response, false)
	if err := validateRetrievalUsageResult(usage); err != nil {
		return nil, usage, err
	}
	response, err = normalizeEmbeddingResponse(response, r)
	return response, usage, err
}
func (a MockAdapter) textEmbeddings(r EmbeddingsRequest) (any, Usage, error) {
	if err := validateEmbeddingRequest(r); err != nil {
		return nil, Usage{}, err
	}
	texts, err := embeddingTexts(r.Input)
	if err != nil {
		return nil, Usage{}, err
	}
	dimensions := 8
	if r.Dimensions != nil {
		dimensions = *r.Dimensions
	}
	data := make([]any, len(texts))
	usage := Usage{}
	for i, text := range texts {
		data[i] = map[string]any{"index": i, "embedding": deterministicEmbedding(text, dimensions)}
		usage.PromptTokens += EstimateTextTokens(text)
	}
	usage.TotalTokens = usage.PromptTokens
	body, err := normalizeEmbeddingResponse(map[string]any{"data": data, "usage": map[string]any{"prompt_tokens": usage.PromptTokens, "total_tokens": usage.TotalTokens}}, r)
	return body, usage, err
}
