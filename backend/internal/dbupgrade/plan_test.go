package dbupgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tokenhub/backend/internal/server"
)

// devSecretKey mirrors the store layer's development default so tests can
// verify ciphertext written by a store opened with ConfigFromEnv defaults.
const devSecretKey = "dev_tokenhub_secret_key"

// adoptTempStore creates a file-backed SQLite store with the full adopted
// schema and returns its URL.
func adoptTempStore(t *testing.T) string {
	t.Helper()
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "source.db")
	store, err := server.NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return databaseURL
}

func addTestProvider(t *testing.T, databaseURL string) {
	t.Helper()
	addProviderWithKey(t, databaseURL, devSecretKey, "prx_plan_test", "sk-plan-test-secret")
}

func addProviderWithKey(t *testing.T, databaseURL, secretKey, providerID, apiKey string) {
	t.Helper()
	config := server.ConfigFromEnv()
	config.DatabaseURL = databaseURL
	config.SecretKey = secretKey
	store, err := server.OpenStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	store.AddProvider(server.Provider{
		ID:     providerID,
		Name:   "plan-test-provider",
		Type:   "openai",
		APIKey: apiKey,
	})
}

// clearSecretKeyEnv removes any inherited TOKENHUB_SECRET_KEY so key
// resolution tests exercise the flag and sidecar paths deterministically.
func clearSecretKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TOKENHUB_SECRET_KEY", "")
}

func TestBuildPlanVerifiesCiphertextAndClassifiesMissingTarget(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	addTestProvider(t, databaseURL)
	targetURL := "sqlite://" + filepath.Join(t.TempDir(), "absent.db")

	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: targetURL,
		SecretKey: devSecretKey,
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Source.Driver != "sqlite" {
		t.Fatalf("source driver: got %q, want sqlite", plan.Source.Driver)
	}
	providers := findTable(plan, "providers")
	if providers == nil || providers.RowCount != 1 {
		t.Fatalf("providers table report: %+v", providers)
	}
	if providers.Group != string(TableGroupProtected) {
		t.Fatalf("providers group: got %q, want protected", providers.Group)
	}
	if !plan.Secret.Verified {
		t.Fatalf("canary must verify with the store's dev key; canary: %+v", plan.Secret.Canary)
	}
	apiKey := findCanary(plan, "providers", "api_key")
	if apiKey == nil || apiKey.DistinctCiphertexts != 1 || apiKey.FailedCiphertexts != 0 {
		t.Fatalf("providers.api_key canary: %+v", apiKey)
	}
	if plan.Secret.KeySource != string(SecretKeySourceFlag) {
		t.Fatalf("key source: got %q, want flag", plan.Secret.KeySource)
	}
	if plan.Target.State != TargetStateEmpty {
		t.Fatalf("target state: got %q, want empty", plan.Target.State)
	}
	if !strings.Contains(plan.Target.Detail, "does not exist yet") {
		t.Fatalf("missing-file target detail: %q", plan.Target.Detail)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", plan.Blockers)
	}
	// The preflight must not create the missing target file.
	if _, err := os.Stat(strings.TrimPrefix(targetURL, "sqlite://")); !os.IsNotExist(err) {
		t.Fatalf("plan must not create the target file: %v", err)
	}
}

func TestBuildPlanFlagsCiphertextThatDoesNotDecrypt(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	addTestProvider(t, databaseURL)

	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
		SecretKey: "not-the-key-the-store-encrypted-with-32b",
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Secret.Verified {
		t.Fatalf("wrong key must fail the canary")
	}
	apiKey := findCanary(plan, "providers", "api_key")
	if apiKey == nil || apiKey.FailedCiphertexts != 1 {
		t.Fatalf("providers.api_key canary with wrong key: %+v", apiKey)
	}
	if !hasFinding(plan.Blockers, "providers.api_key") {
		t.Fatalf("wrong-key canary must be a blocker; blockers: %v", plan.Blockers)
	}
}

func TestBuildPlanWithoutSecretKeyBlocksOnProtectedRows(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	addTestProvider(t, databaseURL)

	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Secret.Verified || plan.Secret.KeySource != string(SecretKeySourceNone) {
		t.Fatalf("unresolved key must mark the canary unverified; secret: %+v", plan.Secret)
	}
	if !hasFinding(plan.Blockers, "no source secret key") {
		t.Fatalf("protected rows without a key must block; blockers: %v", plan.Blockers)
	}
}

func TestBuildPlanWithoutSecretKeyPassesWhenNoProtectedRows(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)

	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if !plan.Secret.Verified {
		t.Fatalf("no protected rows must verify without a key")
	}
	if !hasFinding(plan.Warnings, "no source secret key resolved") {
		t.Fatalf("missing key should be surfaced as a warning; warnings: %v", plan.Warnings)
	}
}

