//go:build integration

package server

import "testing"

func TestPostgresSubscriptionOAuthRegistrationMigration(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_subscription_oauth_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	testSubscriptionOAuthRegistrationMigration(t, dsn)
}
