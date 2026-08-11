package server

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Environment              string
	AppVersion               string
	BuildType                string
	DeploymentType           string
	ManagedUpdates           bool
	ReleaseRepository        string
	InstallRoot              string
	AdminToken               string
	BootstrapAdminPassword   string
	PublicBaseURL            string
	DatabaseURL              string
	SQLiteBackupDir          string
	ModelCatalogFile         string
	ProviderCatalogFile      string
	SecretKey                string
	TrustedProxyCIDRs        []string
	CORSAllowedOrigins       []string
	SeedDemo                 bool
	ResourceFailureThreshold int
	ResourceCooldownSeconds  int
	// ResourceCooldownMaxSeconds caps the exponential backoff applied to repeated
	// half-open recovery failures, so a permanently broken resource is retried on a
	// widening interval instead of once per base cooldown forever.
	ResourceCooldownMaxSeconds int
	// MetricsEnabled turns on Prometheus collection and the /metrics endpoint. Off by
	// default: the endpoint discloses internal topology and spend, so exposing it is
	// an explicit operator decision rather than something a deployment inherits.
	MetricsEnabled bool
	// MetricsToken authenticates scrapes. When empty the admin token is accepted so a
	// default deployment is scrapeable, but a dedicated token is recommended: it keeps
	// the highest-privilege credential out of the scrape config.
	MetricsToken string
	// MetricsProjectLabel adds project_id to every gateway metric. Off by default
	// because it multiplies the series count of every metric by the project count.
	MetricsProjectLabel bool
	// TracingEnabled exports one OpenTelemetry trace per gateway call over OTLP/HTTP.
	// Off by default: it sends operational data to a third party, which is an
	// explicit operator decision rather than something a deployment inherits.
	TracingEnabled bool
	// TracingEndpoint is the signal-specific OTLP traces URL, used verbatim. For
	// Langfuse that is <host>/api/public/otel/v1/traces. Nothing is appended to it,
	// because a guessed suffix fails as a silent 404 rather than as a startup error.
	TracingEndpoint string
	// TracingHeaders authenticates the export, as comma-separated name=value pairs.
	// It is kept in its raw form so ValidateForStartup can reject a malformed value
	// instead of silently exporting without credentials. It holds a credential, so
	// it is never logged and never returned by an admin endpoint.
	TracingHeaders string
	// TracingCapturePayloads adds prompts, responses and upstream error text to the
	// exported spans. Separate from TracingEnabled and off by default: shipping
	// prompt content to another system is a data-egress decision distinct from
	// wanting latency and cost telemetry.
	TracingCapturePayloads bool
	// TracingSampleRatio is the fraction of calls exported, from 0 to 1.
	TracingSampleRatio float64
	// TracingTimeoutSeconds bounds a single export attempt.
	TracingTimeoutSeconds int
	// TracingQueueSize bounds the completions waiting to become spans. When it is
	// full new completions are dropped rather than queued into request latency, and
	// the drops are counted.
	TracingQueueSize int
	// UpstreamNonStreamTimeoutSeconds bounds one non-streaming upstream exchange
	// end to end. Streaming calls deliberately have no such bound: it would cover
	// reading the response body and so truncate a stream that is still producing.
	UpstreamNonStreamTimeoutSeconds int
	// UpstreamStreamIdleTimeoutSeconds is how long a streaming upstream call may
	// wait for response headers, and how long its body may then stay silent. The
	// budget restarts on every delivered byte, so a stream lives as long as the
	// upstream keeps producing.
	UpstreamStreamIdleTimeoutSeconds int
	InFlightLeaseTTLSeconds          int
	ClusterLockTTLSeconds            int
	GracefulShutdownSeconds          int
	DBMaxOpenConns                   int
	DBMaxIdleConns                   int
	DBConnMaxLifetimeMinutes         int
	// CacheAffinityEnabled turns on stateless cache locality routing. Off by
	// default because it changes routing behaviour; roll back by turning it off
	// rather than by rolling back the binary.
	CacheAffinityEnabled bool
	// CacheAffinityModels is the allowlist used for staged rollout. Empty means
	// every model.
	CacheAffinityModels []string
	// CacheAffinityAllowUserScope accepts user-scoped identifiers from Chat,
	// Responses, and Anthropic (metadata.user_id / user) as affinity keys. Off by default: one user's
	// concurrent sessions share the value, pinning all their traffic to a single
	// account and creating a hotspot.
	CacheAffinityAllowUserScope bool
	// GuardrailModelURL is the complete OpenAI-compatible chat completions URL
	// for the dedicated Qwen3Guard service. Empty keeps model detection offline;
	// each policy decides whether that condition audits or blocks.
	GuardrailModelURL            string
	GuardrailModelAPIKey         string
	GuardrailModelName           string
	GuardrailModelTimeoutSeconds int
	ImageStorageDir              string
	ImageWorkerConcurrency       int
	ImageQueueCapacity           int
	ImageJobTimeoutSeconds       int
	ImageCapabilityRetrySecs     int
	MaxJSONRequestBytes          int64
	MaxMultimodalRequestBytes    int64
}

