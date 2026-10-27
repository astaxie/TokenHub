package server

import "tokenhub/backend/internal/dbschema"

func subscriptionOAuthRegistrationMigration() dbschema.Migration {
	return dbschema.Migration{
		Version: 10,
		Name:    "add-subscription-oauth-registrations",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS "subscription_oauth_hosts" (
				"id" text PRIMARY KEY,
				"host_id_encrypted" text NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS "subscription_oauth_registrations" (
				"id" text PRIMARY KEY,
				"actor_id" text NOT NULL,
				"host_id" text NOT NULL,
				"secret_encrypted" text NOT NULL
			)`,
		},
		StatementBudget: 2,
	}
}
