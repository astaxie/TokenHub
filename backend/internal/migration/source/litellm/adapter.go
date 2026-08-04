package litellm

import (
	"context"
	"fmt"
	"os"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/migration/source"
)

const (
	AdapterName           = "litellm"
	AdapterVersion        = "0.1.0"
	SupportedVersionRange = ">=1.52.0, <1.70.0"
)

type Adapter struct{}

func init() {
	source.Register(Adapter{})
}

func (Adapter) Name() string { return AdapterName }

func (Adapter) SupportedVersions() []string { return []string{SupportedVersionRange} }

// Probe inspects a LiteLLM configuration and returns readiness info.
func (Adapter) Probe(ctx context.Context, opts source.ExtractOptions) (source.Info, error) {
	if err := ctx.Err(); err != nil {
		return source.Info{}, err
	}
	if opts.InputPath == "" {
		return source.Info{Adapter: AdapterName, Ready: false, Note: "input path is required"}, nil
	}
	data, err := os.ReadFile(opts.InputPath)
	if err != nil {
		return source.Info{Adapter: AdapterName, Version: "", Ready: false, Note: fmt.Sprintf("cannot read config: %v", err)}, nil
	}
	config, err := ParseConfig(data)
	if err != nil {
		return source.Info{Adapter: AdapterName, Ready: false, Note: fmt.Sprintf("cannot parse config: %v", err)}, nil
	}
	version := config.LitellmSettings.Version
	if err := ValidateSupportedVersion(version); err != nil {
		return source.Info{Adapter: AdapterName, Version: version, Ready: false, Note: fmt.Sprintf("unsupported version: %v", err)}, nil
	}
	return source.Info{Adapter: AdapterName, Version: version, Ready: true}, nil
}

func (Adapter) Extract(ctx context.Context, opts source.ExtractOptions) (*bundle.CanonicalMigrationBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.InputPath == "" {
		return nil, fmt.Errorf("litellm: input path is required")
	}
	data, err := os.ReadFile(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("litellm: read config: %w", err)
	}
	config, err := ParseConfig(data)
	if err != nil {
		return nil, err
	}
	return BuildBundle(config, BuildOptions{
		OriginURL:  opts.OriginURL,
		Metadata:   opts.Metadata,
		IDStrategy: opts.IDStrategy,
	})
}
