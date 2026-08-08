package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretRefJSONRoundTrip(t *testing.T) {
	original := SecretRef{Ref: "OPENAI_API_KEY"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal secret ref: %v", err)
	}

	var decoded SecretRef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal secret ref: %v", err)
	}
	if decoded != original {
		t.Fatalf("decoded secret ref mismatch: got %+v want %+v", decoded, original)
	}
}

func TestSecretRefUnmarshalString(t *testing.T) {
	var decoded SecretRef
	if err := json.Unmarshal([]byte(`"OPENAI_API_KEY"`), &decoded); err != nil {
		t.Fatalf("unmarshal string secret ref: %v", err)
	}
	if decoded.Ref != "OPENAI_API_KEY" {
		t.Fatalf("decoded ref mismatch: got %q", decoded.Ref)
	}
}

func TestEnvResolver(t *testing.T) {
	t.Setenv("MIGRATION_SECRET", "secret-value")

	value, err := EnvResolver{}.Resolve(SecretRef{Ref: "MIGRATION_SECRET"})
	if err != nil {
		t.Fatalf("resolve env secret: %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("resolved value mismatch: got %q", value)
	}
}

func TestFileResolver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	content := []byte("# comment\nOPENAI_API_KEY=secret-value\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write secrets file: %v", err)
	}

	resolver, err := NewFileResolver(path)
	if err != nil {
		t.Fatalf("new file resolver: %v", err)
	}

	value, err := resolver.Resolve(SecretRef{Ref: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatalf("resolve file secret: %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("resolved value mismatch: got %q", value)
	}
}

func TestFileResolverTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	content := []byte("  OPENAI_API_KEY  =  secret-value  \n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write secrets file: %v", err)
	}

	resolver, err := NewFileResolver(path)
	if err != nil {
		t.Fatalf("new file resolver: %v", err)
	}

	value, err := resolver.Resolve(SecretRef{Ref: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatalf("resolve file secret: %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("expected whitespace trimmed, got %q", value)
	}
}

func TestStaticResolver(t *testing.T) {
	resolver := StaticResolver{"OPENAI_API_KEY": "secret-value"}
	value, err := resolver.Resolve(SecretRef{Ref: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatalf("resolve static secret: %v", err)
	}
	if value != "secret-value" {
		t.Fatalf("resolved value mismatch: got %q", value)
	}
}

func TestRedactedBundleReturnsCopy(t *testing.T) {
	original := &CanonicalMigrationBundle{
		SchemaVersion: SchemaVersion,
		Source: Source{
			Adapter:        "litellm",
			AdapterVersion: "1.60.0",
		},
		Providers: []ProviderRef{{
			ExternalRef:  ExternalRef{System: "litellm", ID: "openai"},
			APIKeySecret: &SecretRef{Ref: "OPENAI_API_KEY"},
		}},
	}

	redacted, err := RedactedBundle(original)
	if err != nil {
		t.Fatalf("redact bundle: %v", err)
	}
	if redacted == original {
		t.Fatal("expected a deep copy")
	}
	if redacted.Providers[0].APIKeySecret == nil || redacted.Providers[0].APIKeySecret.Ref != "OPENAI_API_KEY" {
		t.Fatal("expected secret ref to be preserved")
	}
}
