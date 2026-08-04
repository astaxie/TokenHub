package source

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/migration/bundle"
)

// Info describes a source adapter and its readiness.
type Info struct {
	Adapter string `json:"adapter"`
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
	Note    string `json:"note,omitempty"`
}

// ExtractOptions configures a source extraction run.
type ExtractOptions struct {
	InputPath  string
	OriginURL  string
	Metadata   map[string]string
	IDStrategy bundle.IDStrategy
}

// Extractor produces a CanonicalMigrationBundle from a competitor
// gateway configuration or export.
type Extractor interface {
	Name() string
	SupportedVersions() []string
	Probe(ctx context.Context, opts ExtractOptions) (Info, error)
	Extract(ctx context.Context, opts ExtractOptions) (*bundle.CanonicalMigrationBundle, error)
}

var registry = map[string]Extractor{}

// Register makes an extractor discoverable by name.
func Register(extractor Extractor) {
	if extractor == nil {
		panic("source: cannot register nil extractor")
	}
	name := extractor.Name()
	if name == "" {
		panic("source: extractor name must not be empty")
	}
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("source: extractor %q already registered", name))
	}
	registry[name] = extractor
}

// MustGet returns the named extractor or panics if missing.
func MustGet(name string) Extractor {
	extractor, err := Get(name)
	if err != nil {
		panic(err)
	}
	return extractor
}

// Get returns the named extractor.
func Get(name string) (Extractor, error) {
	extractor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("source: unknown extractor %q", name)
	}
	return extractor, nil
}

// Names returns the registered extractor names.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
