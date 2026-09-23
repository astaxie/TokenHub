package main

import (
	"testing"

	"tokenhub/backend/internal/dbschema"
)

// TestFixturesParseAndSelfConsistent guards the committed immutable N-1
// legacy fixtures: they must parse as ObjectSet and be internally consistent
// (comparing the fixture against itself yields no violations). The fixtures
// themselves are regenerated with the real release binaries:
//
//	cd backend && go run ./cmd/n1check -dump internal/dbschema/fixtures/n1-legacy-<name>-<dialect>.json <release-db-url>
func TestFixturesParseAndSelfConsistent(t *testing.T) {
	fixtures := []struct{ name, driver string }{
		{"v040", "sqlite"},
		{"v040", "postgres"},
		{"v050", "sqlite"},
	}
	for _, item := range fixtures {
		fixture, err := loadFixture(item.name, item.driver)
		if err != nil {
			t.Fatalf("load %s %s fixture: %v", item.name, item.driver, err)
		}
		if len(fixture.Tables) == 0 {
			t.Fatalf("%s %s fixture holds no tables", item.name, item.driver)
		}
		foundRequestLogs := false
		for _, table := range fixture.Tables {
			if table.Name == "request_logs" {
				foundRequestLogs = true
			}
			if len(table.Columns) == 0 {
				t.Fatalf("%s %s fixture table %q has no columns", item.name, item.driver, table.Name)
			}
		}
		if !foundRequestLogs {
			t.Fatalf("%s %s fixture is missing the request_logs table", item.name, item.driver)
		}
		if violations := dbschema.CompareObjects(fixture, fixture); len(violations) > 0 {
			t.Fatalf("%s %s fixture is not self-consistent: %s", item.name, item.driver, dbschema.FormatViolations(violations))
		}
	}
}
