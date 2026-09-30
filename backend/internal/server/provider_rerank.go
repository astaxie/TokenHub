package server

import (
	"context"
	"net/http"
	"strings"
)

type ProviderReranker interface {
	Rerank(context.Context, Provider, string, RerankRequest) (any, Usage, error)
}

func providerRerankProtocol(p Provider) string {
	if value := strings.TrimSpace(p.Options["rerank_protocol"]); value != "" {
		return value
	}
	switch p.Options["catalog_id"] {
	case "cohere":
		return "cohere"
	case "jina":
		return "jina"
	case "voyage", "voyageai":
		return "voyage"
	case "siliconflow", "siliconflow-cn":
		return "jina"
	}
	// Custom and local compatible endpoints use the common /rerank contract.
	// Named catalogs and explicit protocol selections retain their own semantics.
	catalog := strings.TrimSpace(p.Options["catalog_id"])
	if (p.Type == ProviderOpenAICompatible || p.Type == "local") && (catalog == "" || catalog == "local" || catalog == ProviderOpenAICompatible) {
		return "jina"
	}
	return ""
}
func rerankPayload(p Provider, model string, r RerankRequest) (string, map[string]any, error) {
	profile := providerRerankProtocol(p)
	body := map[string]any{"model": model, "query": r.Query, "documents": r.Documents}
	path := "/rerank"
	if r.TopN != nil {
		body["top_n"] = *r.TopN
	}
	if r.Instruction != "" && profile != "qwen" && profile != "jina" && profile != "dashscope" {
		return "", nil, NewHTTPError(400, "rerank_parameter_unsupported", "instruction is unsupported by this protocol")
	}
	if r.Truncation != nil && profile != "voyage" && profile != "tei" {
		return "", nil, NewHTTPError(400, "rerank_parameter_unsupported", "truncation is unsupported by this protocol")
	}
	switch profile {
	case "jina":
		body["return_documents"] = false
		if r.Instruction != "" {
			body["instruction"] = r.Instruction
		}
	case "cohere":
	case "voyage":
		delete(body, "top_n")
		if r.TopN != nil {
			body["top_k"] = *r.TopN
		}
		body["return_documents"] = false
		if r.Truncation != nil {
			body["truncation"] = *r.Truncation
		}
	case "qwen":
		path = "/reranks"
		if r.Instruction != "" {
			body["instruct"] = r.Instruction
		}
	case "dashscope":
		path = "/services/rerank/text-rerank/text-rerank"
		params := map[string]any{"return_documents": false}
		if r.TopN != nil {
			params["top_n"] = *r.TopN
		}
		if r.Instruction != "" {
			params["instruct"] = r.Instruction
		}
		body = map[string]any{"model": model, "input": map[string]any{"query": r.Query, "documents": r.Documents}, "parameters": params}
	case "tei":
		body = map[string]any{"query": r.Query, "texts": r.Documents, "raw_scores": false}
		if r.Truncation != nil {
			body["truncate"] = *r.Truncation
		}
	default:
		return "", nil, NewHTTPError(400, "rerank_protocol_required", "Configure a supported rerank_protocol before calling this provider")
	}
	if value := strings.TrimSpace(p.Options["rerank_path"]); value != "" {
		if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#") || strings.Contains(value, "..") {
			return "", nil, NewHTTPError(400, "invalid_rerank_path", "rerank_path must be an absolute path without query or traversal")
		}
		path = value
	}
	return path, body, nil
}
func (a OpenAICompatibleAdapter) Rerank(ctx context.Context, p Provider, model string, r RerankRequest) (any, Usage, error) {
	if err := validateRerankRequest(r); err != nil {
		return nil, Usage{}, err
	}
	path, payload, err := rerankPayload(p, model, r)
	if err != nil {
		return nil, Usage{}, err
	}
	var raw any
	if err = a.doJSON(ctx, p, http.MethodPost, path, payload, &raw); err != nil {
		return nil, Usage{}, err
	}
	body, _ := raw.(map[string]any)
	profile := providerRerankProtocol(p)
	usage := retrievalUsage(body, profile == "cohere")
	if err := validateRetrievalUsageResult(usage); err != nil {
		return nil, usage, err
	}
	normalized, err := normalizeRerankResponse(raw, profile, r)
	return normalized, usage, err
}
