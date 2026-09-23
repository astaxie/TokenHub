// Command n1check verifies that a live database created by the N-1 (previous)
// release matches the committed immutable legacy schema fixture required by
// docs/database-evolution.md. It is dialect-neutral: the same semantic
// fixture pins the SQLite and PostgreSQL legacy shapes.
//
// Usage:
//
//	n1check <database-url>                      verify against the committed fixture
//	n1check -dump <out.json> <url>              write the introspected shape as a fixture
//	n1check -fixture v050 <url>                 verify against a named legacy fixture
//
// The default fixture is v040; -fixture selects another committed legacy
// shape such as v050.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"tokenhub/backend/internal/dbschema"
	"tokenhub/backend/internal/server"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "n1check:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	dumpPath := ""
	fixtureName := "v040"
	for len(args) >= 2 {
		switch args[0] {
		case "-dump":
			dumpPath = args[1]
		case "-fixture":
			fixtureName = args[1]
		default:
			return fmt.Errorf("unknown flag %q", args[0])
		}
		args = args[2:]
	}
	databaseURL := ""
	if len(args) > 0 {
		databaseURL = args[0]
	}
	if databaseURL == "" {
		databaseURL = os.Getenv("TOKENHUB_DATABASE_URL")
	}
	if databaseURL == "" {
		return fmt.Errorf("usage: n1check [-dump <out.json>] [-fixture <name>] <database-url>")
	}
	driver, db, err := server.OpenRawDatabase(databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	actual, err := dbschema.Introspect(context.Background(), db, dbschema.Dialect(driver), "")
	if err != nil {
		return err
	}
	if dumpPath != "" {
		raw, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		return os.WriteFile(dumpPath, raw, 0o644)
	}
	fixture, err := loadFixture(fixtureName, driver)
	if err != nil {
		return err
	}
	if violations := dbschema.CompareObjects(fixture, actual); len(violations) > 0 {
		return fmt.Errorf("legacy database does not match the immutable N-1 schema fixture:\n%s", dbschema.FormatViolations(violations))
	}
	_, _ = fmt.Fprintf(os.Stdout, "N-1 legacy schema fixture %s verified (%d tables, driver=%s)\n", fixtureName, len(actual.Tables), driver)
	return nil
}

// fixturePath resolves the committed N-1 legacy fixture for the driver from
// this file's location so the tool works regardless of the working directory.
// The semantic shape is the same across dialects but column type strings are
// not, so each dialect pins its own immutable fixture.
func fixturePath(name, driver string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	base := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "dbschema", "fixtures")
	if driver == "postgres" {
		return filepath.Join(base, "n1-legacy-"+name+"-postgres.json")
	}
	return filepath.Join(base, "n1-legacy-"+name+"-sqlite.json")
}

func loadFixture(name, driver string) (dbschema.ObjectSet, error) {
	raw, err := os.ReadFile(fixturePath(name, driver))
	if err != nil {
		return dbschema.ObjectSet{}, fmt.Errorf("read N-1 legacy fixture: %w", err)
	}
	var fixture dbschema.ObjectSet
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return dbschema.ObjectSet{}, fmt.Errorf("parse N-1 legacy fixture: %w", err)
	}
	return fixture, nil
}
