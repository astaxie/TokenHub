package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tokenhub/backend/internal/migration/bundle"
	migrationtokenhub "tokenhub/backend/internal/migration/sink/tokenhub"

	"github.com/spf13/cobra"
)

func TestBuildSecretResolverEnv(t *testing.T) {
	const name = "TOKENHUB_MIGRATION_TEST_SECRET"
	t.Setenv(name, "resolved-from-env")

	resolver, err := buildSecretResolver("env", "")
	if err != nil {
		t.Fatalf("buildSecretResolver returned error: %v", err)
	}
	got, err := resolver.Resolve(bundle.SecretRef{Ref: name})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != "resolved-from-env" {
		t.Fatalf("Resolve = %q, want resolved-from-env", got)
	}
}

func TestBuildSecretResolverFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("PROVIDER_KEY=resolved-from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolver, err := buildSecretResolver("file", path)
	if err != nil {
		t.Fatalf("buildSecretResolver returned error: %v", err)
	}
	got, err := resolver.Resolve(bundle.SecretRef{Ref: "PROVIDER_KEY"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != "resolved-from-file" {
		t.Fatalf("Resolve = %q, want resolved-from-file", got)
	}
}

func TestBuildSecretResolverRejectsInvalidConfiguration(t *testing.T) {
	if _, err := buildSecretResolver("file", ""); err == nil {
		t.Fatal("expected missing file path to fail")
	}
	if _, err := buildSecretResolver("prompt", ""); err == nil {
		t.Fatal("expected unsupported prompt source to fail")
	}
}

func TestHandleApplyResultPersistsPartialArtifacts(t *testing.T) {
	dir := t.TempDir()
	checkpointPath := filepath.Join(dir, "checkpoint.json")
	newKeysPath := filepath.Join(dir, "new-keys.json")
	cmd := &cobra.Command{}
	cmd.Flags().String("checkpoint-out", checkpointPath, "")
	cmd.Flags().String("new-keys-out", newKeysPath, "")
	result := &migrationtokenhub.ApplyResult{
		Report: migrationtokenhub.MigrationReport{Created: 1},
		Checkpoint: migrationtokenhub.Checkpoint{Changes: []migrationtokenhub.Change{{
			Resource: "provider",
			ID:       "provider-1",
			Action:   migrationtokenhub.ActionCreate,
		}}},
		NewKeys: map[string]string{"key-ref": "sk-once"},
	}

	applyErr := errors.New("later resource failed")
	err := handleApplyResult(cmd, filepath.Join(dir, "bundle.json"), result, applyErr)
	if !errors.Is(err, applyErr) {
		t.Fatalf("expected apply error to be returned, got %v", err)
	}
	for _, path := range []string{checkpointPath, newKeysPath} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("expected partial artifact %s: %v", path, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}
