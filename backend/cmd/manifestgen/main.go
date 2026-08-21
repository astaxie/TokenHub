// Command manifestgen regenerates the embedded migration manifest
// (backend/internal/dbschema/migrations_manifest.json) from the migration
// registry and the frozen dialect baselines. CI runs it and fails on a diff,
// so the manifest never drifts from source.
package main

import (
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
		fmt.Fprintln(os.Stderr, "manifestgen:", err)
		os.Exit(1)
	}
}

func run() error {
	manifest, err := buildManifest()
	if err != nil {
		return err
	}
	return writeManifest(manifest)
}

// buildManifest computes the manifest from the live registry and the frozen
// baselines; the freshness test uses the same function.
func buildManifest() (dbschema.Manifest, error) {
	sqliteStatements, err := dbschema.SQLiteBaselineStatements()
	if err != nil {
		return dbschema.Manifest{}, err
	}
	postgresStatements, err := dbschema.PostgresBaselineStatements()
	if err != nil {
		return dbschema.Manifest{}, err
	}
	return dbschema.BuildManifest(server.SchemaMigrationRegistry(), map[dbschema.Dialect][]string{
		dbschema.DialectSQLite:   sqliteStatements,
		dbschema.DialectPostgres: postgresStatements,
	})
}

// manifestPath resolves backend/internal/dbschema/migrations_manifest.json
// from this file's location so the tool works regardless of the working
// directory.
func manifestPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "dbschema", "migrations_manifest.json")
}

func writeManifest(manifest dbschema.Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(manifestPath(), raw, 0o644)
}
