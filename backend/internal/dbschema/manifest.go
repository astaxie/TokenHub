package dbschema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

//go:embed migrations_manifest.json
var manifestFS embed.FS

// ManifestSchemaVersion is the current manifest layout version. Bump it when
// the manifest gains or reinterprets fields so old embedded copies fail
// loudly instead of being compared against a new format.
const ManifestSchemaVersion = 1

// ManifestEntry is the frozen fingerprint of one registered migration:
// its content checksum plus the lock and statement budgets it declares
// .
type ManifestEntry struct {
	Version            int64   `json:"version"`
	Name               string  `json:"name"`
	Phase              Phase   `json:"phase"`
	Dialect            Dialect `json:"dialect"`
	Checksum           string  `json:"checksum"`
	LockTimeoutSeconds int64   `json:"lock_timeout_seconds"`
	StatementBudget    int64   `json:"statement_budget"`
}

// Manifest is the build-time fingerprint of the frozen schema evolution: the
// dialect baseline SQL and every registered migration. Release builds embed
// it and CI verifies the embedded copy matches the source.
type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Baselines     map[string]string `json:"baselines"`
	Migrations    []ManifestEntry   `json:"migrations"`
}

// LoadEmbeddedManifest reads the migration manifest embedded at build time.
func LoadEmbeddedManifest() (Manifest, error) {
	raw, err := manifestFS.ReadFile("migrations_manifest.json")
	if err != nil {
		return Manifest{}, fmt.Errorf("dbschema: read embedded migration manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("dbschema: parse embedded migration manifest: %w", err)
	}
	return manifest, nil
}

// BuildManifest computes the release migration manifest from a raw migration
// registry (normalized with the phase budget defaults) and the frozen dialect
// baselines. It is the single source of truth for the manifest generator and
// the freshness test.
func BuildManifest(migrations []Migration, baselines map[Dialect][]string) (Manifest, error) {
	normalized, err := NormalizeMigrations(migrations)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Baselines: map[string]string{}, Migrations: []ManifestEntry{}}
	for dialect, statements := range baselines {
		manifest.Baselines[string(dialect)] = baselineChecksum(statements)
	}
	for _, m := range normalized {
		manifest.Migrations = append(manifest.Migrations, ManifestEntry{
			Version:            m.Version,
			Name:               m.Name,
			Phase:              m.Phase,
			Dialect:            m.Dialect,
			Checksum:           m.Checksum(),
			LockTimeoutSeconds: m.LockTimeoutSeconds,
			StatementBudget:    m.StatementBudget,
		})
	}
	return manifest, nil
}

// baselineChecksum hashes the dialect baseline statements the same way the
// runner hashes migration content, so a baseline edit is a manifest change.
func baselineChecksum(statements []string) string {
	hash := sha256.New()
	for _, statement := range statements {
		hash.Write([]byte(statement))
		hash.Write([]byte("\n--\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// MigrationChecksum returns the manifest's pinned checksum for a version, if
// the version is a registered migration.
func (m Manifest) MigrationChecksum(version int64) (string, bool) {
	for _, entry := range m.Migrations {
		if entry.Version == version {
			return entry.Checksum, true
		}
	}
	return "", false
}
