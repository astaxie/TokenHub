package source_test

import (
	"testing"

	"tokenhub/backend/internal/migration/source"
	_ "tokenhub/backend/internal/migration/source/litellm"
)

func TestGetRegisteredExtractor(t *testing.T) {
	extractor, err := source.Get("litellm")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if extractor.Name() != "litellm" {
		t.Fatalf("unexpected extractor name %q", extractor.Name())
	}
	if len(extractor.SupportedVersions()) == 0 {
		t.Fatal("expected non-empty supported versions")
	}
}

func TestGetUnknownExtractor(t *testing.T) {
	if _, err := source.Get("missing"); err == nil {
		t.Fatal("expected error for unknown extractor")
	}
}
