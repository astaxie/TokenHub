package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

// CompatibilityManifest declares the database compatibility range of one
// release: target is the database state the release establishes,
// min_compatible the lowest state it fully runs on, and max_compatible the
// highest state it can run on. Compatibility means full read and write, not
// merely being able to query.
type CompatibilityManifest struct {
	TargetVersion int64 `json:"target"`
	MinCompatible int64 `json:"min_compatible"`
	MaxCompatible int64 `json:"max_compatible"`
}

// CurrentCompatibilityManifest is this release's declaration. The bridge
// release establishes the adoption baseline and supports no other state;
// later releases widen the range as they ship expand migrations.
func CurrentCompatibilityManifest() CompatibilityManifest {
	return CompatibilityManifest{
		TargetVersion: dbschema.BaselineVersion,
		MinCompatible: dbschema.BaselineVersion,
		MaxCompatible: dbschema.BaselineVersion,
	}
}

// legacyReleaseCompatibility is the bridge release's one-time record of
// releases that predate the migration ledger. Each entry's range is verified
// with the real release binary against databases created and extended by the
// frozen schema flow (legacy adoption is validated with real v0.4.0
// and v0.5.0 fixtures). Releases without an entry have unknown compatibility
// and must not be activated through a managed rollback.
var legacyReleaseCompatibility = map[string]CompatibilityManifest{
	"0.4.0": {TargetVersion: 0, MinCompatible: 0, MaxCompatible: dbschema.BaselineVersion},
	"0.5.0": {TargetVersion: 0, MinCompatible: 0, MaxCompatible: dbschema.BaselineVersion},
}

// Rollback compatibility verdicts exposed to the admin surface.
const (
	rollbackCompatible   = "compatible"
	rollbackIncompatible = "incompatible"
	rollbackUnknown      = "unknown"
)

// RollbackCompatibility reports whether the current database state allows
// activating the requested release.
type RollbackCompatibility struct {
	Release       string
	Compatibility string
	Reason        string
	ReasonCode    string
	ReasonParams  map[string]any
}

func (s *Server) rollbackCompatibility(ctx context.Context, requestedVersion string) RollbackCompatibility {
	canonical, _, ok := parseSemanticVersion(requestedVersion)
	if !ok {
		return RollbackCompatibility{
			Release:       requestedVersion,
			Compatibility: rollbackUnknown,
			Reason:        "requested version is not a semantic version",
			ReasonCode:    "requested_version_invalid",
			ReasonParams:  map[string]any{"version": requestedVersion},
		}
	}
	manifest, known := legacyReleaseCompatibility[canonical]
	if !known {
		return RollbackCompatibility{
			Release:       canonical,
			Compatibility: rollbackUnknown,
			Reason:        "release predates database compatibility manifests and carries no verified record",
			ReasonCode:    "compatibility_record_missing",
			ReasonParams:  map[string]any{"version": canonical},
		}
	}
	evolution, hasEvolution := s.store.(interface {
		DatabaseEvolutionStatus(context.Context) DatabaseEvolutionStatus
	})
	if !hasEvolution {
		// Stores without a persistent evolution state have no schema
		// compatibility constraints.
		return RollbackCompatibility{Release: canonical, Compatibility: rollbackCompatible}
	}
	state := evolution.DatabaseEvolutionStatus(ctx)
	if !state.Ready {
		return RollbackCompatibility{
			Release:       canonical,
			Compatibility: rollbackIncompatible,
			Reason:        fmt.Sprintf("database evolution state is not clean: %s", state.Reason),
			ReasonCode:    "database_evolution_not_clean",
			ReasonParams:  map[string]any{"reason_code": state.ReasonCode},
		}
	}
	if state.SchemaVersion > manifest.MaxCompatible || state.SchemaVersion < manifest.MinCompatible {
		return RollbackCompatibility{
			Release:       canonical,
			Compatibility: rollbackIncompatible,
			Reason: fmt.Sprintf("database state version %d is outside release %s compatibility range [%d, %d]",
				state.SchemaVersion, canonical, manifest.MinCompatible, manifest.MaxCompatible),
			ReasonCode: "database_version_outside_range",
			ReasonParams: map[string]any{
				"state":   state.SchemaVersion,
				"release": canonical,
				"min":     manifest.MinCompatible,
				"max":     manifest.MaxCompatible,
			},
		}
	}
	return RollbackCompatibility{Release: canonical, Compatibility: rollbackCompatible}
}
