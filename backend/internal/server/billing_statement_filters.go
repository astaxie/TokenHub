package server

import "gorm.io/gorm"

// Keys and column names are internal constants, never request input.
func statementJSON(tx *gorm.DB, column, key string) string {
	if tx.Dialector.Name() == "postgres" {
		return "(" + column + "::jsonb ->> '" + key + "')"
	}
	return "json_extract(" + column + ", '$." + key + "')"
}

func statementSeedQuery(tx *gorm.DB, out *statementResult, kind string) *gorm.DB {
	q := out.Query
	query := tx.Where("kind = ? AND created_at >= ? AND created_at < ? AND created_at <= ?", kind, out.From, out.To, out.GeneratedAt)
	if kind == "attempt_prepared" {
		for _, filter := range []struct{ key, value string }{{"provider_id", q.ProviderID}, {"resource_id", q.ResourceID}, {"upstream_model", q.Model}} {
			if filter.value != "" {
				query = query.Where(statementJSON(tx, "payload", filter.key)+" = ?", filter.value)
			}
		}
		if len(q.ProjectIDs) > 0 {
			admission := tx.Model(&meteringEntry{}).Select("scope").Where("kind = ?", "admission").Where(statementJSON(tx, "payload", "project_id")+" IN ?", q.ProjectIDs)
			query = query.Where("scope IN (?)", admission)
		}
	} else {
		if len(q.ProjectIDs) > 0 {
			query = query.Where(statementJSON(tx, "payload", "project_id")+" IN ?", q.ProjectIDs)
		}
		if q.Model != "" {
			legacy := tx.Model(&UsageRecord{}).Select("request_id").Where("model_name = ?", q.Model)
			query = query.Where("("+statementJSON(tx, "payload", "model")+" = ? OR scope IN (?))", q.Model, legacy)
		}
	}
	return query
}
