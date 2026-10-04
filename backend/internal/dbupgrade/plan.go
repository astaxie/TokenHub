package dbupgrade

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tokenhub/backend/internal/server"
)

// BuildPlan runs the read-only upgrade preflight: it inventories the source
// SQLite database, resolves the source secret key, verifies protected
// ciphertext with it, and classifies the target database. It never writes
// to either database. A returned error means the preflight itself could not
// run; findings land in Plan.Blockers and Plan.Warnings instead.
func BuildPlan(ctx context.Context, opts Options) (*Plan, error) {
	plan := &Plan{Blockers: []string{}, Warnings: []string{}}

	sourceURL, err := sqliteSourceReadOnlyURL(opts.SourceURL)
	if err != nil {
		return nil, err
	}

	secretKey, keySource, keyNotice := resolveSecretKey(opts.SourceURL, opts.SecretKey)
	plan.Secret.KeySource = string(keySource)
	if keyNotice != "" {
		plan.Warnings = append(plan.Warnings, keyNotice)
	}

	driver, db, err := server.OpenRawDatabase(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if driver != "sqlite" {
		return nil, fmt.Errorf("upgrade source must be a SQLite database, got driver %q", driver)
	}
	plan.Source.Driver = driver

	tables, err := listTables(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		encrypted := encryptedColumnsForTable(table)
		count, err := rowCount(ctx, db, table)
		if err != nil {
			return nil, err
		}
		plan.Source.Tables = append(plan.Source.Tables, TableReport{
			Name:             table,
			Group:            string(tableGroup(table, len(encrypted) > 0)),
			RowCount:         count,
			EncryptedColumns: encrypted,
		})
		plan.Source.TotalRows += count
	}
	// A registry entry that matches no table means the schema and the
	// encrypted-column registry drifted; surface it instead of silently
	// skipping the column's canary.
	for _, unknown := range registryTablesMissingFrom(tables) {
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("encrypted-column registry references unknown table %s; update backend/internal/dbupgrade/registry.go", unknown))
	}

	if keySource == SecretKeySourceNone {
		if protectedRows(plan) > 0 {
			plan.Blockers = append(plan.Blockers,
				"no source secret key resolved; pass --secret-key, set TOKENHUB_SECRET_KEY, or restore the .secret-key sidecar file before upgrading")
			plan.Secret.Verified = false
		} else {
			plan.Warnings = append(plan.Warnings,
				"no source secret key resolved, but no protected column holds rows")
			plan.Secret.Verified = true
		}
	} else {
		reports, err := runCanary(ctx, db, tables, secretKey)
		if err != nil {
			return nil, err
		}
		plan.Secret.Canary = reports
		plan.Secret.Verified = true
		for _, report := range reports {
			if report.FailedCiphertexts == 0 {
				continue
			}
			message := fmt.Sprintf("protected column %s.%s: %d of %d distinct ciphertext value(s) do not decrypt",
				report.Table, report.Column, report.FailedCiphertexts, report.DistinctCiphertexts)
			if report.Severity == string(SeverityBlocker) {
				plan.Blockers = append(plan.Blockers, message)
				plan.Secret.Verified = false
			} else {
				plan.Warnings = append(plan.Warnings, message)
			}
		}
	}

	target, err := classifyTargetURL(ctx, opts.TargetURL)
	if err != nil {
		return nil, err
	}
	plan.Target = target
	switch target.State {
	case TargetStateUnrecognized:
		plan.Blockers = append(plan.Blockers,
			"target database is not empty and has no TokenHub migration ledger; the upgrade requires an empty target")
	case TargetStateTokenhub:
		if target.HasData {
			plan.Blockers = append(plan.Blockers,
				"target database already holds TokenHub data; the upgrade requires an empty target")
		} else {
			plan.Warnings = append(plan.Warnings,
				fmt.Sprintf("target holds an adopted TokenHub schema at ledger version %d with no data", target.LedgerVersion))
		}
	}
	return plan, nil
}

// classifyTargetURL classifies the target database. A SQLite-style URL
// whose file does not exist yet is reported as empty without connecting,
// because connecting to a missing SQLite file would create it as a side
// effect of a read-only preflight.
func classifyTargetURL(ctx context.Context, targetURL string) (TargetReport, error) {
	if missing, name := sqliteTargetFileMissing(targetURL); missing {
		return TargetReport{
			Driver: "sqlite",
			State:  TargetStateEmpty,
			Detail: fmt.Sprintf("target file %s does not exist yet; the upgrade run initializes it", name),
		}, nil
	}
	driver, db, err := server.OpenRawDatabase(targetURL)
	if err != nil {
		return TargetReport{}, fmt.Errorf("open target database: %w", err)
	}
	defer func() { _ = db.Close() }()
	report, err := classifyTarget(ctx, db, driver)
	if err != nil {
		return TargetReport{}, fmt.Errorf("classify target database: %w", err)
	}
	return report, nil
}

