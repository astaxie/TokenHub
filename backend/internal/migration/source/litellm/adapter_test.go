package litellm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	migrationbundle "tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/migration/source"
)

func TestExtractBasicConfig(t *testing.T) {
	adapter := Adapter{}
	bundle, err := adapter.Extract(context.Background(), source.ExtractOptions{
		InputPath: filepath.Join("testdata", "config-basic.yaml"),
		OriginURL: "https://litellm.example/config.yaml",
		Metadata:  map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if bundle.Source.Adapter != AdapterName {
		t.Fatalf("unexpected adapter %q", bundle.Source.Adapter)
	}
	if len(bundle.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(bundle.Providers))
	}
	if len(bundle.ProviderResources) != 2 {
		t.Fatalf("expected 2 provider resources, got %d", len(bundle.ProviderResources))
	}
	if len(bundle.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(bundle.Models))
	}
	if len(bundle.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(bundle.Routes))
	}
	if len(bundle.Teams) != 1 || len(bundle.Users) != 1 || len(bundle.Projects) != 1 || len(bundle.APIKeys) != 1 {
		t.Fatalf("unexpected identity resource counts: teams=%d users=%d projects=%d keys=%d", len(bundle.Teams), len(bundle.Users), len(bundle.Projects), len(bundle.APIKeys))
	}
	if bundle.APIKeys[0].KeySecret == nil || bundle.APIKeys[0].KeySecret.Ref == "" {
		t.Fatal("expected api key secret ref")
	}
	if bundle.ProviderResources[0].APIKeySecret == nil || bundle.ProviderResources[0].APIKeySecret.Ref != "OPENAI_API_KEY" {
		t.Fatalf("expected env-backed provider resource secret ref to preserve env name, got %+v", bundle.ProviderResources[0].APIKeySecret)
	}
	if bundle.APIKeys[0].ProjectRef != "project:team:team-red" {
		t.Fatalf("unexpected project ref: %q", bundle.APIKeys[0].ProjectRef)
	}
	if bundle.APIKeys[0].Spec.Metadata["litellm_user_ref"] != "user:user-alice" {
		t.Fatalf("expected user linkage metadata, got %+v", bundle.APIKeys[0].Spec.Metadata)
	}
	if bundle.Users[0].TeamRef != "team:team-red" {
		t.Fatalf("unexpected user team ref: %q", bundle.Users[0].TeamRef)
	}
	if bundle.Routes[0].ModelRef == "" || bundle.Routes[0].ProviderRef == "" || bundle.Routes[0].ProviderResourceRef == "" {
		t.Fatalf("expected route refs to be populated: %+v", bundle.Routes[0])
	}
	if bundle.Routes[0].Spec.ProviderModel == "" || bundle.Routes[0].Spec.ProviderModel == "openai/gpt-4o-mini" {
		t.Fatalf("expected upstream provider model without provider prefix, got %q", bundle.Routes[0].Spec.ProviderModel)
	}
	for _, resource := range bundle.ProviderResources {
		if resource.APIKeySecret == nil {
			t.Fatal("expected provider resource secret ref")
		}
	}
	if len(bundle.Warnings) < 2 {
		t.Fatalf("expected warnings for partial support, got %d", len(bundle.Warnings))
	}
}

func TestExtractHonorsIDStrategy(t *testing.T) {
	b, err := Adapter{}.Extract(context.Background(), source.ExtractOptions{
		InputPath:  filepath.Join("testdata", "config-basic.yaml"),
		IDStrategy: migrationbundle.IDStrategySource,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(b.Users) == 0 {
		t.Fatal("expected extracted users")
	}
	if b.Users[0].Spec.ID != b.Users[0].ExternalRef.ID {
		t.Fatalf("source strategy should preserve external ID: got %q, want %q", b.Users[0].Spec.ID, b.Users[0].ExternalRef.ID)
	}
	if len(b.Providers) == 0 {
		t.Fatal("expected extracted providers")
	}
	if b.Providers[0].Spec.ID != b.Providers[0].ExternalRef.ID {
		t.Fatalf("source strategy should preserve provider external ID: got %q, want %q", b.Providers[0].Spec.ID, b.Providers[0].ExternalRef.ID)
	}
}

func TestExtractMaterializesReferencedTeamAndProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`litellm_settings:
  version: 1.58.2
model_list:
  - model_name: gpt-4o-mini
    litellm_params:
      model: openai/gpt-4o-mini
      api_base: https://api.openai.com/v1
      api_key: os.environ/OPENAI_API_KEY
key_management_settings:
  users:
    - user_id: user-alice
      user_email: alice@example.com
      team_id: team-red
  virtual_keys:
    - key_alias: red-team-key
      token: sk-red-team
      team_id: team-red
      user_id: user-alice
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	bundle, err := Adapter{}.Extract(context.Background(), source.ExtractOptions{InputPath: path})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(bundle.Teams) != 1 || bundle.Teams[0].ExternalRef.ID != "team:team-red" {
		t.Fatalf("expected team to be materialized, got %+v", bundle.Teams)
	}
	if len(bundle.Projects) != 1 || bundle.Projects[0].ExternalRef.ID != "project:team:team-red" {
		t.Fatalf("expected team project to be materialized, got %+v", bundle.Projects)
	}
	if bundle.Users[0].TeamRef != "team:team-red" {
		t.Fatalf("expected user team ref to resolve, got %q", bundle.Users[0].TeamRef)
	}
	if bundle.APIKeys[0].ProjectRef != "project:team:team-red" {
		t.Fatalf("expected api key project ref to resolve, got %q", bundle.APIKeys[0].ProjectRef)
	}
}

func TestExtractCreatesDistinctProvidersForDistinctEndpoints(t *testing.T) {
	adapter := Adapter{}
	bundle, err := adapter.Extract(context.Background(), source.ExtractOptions{
		InputPath: filepath.Join("testdata", "config-basic.yaml"),
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(bundle.Providers) != 2 {
		t.Fatalf("expected 2 providers for distinct endpoint/base combinations, got %d", len(bundle.Providers))
	}
}

func TestParseConfigRejectsMissingVersion(t *testing.T) {
	_, err := ParseConfig([]byte("model_list: []\n"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractRequiresInputPath(t *testing.T) {
	_, err := Adapter{}.Extract(context.Background(), source.ExtractOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("litellm_settings:\n  version: 1.70.0\nmodel_list:\n  - model_name: test\n    litellm_params:\n      model: openai/test\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Adapter{}.Extract(context.Background(), source.ExtractOptions{InputPath: path})
	if err == nil {
		t.Fatal("expected unsupported version error")
	}
}

func TestExtractNeverEmbedsRawTokenWithoutAlias(t *testing.T) {
	const rawToken = "sk-super-secret-token"
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`litellm_settings:
  version: 1.58.2
model_list:
  - model_name: gpt-4o-mini
    litellm_params:
      model: openai/gpt-4o-mini
      api_base: https://api.openai.com/v1
      api_key: os.environ/OPENAI_API_KEY
key_management_settings:
  virtual_keys:
    - token: ` + rawToken + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	b, err := Adapter{}.Extract(context.Background(), source.ExtractOptions{InputPath: path})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(b.APIKeys) != 1 {
		t.Fatalf("expected 1 api key, got %d", len(b.APIKeys))
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), rawToken) {
		t.Fatal("raw virtual-key token must not be serialized into the bundle")
	}
	if b.APIKeys[0].KeySecret == nil {
		t.Fatal("expected key secret ref for token-bearing virtual key")
	}
}
