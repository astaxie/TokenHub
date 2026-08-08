package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDefaultDatabaseURLPrefersBackendDataFromRepoRoot(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "backend", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data", "model-catalog.yaml"), []byte("version: 1\nmodels:\n  - name: test-model\n    category: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, tmp)
	t.Setenv("TOKENHUB_DATABASE_URL", "")
	t.Setenv("TOKENHUB_SQLITE_BACKUP_DIR", "")
	t.Setenv("TOKENHUB_MODEL_CATALOG_FILE", "")

	config := ConfigFromEnv()
	if config.DatabaseURL != "sqlite://backend/data/tokenhub.db" {
		t.Fatalf("expected backend data database, got %q", config.DatabaseURL)
	}
	if config.SQLiteBackupDir != "backend/data/backups" {
		t.Fatalf("expected backend data backup dir, got %q", config.SQLiteBackupDir)
	}
	if config.ModelCatalogFile != "data/model-catalog.yaml" {
		t.Fatalf("expected root model catalog, got %q", config.ModelCatalogFile)
	}
}

func TestConfigDefaultDatabaseURLUsesLocalDataFromBackendDir(t *testing.T) {
	tmp := t.TempDir()
	backendDir := filepath.Join(tmp, "backend")
	if err := os.MkdirAll(filepath.Join(backendDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data", "model-catalog.yaml"), []byte("version: 1\nmodels:\n  - name: test-model\n    category: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, backendDir)
	t.Setenv("TOKENHUB_DATABASE_URL", "")
	t.Setenv("TOKENHUB_SQLITE_BACKUP_DIR", "")
	t.Setenv("TOKENHUB_MODEL_CATALOG_FILE", "")

	config := ConfigFromEnv()
	if config.DatabaseURL != defaultSQLiteDatabaseURL {
		t.Fatalf("expected local backend database, got %q", config.DatabaseURL)
	}
	if config.SQLiteBackupDir != "data/backups" {
		t.Fatalf("expected local backup dir, got %q", config.SQLiteBackupDir)
	}
	if config.ModelCatalogFile != "../data/model-catalog.yaml" {
		t.Fatalf("expected parent model catalog, got %q", config.ModelCatalogFile)
	}
}

func TestConfigParsesTrustedProxyCIDRs(t *testing.T) {
	t.Setenv("TOKENHUB_TRUSTED_PROXY_CIDRS", "127.0.0.1, 10.0.0.0/8;2001:db8::/32")

	config := ConfigFromEnv()
	if len(config.TrustedProxyCIDRs) != 3 {
		t.Fatalf("expected three trusted proxy entries, got %#v", config.TrustedProxyCIDRs)
	}
}

func TestConfigParsesClusterCoordinationSettings(t *testing.T) {
	t.Setenv("TOKENHUB_CORS_ALLOWED_ORIGINS", "https://console.example.com,https://admin.example.com")
	t.Setenv("TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS", "45")
	t.Setenv("TOKENHUB_CLUSTER_LOCK_TTL_SECONDS", "60")
	t.Setenv("TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS", "90")

	config := ConfigFromEnv()
	if len(config.CORSAllowedOrigins) != 2 {
		t.Fatalf("expected two CORS origins, got %#v", config.CORSAllowedOrigins)
	}
	if config.InFlightLeaseTTLSeconds != 45 || config.ClusterLockTTLSeconds != 60 || config.GracefulShutdownSeconds != 90 {
		t.Fatalf("unexpected cluster settings: %+v", config)
	}
}

func TestConfigParsesImageExecutionSettings(t *testing.T) {
	t.Setenv("TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS", "300")
	t.Setenv("TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS", "86400")

	config := ConfigFromEnv()
	if config.ImageJobTimeoutSeconds != 300 || config.ImageCapabilityRetrySecs != 86400 {
		t.Fatalf("unexpected image execution settings: %+v", config)
	}
}

func TestProductionConfigRejectsPlaceholderCredentials(t *testing.T) {
	config := Config{
		Environment:            "prod",
		AdminToken:             "change-me-tokenhub-admin-token",
		SecretKey:              "change-me-tokenhub-secret-key",
		BootstrapAdminPassword: "admin123456",
	}
	err := config.ValidateForStartup()
	if err == nil {
		t.Fatal("expected production credential validation to fail")
	}
	for _, name := range []string{"TOKENHUB_ADMIN_TOKEN", "TOKENHUB_SECRET_KEY", "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("expected validation error to mention %s: %v", name, err)
		}
	}
	if !strings.Contains(err.Error(), "default placeholder value") {
		t.Fatalf("expected validation error to explain placeholder rejection: %v", err)
	}
}

func TestProductionConfigReportsMinimumCredentialLengths(t *testing.T) {
	config := Config{
		Environment:            "prod",
		AdminToken:             "short-token",
		SecretKey:              "short-secret",
		BootstrapAdminPassword: "short",
	}
	err := config.ValidateForStartup()
	if err == nil {
		t.Fatal("expected short production credentials to fail")
	}
	for _, expected := range []string{
		"TOKENHUB_ADMIN_TOKEN must be at least 32 bytes",
		"TOKENHUB_SECRET_KEY must be at least 32 bytes",
		"TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD must be at least 12 bytes",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected validation error to contain %q: %v", expected, err)
		}
	}
	for _, secret := range []string{config.AdminToken, config.SecretKey, config.BootstrapAdminPassword} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error must not expose credential value %q: %v", secret, err)
		}
	}
}

