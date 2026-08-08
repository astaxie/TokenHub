package server

import (
	"net/http"
	"strconv"
	"strings"
)

type gatewayModelItem struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Category               string   `json:"category"`
	Family                 string   `json:"family"`
	Modality               string   `json:"modality"`
	ContextWindow          int64    `json:"context_window"`
	InputPriceUSDPer1M     float64  `json:"input_price_usd_per_1m"`
	OutputPriceUSDPer1M    float64  `json:"output_price_usd_per_1m"`
	EmbeddingPriceUSDPer1M float64  `json:"embedding_price_usd_per_1m"`
	InputModalities        []string `json:"input_modalities"`
	OutputModalities       []string `json:"output_modalities"`
	Capabilities           []string `json:"capabilities"`
	Status                 string   `json:"status"`
}

func (s *Server) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}

	page, pageSize, ok := gatewayModelPagination(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model_query", "Model query is invalid"))
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if len(name) > 200 || len(category) > 100 || len(status) > 100 {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model_query", "Model query is invalid"))
		return
	}

	filtered := make([]gatewayModelItem, 0)
	models, err := listGatewayModelsForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, model := range models {
		if name != "" && !strings.Contains(strings.ToLower(model.Name), name) {
			continue
		}
		if category != "" && model.Category != category {
			continue
		}
		if status != "" && model.Status != status {
			continue
		}
		filtered = append(filtered, gatewayModelItem{
			ID: model.ID, Name: model.Name, Category: model.Category, Family: model.Family,
			Modality: model.Modality, ContextWindow: model.ContextWindow,
			InputPriceUSDPer1M: model.InputPriceUSDPer1M, OutputPriceUSDPer1M: model.OutputPriceUSDPer1M,
			EmbeddingPriceUSDPer1M: model.EmbeddingPriceUSDPer1M,
			InputModalities:        append([]string(nil), model.InputModalities...),
			OutputModalities:       append([]string(nil), model.OutputModalities...),
			Capabilities:           append([]string(nil), model.Capabilities...), Status: model.Status,
		})
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": filtered[start:end], "page": page, "page_size": pageSize, "total": total,
	})
}

func gatewayModelPagination(r *http.Request) (int, int, bool) {
	page, pageSize := 1, 20
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("page")); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 || page > 10_000 {
			return 0, 0, false
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("page_size")); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return 0, 0, false
		}
	}
	return page, pageSize, true
}
