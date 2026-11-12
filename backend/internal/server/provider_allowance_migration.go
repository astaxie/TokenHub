package server

import "tokenhub/backend/internal/dbschema"

func providerAllowanceMigration() dbschema.Migration {
	return dbschema.Migration{Version: 11, Name: "add-provider-allowance-management", StatementBudget: 12, Statements: []string{
		`CREATE TABLE IF NOT EXISTS "provider_allowance_settings" (
 "resource_id" text PRIMARY KEY, "shared_account_id" text NOT NULL DEFAULT '', "revision" text NOT NULL, "source_encrypted" text NOT NULL,
 "token_encrypted" text NOT NULL, "policy_json" text NOT NULL, "wallet_snapshot" text NOT NULL DEFAULT '', "updated_at" timestamp NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS "idx_provider_allowance_settings_shared" ON "provider_allowance_settings" ("shared_account_id")`,
		`CREATE TABLE IF NOT EXISTS "provider_allowance_points" (
 "id" text PRIMARY KEY, "resource_id" text NOT NULL, "fetched_at" timestamp NOT NULL,
 "digest" text NOT NULL, "snapshot" text NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS "idx_provider_allowance_points_resource_time" ON "provider_allowance_points" ("resource_id", "fetched_at")`,
		`CREATE INDEX IF NOT EXISTS "idx_provider_allowance_points_time" ON "provider_allowance_points" ("fetched_at")`,
		`CREATE TABLE IF NOT EXISTS "provider_allowance_events" (
 "id" text PRIMARY KEY, "resource_id" text NOT NULL, "kind" text NOT NULL,
 "cycle" text NOT NULL, "state" text NOT NULL, "created_at" timestamp NOT NULL, "updated_at" timestamp NOT NULL, "details" text NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS "idx_provider_allowance_events_resource_time" ON "provider_allowance_events" ("resource_id", "created_at")`,
		`CREATE TABLE IF NOT EXISTS "provider_allowance_sessions" ("resource_id" text PRIMARY KEY, "revision" text NOT NULL, "token_encrypted" text NOT NULL, "expires_at" timestamp NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS "provider_allowance_wallet_counters" ("resource_id" text NOT NULL, "window_key" text NOT NULL, "cycle" text NOT NULL, "previous_balance" double precision NOT NULL, "spent" double precision NOT NULL, "observed_at" timestamp NOT NULL, PRIMARY KEY ("resource_id", "window_key", "cycle"))`,
		`CREATE INDEX IF NOT EXISTS "idx_provider_allowance_wallet_counter_time" ON "provider_allowance_wallet_counters" ("observed_at")`,
	}}
}
