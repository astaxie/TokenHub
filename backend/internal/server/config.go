package server

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Environment              string
	AdminToken               string
	BootstrapAdminPassword   string
	PublicBaseURL            string
	DatabaseURL              string
	SQLiteBackupDir          string
	ModelCatalogFile         string
	SecretKey                string
	TrustedProxyCIDRs        []string
	CORSAllowedOrigins       []string
	SeedDemo                 bool
	ResourceFailureThreshold int
	ResourceCooldownSeconds  int
	InFlightLeaseTTLSeconds  int
	ClusterLockTTLSeconds    int
	GracefulShutdownSeconds  int
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	DBConnMaxLifetimeMinutes int
}

func ConfigFromEnv() Config {
	return Config{
		Environment:              getenv("TOKENHUB_ENV", "dev"),
		AdminToken:               getenv("TOKENHUB_ADMIN_TOKEN", "dev_admin_token"),
		BootstrapAdminPassword:   getenv("TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD", "admin123456"),
		PublicBaseURL:            getenv("TOKENHUB_PUBLIC_BASE_URL", ""),
		DatabaseURL:              resolveDatabaseURL(),
		SQLiteBackupDir:          getenv("TOKENHUB_SQLITE_BACKUP_DIR", defaultSQLiteBackupDir()),
		ModelCatalogFile:         getenv("TOKENHUB_MODEL_CATALOG_FILE", defaultModelCatalogFile()),
		SecretKey:                getenv("TOKENHUB_SECRET_KEY", "dev_tokenhub_secret_key"),
		TrustedProxyCIDRs:        getenvList("TOKENHUB_TRUSTED_PROXY_CIDRS"),
		CORSAllowedOrigins:       getenvList("TOKENHUB_CORS_ALLOWED_ORIGINS"),
		SeedDemo:                 getenvBool("TOKENHUB_SEED_DEMO", false),
		ResourceFailureThreshold: getenvInt("TOKENHUB_RESOURCE_FAILURE_THRESHOLD", 3),
		ResourceCooldownSeconds:  getenvInt("TOKENHUB_RESOURCE_COOLDOWN_SECONDS", 300),
		InFlightLeaseTTLSeconds:  getenvInt("TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS", 300),
		ClusterLockTTLSeconds:    getenvInt("TOKENHUB_CLUSTER_LOCK_TTL_SECONDS", 180),
		GracefulShutdownSeconds:  getenvInt("TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS", 150),
		DBMaxOpenConns:           getenvInt("TOKENHUB_DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:           getenvInt("TOKENHUB_DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetimeMinutes: getenvInt("TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES", 30),
	}
}

func (c Config) ValidateForStartup() error {
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
