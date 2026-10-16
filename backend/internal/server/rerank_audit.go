package server

import "tokenhub/backend/internal/guardrails"

type rerankAuditRequest struct {
	Model         string               `json:"model"`
	DocumentCount int                  `json:"document_count"`
	Guardrail     *guardrails.Decision `json:"guardrail,omitempty"`
}

func rerankRequestAuditPayload(request RerankRequest) rerankAuditRequest {
	return rerankAuditRequest{Model: request.Model, DocumentCount: len(request.Documents)}
}

func rerankResponseAuditPayload(request RerankRequest) any {
	count := len(request.Documents)
	if request.TopN != nil {
		count = *request.TopN
	}
	return map[string]any{"model": request.Model, "result_count": count}
}
