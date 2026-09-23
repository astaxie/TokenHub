package bundle

import (
	"fmt"
	"strconv"
	"strings"
)

// SchemaVersion is the current version of the CanonicalMigrationBundle
// schema shipped by this package. Follow semver: bump the major on any
// breaking change to the on-disk JSON layout.
const SchemaVersion = "2.0.0"

// SemVer describes a parsed schema version.
type SemVer struct {
	Major int
	Minor int
	Patch int
}

// ParseSchemaVersion parses a "MAJOR.MINOR.PATCH" schema version.
func ParseSchemaVersion(v string) (SemVer, error) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return SemVer{}, fmt.Errorf("bundle: invalid schema_version %q, expected MAJOR.MINOR.PATCH", v)
	}
	var sv SemVer
	var err error
	if sv.Major, err = strconv.Atoi(parts[0]); err != nil {
		return SemVer{}, fmt.Errorf("bundle: invalid major in %q: %w", v, err)
	}
	if sv.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return SemVer{}, fmt.Errorf("bundle: invalid minor in %q: %w", v, err)
	}
	if sv.Patch, err = strconv.Atoi(parts[2]); err != nil {
		return SemVer{}, fmt.Errorf("bundle: invalid patch in %q: %w", v, err)
	}
	if sv.Major < 0 || sv.Minor < 0 || sv.Patch < 0 {
		return SemVer{}, fmt.Errorf("bundle: invalid schema_version %q, version components must be non-negative", v)
	}
	return sv, nil
}

// RejectIfIncompatible returns nil when the bundle can be safely
// processed by this version of the package. A different major version
// means the schema is incompatible and the bundle must be rejected.
// Newer minor/patch versions are accepted so old sinks can still read
// forward-compatible bundles produced by newer tools.
func RejectIfIncompatible(v string) error {
	if v == "" {
		return fmt.Errorf("bundle: missing schema_version")
	}
	got, err := ParseSchemaVersion(v)
	if err != nil {
		return err
	}
	expected, err := ParseSchemaVersion(SchemaVersion)
	if err != nil {
		return fmt.Errorf("bundle: internal error parsing SchemaVersion: %w", err)
	}
	if got.Major != expected.Major {
		return fmt.Errorf("bundle: incompatible schema_version %s, this package supports %s", v, SchemaVersion)
	}
	return nil
}
