package server

import (
	"context"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) handleAdminModelSearch(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, 200, map[string]any{"data": []Model{}})
		return
	}
	if len(query) > 200 {
		writeError(w, r, NewHTTPError(400, "query_too_long", "Search query is too long"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), maximumUsageQueryDuration)
	defer cancel()
	models, err := s.store.SearchAdminModels(ctx, s.canViewGlobalOperations(user), query)
	if err != nil {
		writeError(w, r, NewHTTPError(503, "model_search_unavailable", "Model search is temporarily unavailable"))
		return
	}
	results := make([]adminModelSearchResult, len(models))
	for i, model := range models {
		results[i] = adminModelSearchResult{ID: model.ID, Name: model.Name, Family: model.Family, Modality: model.Modality, Status: model.Status, Capabilities: model.Capabilities}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": results})
}

func (s *GormStore) SearchAdminModels(ctx context.Context, global bool, query string) ([]Model, error) {
	models := []Model{}
	db := visibleAdminModels(s.criticalReadDatabase().WithContext(ctx), global).Select("id", "name", "status", "modality", "family", "capabilities")
	normalized := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(normalized) + "%"
	capabilityWords := "(SELECT group_concat(value, ' ') FROM json_each(COALESCE(NULLIF(capabilities, ''), '[]')))"
	if s.dbDriver == "postgres" {
		capabilityWords = "(SELECT string_agg(value, ' ' ORDER BY ord) FROM jsonb_array_elements_text(CAST(COALESCE(NULLIF(capabilities, ''), '[]') AS jsonb)) WITH ORDINALITY AS capability(value, ord))"
	}
	keywords := "LOWER(COALESCE(name, '') || ' ' || COALESCE(id, '') || ' ' || COALESCE(modality, '') || ' ' || COALESCE(family, '') || ' ' || COALESCE(" + capabilityWords + ", '') || ' model models 模型 可用模型') LIKE ?"
	err := db.Where(keywords+` ESCAPE '\'`, pattern).Order("name, id").Limit(20).Find(&models).Error
	return models, err
}

func visibleAdminModels(db *gorm.DB, global bool) *gorm.DB {
	db = db.Model(&Model{})
	if !global {
		db = db.Where("models.status = ?", StatusActive).Where("EXISTS (SELECT 1 FROM model_routes r WHERE r.status = ? AND (r.model_name = models.name OR r.model_name = models.id))", StatusActive)
	}
	return db
}

type adminModelSearchResult struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Family       string   `json:"family"`
	Modality     string   `json:"modality"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities,omitempty"`
}