// sqliteTargetFileMissing reports whether the URL names a SQLite file that
// does not exist yet. The prefix check is only a pre-connect guard for the
// side-effect-free plan; the authoritative dialect decision remains the
// store layer's URL parser.
func sqliteTargetFileMissing(targetURL string) (bool, string) {
	trimmed := strings.TrimSpace(targetURL)
	for _, prefix := range []string{"sqlite://", "sqlite:"} {
		if strings.HasPrefix(trimmed, prefix) {
			path := strings.SplitN(strings.TrimPrefix(trimmed, prefix), "?", 2)[0]
			if path == "" {
				return false, ""
			}
			return fileMissing(path), path
		}
	}
	return false, ""
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// sqliteSourceReadOnlyURL validates that the source names an existing
// SQLite database and rewrites its URL to open the file read-only.
// Connecting to a missing SQLite file would create it, so a typo in --from
// used to produce a zero-byte source that planned as an empty, READY
// upgrade. URLs for other drivers pass through unchanged and fail the
// driver check after opening.
func sqliteSourceReadOnlyURL(sourceURL string) (string, error) {
	trimmed := strings.TrimSpace(sourceURL)
	var prefix string
	switch {
	case strings.HasPrefix(trimmed, "sqlite://"):
		prefix = "sqlite://"
	case strings.HasPrefix(trimmed, "sqlite:"):
		prefix = "sqlite:"
	case strings.Contains(trimmed, "://"), isKeywordStyleURL(trimmed):
		return trimmed, nil
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	path, query := rest, ""
	if idx := strings.Index(rest, "?"); idx >= 0 {
		path, query = rest[:idx], rest[idx+1:]
	}
	// In-memory and file: DSNs carry driver URI semantics of their own.
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return trimmed, nil
	}
	if fileMissing(path) {
		return "", fmt.Errorf("open source database: source file %s does not exist; point --from at the SQLite deployment to upgrade", path)
	}
	return prefix + path + "?" + withReadOnlyMode(query), nil
}

// isKeywordStyleURL reports whether the URL is a PostgreSQL keyword DSN
// such as "host=db user=tokenhub dbname=tokenhub" rather than a SQLite
// path. Keyword DSNs have no scheme and begin with a connection keyword.
func isKeywordStyleURL(databaseURL string) bool {
	first := strings.SplitN(databaseURL, "=", 2)
	if len(first) != 2 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(first[0])) {
	case "host", "hostaddr", "user", "dbname", "port", "password", "sslmode":
		return true
	}
	return false
}

// withReadOnlyMode merges a mode=ro parameter into a SQLite DSN query,
// replacing any mode the URL already carried, so no connection the
// preflight opens can write to the source.
func withReadOnlyMode(query string) string {
	var parts []string
	for _, part := range strings.Split(query, "&") {
		if part == "" || strings.HasPrefix(part, "mode=") {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(append(parts, "mode=ro"), "&")
}

// protectedRows totals the source rows in tables that carry protected
// columns.
func protectedRows(plan *Plan) int64 {
	var total int64
	for _, table := range plan.Source.Tables {
		if len(table.EncryptedColumns) > 0 {
			total += table.RowCount
		}
	}
	return total
}

// resolveSecretKey picks the source secret key: an explicit value wins,
// then TOKENHUB_SECRET_KEY from the environment, then the .secret-key
// sidecar file beside the source database. The returned notice explains a
// sidecar that exists but cannot be used.
func resolveSecretKey(sourceURL string, explicit string) (string, SecretKeySource, string) {
	if key := strings.TrimSpace(explicit); key != "" {
		return key, SecretKeySourceFlag, ""
	}
	if key := strings.TrimSpace(os.Getenv("TOKENHUB_SECRET_KEY")); key != "" {
		return key, SecretKeySourceEnvironment, ""
	}
	key, found, err := server.ReadSQLiteSecretKeySidecar(sourceURL)
	if err != nil {
		return "", SecretKeySourceNone, fmt.Sprintf("read .secret-key sidecar: %v", err)
	}
	if found {
		return key, SecretKeySourceSidecar, ""
	}
	return "", SecretKeySourceNone, ""
}
