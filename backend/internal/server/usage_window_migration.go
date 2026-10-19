package server

import "tokenhub/backend/internal/dbschema"

func usageWindowIndexesMigration() dbschema.Migration {
	return dbschema.Migration{
		Version: 9,
		Name:    "add-usage-scope-window-indexes",
		Statements: []string{
			`CREATE INDEX IF NOT EXISTS "idx_usage_records_api_key_created" ON "usage_records" ("api_key_id", "created_at")`,
			`CREATE INDEX IF NOT EXISTS "idx_usage_records_attributed_user_created" ON "usage_records" ("attributed_user_id", "created_at")`,
		},
		StatementBudget: 2,
	}
}
