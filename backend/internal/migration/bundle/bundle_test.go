package bundle

import (
	"testing"
	"time"
)

func TestMarshalUnmarshalBundle(t *testing.T) {
	original := &CanonicalMigrationBundle{
		SchemaVersion: SchemaVersion,
		Source: Source{
			Adapter:        "litellm",
			AdapterVersion: "1.60.0",
		},
		GeneratedAt: time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC),
		Warnings: []Warning{{
			Code:    "example_warning",
			Message: "example",
		}},
	}

	data, err := Marshal(original)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	decoded, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if decoded.SchemaVersion != original.SchemaVersion {
		t.Fatalf("schema version mismatch: got %q want %q", decoded.SchemaVersion, original.SchemaVersion)
	}
	if decoded.Source.Adapter != original.Source.Adapter {
		t.Fatalf("source adapter mismatch: got %q want %q", decoded.Source.Adapter, original.Source.Adapter)
	}
}

func TestUnmarshalRejectsIncompatibleVersion(t *testing.T) {
	data := []byte(`{"schema_version":"1.2.0","source":{"adapter":"litellm","adapter_version":"1.60.0"},"generated_at":"2026-07-23T10:00:00Z"}`)
	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected incompatible schema version to fail")
	}
}