func ConfigFromEnv() Config {
	return Config{
		Environment:                      getenv("TOKENHUB_ENV", "dev"),
		AppVersion:                       DefaultAppVersion,
		BuildType:                        defaultBuildType,
		DeploymentType:                   sourceDeploymentType,
		ManagedUpdates:                   getenvBool("TOKENHUB_MANAGED_UPDATES", false),
		ReleaseRepository:                getenv("TOKENHUB_RELEASE_REPOSITORY", defaultReleaseRepository),
		InstallRoot:                      getenv("TOKENHUB_INSTALL_ROOT", defaultNativeInstallRoot),
		AdminToken:                       getenv("TOKENHUB_ADMIN_TOKEN", "dev_admin_token"),
		BootstrapAdminPassword:           getenv("TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD", "admin123456"),
		PublicBaseURL:                    getenv("TOKENHUB_PUBLIC_BASE_URL", ""),
		DatabaseURL:                      resolveDatabaseURL(),
		SQLiteBackupDir:                  getenv("TOKENHUB_SQLITE_BACKUP_DIR", defaultSQLiteBackupDir()),
		ModelCatalogFile:                 getenv("TOKENHUB_MODEL_CATALOG_FILE", defaultModelCatalogFile()),
		ProviderCatalogFile:              getenv("TOKENHUB_PROVIDER_CATALOG_FILE", defaultProviderCatalogFile()),
		SecretKey:                        getenv("TOKENHUB_SECRET_KEY", "dev_tokenhub_secret_key"),
		TrustedProxyCIDRs:                getenvList("TOKENHUB_TRUSTED_PROXY_CIDRS"),
		CORSAllowedOrigins:               getenvList("TOKENHUB_CORS_ALLOWED_ORIGINS"),
		SeedDemo:                         getenvBool("TOKENHUB_SEED_DEMO", false),
		ResourceFailureThreshold:         getenvInt("TOKENHUB_RESOURCE_FAILURE_THRESHOLD", 3),
		ResourceCooldownSeconds:          getenvInt("TOKENHUB_RESOURCE_COOLDOWN_SECONDS", 300),
		ResourceCooldownMaxSeconds:       getenvInt("TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS", 3600),
		MetricsEnabled:                   getenvBool("TOKENHUB_METRICS_ENABLED", false),
		MetricsToken:                     getenv("TOKENHUB_METRICS_TOKEN", ""),
		MetricsProjectLabel:              getenvBool("TOKENHUB_METRICS_PROJECT_LABEL", false),
		TracingEnabled:                   getenvBool("TOKENHUB_TRACING_ENABLED", false),
		TracingEndpoint:                  getenv("TOKENHUB_TRACING_ENDPOINT", ""),
		TracingHeaders:                   getenv("TOKENHUB_TRACING_HEADERS", ""),
		TracingCapturePayloads:           getenvBool("TOKENHUB_TRACING_CAPTURE_PAYLOADS", false),
		TracingSampleRatio:               getenvFloat("TOKENHUB_TRACING_SAMPLE_RATIO", 1),
		TracingTimeoutSeconds:            getenvSetInt("TOKENHUB_TRACING_TIMEOUT_SECONDS", 10),
		TracingQueueSize:                 getenvSetInt("TOKENHUB_TRACING_QUEUE_SIZE", 2048),
		UpstreamNonStreamTimeoutSeconds:  getenvInt("TOKENHUB_UPSTREAM_NON_STREAM_TIMEOUT_SECONDS", defaultUpstreamNonStreamTimeoutSeconds),
		UpstreamStreamIdleTimeoutSeconds: getenvInt("TOKENHUB_UPSTREAM_STREAM_IDLE_TIMEOUT_SECONDS", defaultUpstreamStreamIdleTimeoutSeconds),
		InFlightLeaseTTLSeconds:          getenvInt("TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS", 300),
		ClusterLockTTLSeconds:            getenvInt("TOKENHUB_CLUSTER_LOCK_TTL_SECONDS", 180),
		GracefulShutdownSeconds:          getenvInt("TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS", 150),
		DBMaxOpenConns:                   getenvInt("TOKENHUB_DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:                   getenvInt("TOKENHUB_DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetimeMinutes:         getenvInt("TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES", 30),

		CacheAffinityEnabled:         getenvBool("TOKENHUB_CACHE_AFFINITY_ENABLED", false),
		CacheAffinityModels:          getenvList("TOKENHUB_CACHE_AFFINITY_MODELS"),
		CacheAffinityAllowUserScope:  getenvBool("TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE", false),
		GuardrailModelURL:            getenv("TOKENHUB_GUARDRAIL_MODEL_URL", ""),
		GuardrailModelAPIKey:         getenv("TOKENHUB_GUARDRAIL_MODEL_API_KEY", ""),
		GuardrailModelName:           getenv("TOKENHUB_GUARDRAIL_MODEL_NAME", "Qwen/Qwen3Guard-Gen-0.6B"),
		GuardrailModelTimeoutSeconds: getenvInt("TOKENHUB_GUARDRAIL_MODEL_TIMEOUT_SECONDS", 10),
		ImageStorageDir:              getenv("TOKENHUB_IMAGE_STORAGE_DIR", defaultImageStorageDir()),
		ImageWorkerConcurrency:       getenvInt("TOKENHUB_IMAGE_WORKER_CONCURRENCY", 2),
		ImageQueueCapacity:           getenvInt("TOKENHUB_IMAGE_QUEUE_CAPACITY", 64),
		ImageJobTimeoutSeconds:       getenvInt("TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS", 300),
		ImageCapabilityRetrySecs:     getenvInt("TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS", 86400),
		MaxJSONRequestBytes:          getenvBytes("TOKENHUB_MAX_JSON_REQUEST_BYTES", defaultMaxJSONRequestBytes),
		MaxMultimodalRequestBytes:    getenvBytes("TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES", defaultMaxMultimodalRequestBytes),
	}
}

func (c Config) ValidateForStartup() error {
	if repository := strings.TrimSpace(c.ReleaseRepository); repository != "" && !validReleaseRepository(repository) {
		return fmt.Errorf("invalid TOKENHUB_RELEASE_REPOSITORY: expected owner/repository")
	}
	// Checked in every environment, not only production: a tracing setting that is
	// wrong fails as silence, which is the one failure mode an operator cannot see.
	if err := c.validateTracing(); err != nil {
		return err
	}
	deploymentType := normalizeDeploymentType(c.DeploymentType, c.BuildType)
	if deploymentType == nativeDeploymentType ||
		(deploymentType == containerDeploymentType && c.ManagedUpdates) {
		root := filepath.Clean(strings.TrimSpace(c.InstallRoot))
		if !filepath.IsAbs(root) || root == string(filepath.Separator) {
			return fmt.Errorf("invalid TOKENHUB_INSTALL_ROOT: managed updates require a non-root absolute path")
		}
	}
	environment := strings.ToLower(strings.TrimSpace(c.Environment))
	if environment == "" {
		return fmt.Errorf("unsafe TOKENHUB_ENV configuration: set an explicit environment")
	}
	switch environment {
	case "dev", "development", "local", "test":
		return nil
	}
	invalid := make([]string, 0, 3)
	if reason := weakProductionSecretReason(c.AdminToken, 32, "dev_admin_token", "change-me-tokenhub-admin-token"); reason != "" {
		invalid = append(invalid, "TOKENHUB_ADMIN_TOKEN "+reason)
	}
	if reason := weakProductionSecretReason(c.SecretKey, 32, "dev_tokenhub_secret_key", "change-me-tokenhub-secret-key"); reason != "" {
		invalid = append(invalid, "TOKENHUB_SECRET_KEY "+reason)
	}
	if reason := weakProductionSecretReason(c.BootstrapAdminPassword, 12, "admin123456", "change-me-tokenhub-admin-password"); reason != "" {
		invalid = append(invalid, "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD "+reason)
	}
	if len(invalid) > 0 {
		return fmt.Errorf("unsafe %s configuration: %s", environment, strings.Join(invalid, "; "))
	}
	return nil
}

func weakProductionSecretReason(value string, minimumLength int, blocked ...string) string {
	value = strings.TrimSpace(value)
	for _, candidate := range blocked {
		if value == candidate {
			return "must not use a default placeholder value"
		}
	}
	if len(value) < minimumLength {
		return fmt.Sprintf("must be at least %d bytes after trimming whitespace", minimumLength)
	}
	return ""
}

func getenvList(key string) []string {
	fields := strings.FieldsFunc(os.Getenv(key), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// resolveDatabaseURL determines the database URL/DSN from the environment.
// Precedence:
//  1. TOKENHUB_DATABASE_URL, if set (URL or keyword DSN form).
//  2. A PostgreSQL keyword DSN built from separate TOKENHUB_DB_* fields, if
//     TOKENHUB_DB_HOST is set. This avoids URL-encoding issues when the
//     password contains delimiters such as #, ?, /, or %.
//  3. The default SQLite URL.
func resolveDatabaseURL() string {
	if url := strings.TrimSpace(os.Getenv("TOKENHUB_DATABASE_URL")); url != "" {
		return url
	}
	if host := strings.TrimSpace(os.Getenv("TOKENHUB_DB_HOST")); host != "" {
		return buildPostgresKeywordDSN(
			host,
			getenv("TOKENHUB_DB_PORT", "5432"),
			os.Getenv("TOKENHUB_DB_USER"),
			os.Getenv("TOKENHUB_DB_PASSWORD"),
			os.Getenv("TOKENHUB_DB_NAME"),
			getenv("TOKENHUB_DB_SSLMODE", "disable"),
		)
	}
	return defaultConfigDatabaseURL()
}

// buildPostgresKeywordDSN builds a PostgreSQL keyword/value DSN from raw fields.
// Values are quoted and escaped per libpq rules so passwords containing spaces
// or special characters (#, ?, /, %, ', \) are passed through unchanged.
func buildPostgresKeywordDSN(host, port, user, password, dbname, sslmode string) string {
	pairs := make([]string, 0, 6)
	appendPair := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		pairs = append(pairs, key+"="+quotePostgresDSNValue(value))
	}
	appendPair("host", host)
	appendPair("port", port)
	appendPair("user", user)
	appendPair("password", password)
	appendPair("dbname", dbname)
	appendPair("sslmode", sslmode)
	return strings.Join(pairs, " ")
}

// quotePostgresDSNValue quotes a keyword DSN value if it contains characters
// that require quoting (spaces, quotes, backslashes, or is empty), escaping
// backslashes and single quotes per libpq rules.
func quotePostgresDSNValue(value string) string {
	if value != "" && !strings.ContainsAny(value, " '\\") {
		return value
	}
	replacer := strings.NewReplacer("\\", "\\\\", "'", "\\'")
	return "'" + replacer.Replace(value) + "'"
}

func defaultConfigDatabaseURL() string {
	if pathExists("backend/data") {
		return "sqlite://backend/data/tokenhub.db"
	}
	return defaultSQLiteDatabaseURL
}

func defaultSQLiteBackupDir() string {
	if pathExists("backend/data") {
		return "backend/data/backups"
	}
	return "data/backups"
}

func defaultModelCatalogFile() string {
	for _, path := range []string{
		"data/model-catalog.yaml",
		"../data/model-catalog.yaml",
		"../../data/model-catalog.yaml",
		"../../../data/model-catalog.yaml",
		"/app/catalog/model-catalog.yaml",
		"/app/data/model-catalog.yaml",
	} {
		if pathExists(path) {
			return path
		}
	}
	return "data/model-catalog.yaml"
}

func defaultProviderCatalogFile() string {
	for _, path := range []string{
		"data/provider-catalog.json",
		"../data/provider-catalog.json",
		"../../data/provider-catalog.json",
		"../../../data/provider-catalog.json",
		"/app/catalog/provider-catalog.json",
		"/app/data/provider-catalog.json",
	} {
		if pathExists(path) {
			return path
		}
	}
	return "data/provider-catalog.json"
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var parsed int
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return fallback
		}
		parsed = parsed*10 + int(ch-'0')
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

// getenvFloat returns NaN when the variable is set to something that is not a
// number, so validation can reject the deployment instead of quietly substituting a
// default the operator did not ask for.
func getenvFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return math.NaN()
	}
	return parsed
}

