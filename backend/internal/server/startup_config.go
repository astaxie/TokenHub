package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
)

const sqliteSecretKeySuffix = ".secret-key"

// PrepareForStartup converts known production placeholders for optional
// bootstrap credentials into disabled values and provisions a durable secret
// key only when a brand-new file-backed SQLite database makes that safe.
func (c Config) PrepareForStartup() (Config, error) {
	environment := strings.ToLower(strings.TrimSpace(c.Environment))
	if isDevelopmentEnvironment(environment) || environment == "" {
		return c, nil
	}

	if isKnownPlaceholder(c.AdminToken, "dev_admin_token", "change-me-tokenhub-admin-token") {
		c.AdminToken = ""
	}
	if isKnownPlaceholder(c.BootstrapAdminPassword, "admin123456", "change-me-tokenhub-admin-password") {
		c.BootstrapAdminPassword = ""
	}
	if !isKnownPlaceholder(c.SecretKey, "dev_tokenhub_secret_key", "change-me-tokenhub-secret-key") {
		return c, nil
	}

	keyPath, databasePath, ok, err := sqliteSecretKeyPaths(c.DatabaseURL)
	if err != nil || !ok {
		return c, err
	}
	key, err := loadOrCreateSQLiteSecretKey(keyPath, databasePath)
	if err != nil {
		return c, err
	}
	c.SecretKey = key
	return c, nil
}

func isDevelopmentEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "dev", "development", "local", "test":
		return true
	default:
		return false
	}
}

func isKnownPlaceholder(value string, candidates ...string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func sqliteSecretKeyPaths(databaseURL string) (keyPath string, databasePath string, ok bool, err error) {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return "", "", false, err
	}
	if driver != "sqlite" {
		return "", "", false, nil
	}
	databasePath, ok, err = sqliteDatabaseFilePath(dsn)
	if err != nil || !ok {
		return "", "", false, err
	}
	return databasePath + sqliteSecretKeySuffix, databasePath, true, nil
}

func sqliteDatabaseFilePath(dsn string) (string, bool, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" || dsn == ":memory:" {
		return "", false, nil
	}
	if !strings.HasPrefix(dsn, "file:") {
		if queryIndex := strings.Index(dsn, "?"); queryIndex >= 0 {
			dsn = dsn[:queryIndex]
		}
		if strings.TrimSpace(dsn) == "" {
			return "", false, nil
		}
		return dsn, true, nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", false, fmt.Errorf("parse SQLite file DSN: %w", err)
	}
	if strings.EqualFold(parsed.Query().Get("mode"), "memory") {
		return "", false, nil
	}
	path := parsed.Path
	if path == "" {
		path = parsed.Opaque
	}
	if path == "" || path == ":memory:" {
		return "", false, nil
	}
	return path, true, nil
}

func loadOrCreateSQLiteSecretKey(keyPath string, databasePath string) (string, error) {
	if key, found, err := readSQLiteSecretKey(keyPath); found || err != nil {
		return key, err
	}
	if info, err := os.Stat(databasePath); err == nil && info.Size() > 0 {
		return "", fmt.Errorf("TOKENHUB_SECRET_KEY is required for existing SQLite database %s; restore %s or set the original key", databasePath, keyPath)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect SQLite database before creating TOKENHUB_SECRET_KEY: %w", err)
	}

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate TOKENHUB_SECRET_KEY: %w", err)
	}
	key := hex.EncodeToString(randomBytes)
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		persisted, found, readErr := readSQLiteSecretKey(keyPath)
		if !found && readErr == nil {
			readErr = fmt.Errorf("generated SQLite secret key file disappeared")
		}
		return persisted, readErr
	}
	if err != nil {
		return "", fmt.Errorf("create SQLite secret key file %s: %w", keyPath, err)
	}
	removeIncomplete := true
	defer func() {
		_ = file.Close()
		if removeIncomplete {
			_ = os.Remove(keyPath)
		}
	}()
	if _, err := file.WriteString(key + "\n"); err != nil {
		return "", fmt.Errorf("write SQLite secret key file %s: %w", keyPath, err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync SQLite secret key file %s: %w", keyPath, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close SQLite secret key file %s: %w", keyPath, err)
	}
	removeIncomplete = false
	log.Printf("[tokenhub] generated persistent TOKENHUB_SECRET_KEY for new SQLite database at %s", keyPath)
	return key, nil
}

func readSQLiteSecretKey(keyPath string) (string, bool, error) {
	info, err := os.Stat(keyPath)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect SQLite secret key file %s: %w", keyPath, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", true, fmt.Errorf("SQLite secret key file %s must not be readable by group or other users", keyPath)
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", true, fmt.Errorf("read SQLite secret key file %s: %w", keyPath, err)
	}
	key := strings.TrimSpace(string(data))
	if reason := weakProductionSecretReason(key, 32, "dev_tokenhub_secret_key", "change-me-tokenhub-secret-key"); reason != "" {
		return "", true, fmt.Errorf("SQLite secret key file %s contains an unsafe key: %s", keyPath, reason)
	}
	return key, true, nil
}