func TestBuildPlanResolvesSidecarSecretKey(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	// Re-encrypt a provider under the sidecar key by opening the store with
	// it, so the canary proves the sidecar was actually used.
	sidecarKey := "sidecar-key-0123456789abcdef0123456789abcdef"
	addProviderWithKey(t, databaseURL, sidecarKey, "prx_sidecar", "sk-sidecar-secret")
	sidecarPath := strings.TrimPrefix(databaseURL, "sqlite://") + ".secret-key"
	if err := os.WriteFile(sidecarPath, []byte(sidecarKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Secret.KeySource != string(SecretKeySourceSidecar) {
		t.Fatalf("key source: got %q, want sidecar-file", plan.Secret.KeySource)
	}
	if !plan.Secret.Verified {
		t.Fatalf("sidecar key must verify ciphertext written under it; canary: %+v", plan.Secret.Canary)
	}
}

func TestBuildPlanClassifiesTargetStates(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)

	t.Run("adopted empty target warns instead of blocking", func(t *testing.T) {
		targetURL := adoptTempStore(t)
		plan, err := BuildPlan(context.Background(), Options{
			SourceURL: databaseURL,
			TargetURL: targetURL,
			SecretKey: devSecretKey,
		})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		if plan.Target.State != TargetStateTokenhub || plan.Target.HasData {
			t.Fatalf("target: %+v", plan.Target)
		}
		if len(plan.Blockers) != 0 {
			t.Fatalf("empty tokenhub target must not block: %v", plan.Blockers)
		}
	})

	t.Run("target holding tokenhub data blocks", func(t *testing.T) {
		targetURL := adoptTempStore(t)
		addTestProvider(t, targetURL)
		plan, err := BuildPlan(context.Background(), Options{
			SourceURL: databaseURL,
			TargetURL: targetURL,
			SecretKey: devSecretKey,
		})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		if !plan.Target.HasData || !hasFinding(plan.Blockers, "already holds TokenHub data") {
			t.Fatalf("occupied target must block; target: %+v blockers: %v", plan.Target, plan.Blockers)
		}
	})

	t.Run("unrecognized target blocks", func(t *testing.T) {
		targetURL := "sqlite://" + filepath.Join(t.TempDir(), "junk.db")
		_, db, err := server.OpenRawDatabase(targetURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE unrelated (id TEXT PRIMARY KEY)"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		plan, err := BuildPlan(context.Background(), Options{
			SourceURL: databaseURL,
			TargetURL: targetURL,
			SecretKey: devSecretKey,
		})
		if err != nil {
			t.Fatalf("build plan: %v", err)
		}
		if plan.Target.State != TargetStateUnrecognized || !hasFinding(plan.Blockers, "no TokenHub migration ledger") {
			t.Fatalf("unrecognized target must block; target: %+v blockers: %v", plan.Target, plan.Blockers)
		}
	})
}

func TestBuildPlanSurfacesUnreadableSource(t *testing.T) {
	clearSecretKeyEnv(t)
	// A directory is not a SQLite database: connecting must fail loudly.
	directory := t.TempDir()
	_, err := BuildPlan(context.Background(), Options{
		SourceURL: "sqlite://" + directory,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
		SecretKey: devSecretKey,
	})
	if err == nil || !strings.Contains(err.Error(), "open source database") {
		t.Fatalf("unreadable source must error, got: %v", err)
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	addTestProvider(t, databaseURL)
	plan, err := BuildPlan(context.Background(), Options{
		SourceURL: databaseURL,
		TargetURL: "sqlite://" + filepath.Join(t.TempDir(), "absent.db"),
		SecretKey: devSecretKey,
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	var buffer bytes.Buffer
	if err := RenderJSON(&buffer, plan); err != nil {
		t.Fatalf("render json: %v", err)
	}
	var decoded Plan
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatalf("decode rendered json: %v", err)
	}
	if decoded.Source.Driver != "sqlite" || len(decoded.Source.Tables) == 0 || !decoded.Secret.Verified {
		t.Fatalf("decoded plan lost fields: %+v", decoded)
	}
	var text bytes.Buffer
	RenderText(&text, plan)
	for _, want := range []string{"protected:", "providers", "ciphertext canary:", "state:", "result:"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text render missing %q:\n%s", want, text.String())
		}
	}
}

// TestRegistryTablesExistInAdoptedSchema pins the encrypted-column registry
// to the real adopted schema. GORM's snake-casing produces surprises such
// as provider_account_o_auth_session_records; a drifted registry table
// would silently skip that column's canary.
func TestRegistryTablesExistInAdoptedSchema(t *testing.T) {
	clearSecretKeyEnv(t)
	databaseURL := adoptTempStore(t)
	_, db, err := server.OpenRawDatabase(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tables, err := listTables(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if missing := registryTablesMissingFrom(tables); len(missing) != 0 {
		t.Fatalf("encrypted-column registry drifted from the schema: %v", missing)
	}
}

func findTable(plan *Plan, name string) *TableReport {
	for i := range plan.Source.Tables {
		if plan.Source.Tables[i].Name == name {
			return &plan.Source.Tables[i]
		}
	}
	return nil
}

func findCanary(plan *Plan, table, column string) *CanaryReport {
	for i := range plan.Secret.Canary {
		if plan.Secret.Canary[i].Table == table && plan.Secret.Canary[i].Column == column {
			return &plan.Secret.Canary[i]
		}
	}
	return nil
}

func hasFinding(findings []string, want string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, want) {
			return true
		}
	}
	return false
}