// getenvSetInt returns -1 when the variable is set to something that is not a
// positive integer. It differs from getenvInt, which treats a malformed value as
// absent; here the difference has to survive as far as validation.
func getenvSetInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return -1
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	case "0", "false", "FALSE", "False", "no", "NO", "off", "OFF":
		return false
	default:
		return fallback
	}
}

const maxConfigurableRequestBytes int64 = 512 << 20

// Default request-body ceilings. The multimodal default is higher because Codex
// and vision requests inline base64-encoded images (~33% overhead), which routinely
// exceed a text-sized limit. Both are also used as the safety fallback when a
// zero-value Config reaches the decode path (e.g. in tests).
const (
	defaultMaxJSONRequestBytes       int64 = 8 << 20
	defaultMaxMultimodalRequestBytes int64 = 32 << 20
)

// getenvBytes reads a byte-size env var. It accepts a raw integer ("1048576")
// or a binary size suffix ("16k", "32mb", "8MiB", "1g"). Empty, unparseable,
// or non-positive values fall back. Values above maxConfigurableRequestBytes
// are clamped to the ceiling to guard against typos that would risk OOM.
func getenvBytes(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, ok := parseByteSize(raw)
	if !ok || value <= 0 {
		return fallback
	}
	if value > maxConfigurableRequestBytes {
		log.Printf("%s=%s exceeds the %d byte ceiling; clamping to ceiling", key, raw, maxConfigurableRequestBytes)
		return maxConfigurableRequestBytes
	}
	return value
}

// parseByteSize parses a byte-size string into a count of bytes. It accepts a
// bare integer ("1048576") or an integer followed by exactly one of the
// documented, case-insensitive binary suffixes: k/kb/kib, m/mb/mib, g/gb/gib
// (and a plain "b" for bytes). All multipliers are binary (1k = 1024). Any
// other trailing text — including doubled or mixed suffixes like "8kk" or
// "8big" — is rejected.
func parseByteSize(raw string) (int64, bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return 0, false
	}
	// Split leading decimal digits from the trailing unit suffix.
	split := len(lower)
	for i, ch := range lower {
		if ch < '0' || ch > '9' {
			split = i
			break
		}
	}
	digits := lower[:split]
	suffix := lower[split:]
	if digits == "" {
		return 0, false
	}
	var multiplier int64
	switch suffix {
	case "", "b":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = 1 << 10
	case "m", "mb", "mib":
		multiplier = 1 << 20
	case "g", "gb", "gib":
		multiplier = 1 << 30
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	if multiplier > 1 && n > math.MaxInt64/multiplier {
		return 0, false // overflow
	}
	return n * multiplier, true
}