func TestProductionConfigAcceptsStrongCredentials(t *testing.T) {
	config := Config{
		Environment:            "production",
		AdminToken:             strings.Repeat("a", 32),
		SecretKey:              strings.Repeat("s", 32),
		BootstrapAdminPassword: "strong-admin-password",
	}
	if err := config.ValidateForStartup(); err != nil {
		t.Fatalf("expected strong production credentials to pass: %v", err)
	}
}

func TestDevelopmentConfigKeepsLocalDefaults(t *testing.T) {
	config := Config{
		Environment:            "dev",
		AdminToken:             "dev_admin_token",
		SecretKey:              "dev_tokenhub_secret_key",
		BootstrapAdminPassword: "admin123456",
	}
	if err := config.ValidateForStartup(); err != nil {
		t.Fatalf("expected development defaults to remain available: %v", err)
	}
}

func TestBlankEnvironmentIsRejected(t *testing.T) {
	for _, environment := range []string{"", " \t\n"} {
		config := Config{
			Environment:            environment,
			AdminToken:             strings.Repeat("a", 32),
			SecretKey:              strings.Repeat("s", 32),
			BootstrapAdminPassword: "strong-admin-password",
		}
		err := config.ValidateForStartup()
		if err == nil || !strings.Contains(err.Error(), "TOKENHUB_ENV") {
			t.Fatalf("expected %q environment to be rejected, got %v", environment, err)
		}
	}
}

func TestGetenvBytes(t *testing.T) {
	const fallback int64 = 8 << 20
	cases := []struct {
		name string
		set  bool
		val  string
		want int64
	}{
		{"unset", false, "", fallback},
		{"empty", true, "", fallback},
		{"raw", true, "1048576", 1 << 20},
		{"kib", true, "16k", 16 << 10},
		{"mb", true, "32mb", 32 << 20},
		{"mib_caps", true, "8MiB", 8 << 20},
		{"gib_clamped", true, "1g", maxConfigurableRequestBytes},
		{"bytes_suffix", true, "2048b", 2048},
		{"garbage", true, "abc", fallback},
		{"zero", true, "0", fallback},
		{"negative", true, "-5", fallback},
		{"above_ceiling", true, "9999g", maxConfigurableRequestBytes},
		// Representable in int64 (~5.4e18 < math.MaxInt64) but above the ceiling:
		// must clamp to the ceiling, not fall back. Regression for the old
		// (1<<62)/multiplier overflow guard that wrongly rejected it.
		{"representable_above_ceiling", true, "5000000000g", maxConfigurableRequestBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("TOKENHUB_TEST_BYTES", tc.val)
			} else {
				t.Setenv("TOKENHUB_TEST_BYTES", "")
			}
			if got := getenvBytes("TOKENHUB_TEST_BYTES", fallback); got != tc.want {
				t.Fatalf("getenvBytes(%q)=%d, want %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1048576", 1 << 20, true},
		{"16k", 16 << 10, true},
		{"512m", 512 << 20, true},
		{"1g", 1 << 30, true},
		{"2048b", 2048, true},
		{"8MiB", 8 << 20, true},
		{"", 0, false},
		{"abc", 0, false},
		{"99999999999g", 0, false},              // overflow beyond int64
		{"5000000000g", 5000000000 << 30, true}, // representable above ceiling; clamp happens in getenvBytes
		{"8kk", 0, false},                       // malformed suffix
		{"8ib", 0, false},                       // malformed suffix
		{"8big", 0, false},                      // malformed suffix
		{"8mbb", 0, false},                      // malformed suffix
		{"k", 0, false},                         // suffix without digits
		{"-5", 0, false},                        // negative
	}
	for _, tc := range cases {
		got, ok := parseByteSize(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("parseByteSize(%q)=(%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working dir: %v", err)
		}
	})
}

// Metrics expose model names, provider identifiers and spend, so a deployment must opt
// in rather than inherit the endpoint.
func TestConfigMetricsAreDisabledByDefault(t *testing.T) {
	t.Setenv("TOKENHUB_METRICS_ENABLED", "")
	t.Setenv("TOKENHUB_METRICS_TOKEN", "")
	t.Setenv("TOKENHUB_METRICS_PROJECT_LABEL", "")

	config := ConfigFromEnv()
	if config.MetricsEnabled {
		t.Fatal("metrics must be off unless explicitly enabled")
	}
	if config.MetricsProjectLabel {
		t.Fatal("the project label must be off by default")
	}

	t.Setenv("TOKENHUB_METRICS_ENABLED", "true")
	if !ConfigFromEnv().MetricsEnabled {
		t.Fatal("TOKENHUB_METRICS_ENABLED=true must enable metrics")
	}
}
