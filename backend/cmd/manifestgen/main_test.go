package main

import (
	"os"
	"reflect"
	"testing"

	"tokenhub/backend/internal/dbschema"
)

// TestEmbeddedManifestIsCurrent fails when the embedded manifest does not
// match the registry and frozen baselines, so a source change without a
// manifest update breaks CI. Regenerate with:
//
//	UPDATE_BASELINE=1 go test ./cmd/manifestgen -run TestEmbeddedManifestIsCurrent
func TestEmbeddedManifestIsCurrent(t *testing.T) {
	manifest, err := buildManifest()
	if err != nil {
		t.Fatalf("build manifest: %v", err)
	}
	embedded, err := dbschema.LoadEmbeddedManifest()
	if err != nil {
		t.Fatalf("load embedded manifest: %v", err)
	}
	if !reflect.DeepEqual(manifest, embedded) {
		if os.Getenv("UPDATE_BASELINE") == "1" {
			if err := writeManifest(manifest); err != nil {
				t.Fatalf("regenerate manifest: %v", err)
			}
			t.Skip("manifest regenerated; commit the diff")
		}
		t.Fatalf("embedded migration manifest is stale; run `go run ./cmd/manifestgen` from backend/ and commit the diff")
	}
}
