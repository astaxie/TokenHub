package dbupgrade

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"

	"tokenhub/backend/internal/server"
)

// ciphertextTokenPattern matches enc:v1 payloads inside raw column text. It
// finds both plain ciphertext columns and ciphertext embedded in JSON
// documents such as providers.sensitive_headers.
var ciphertextTokenPattern = regexp.MustCompile(regexp.QuoteMeta(server.SecretCiphertextPrefix) + `[A-Za-z0-9_-]+`)

// canaryBatchSize bounds the rows fetched per query so a large table is
// verified with constant memory.
const canaryBatchSize = 500

// runCanary verifies that every registered protected column present in the
// source decrypts under secretKey. Distinct ciphertext is verified once;
// repeated values across rows cost nothing.
func runCanary(ctx context.Context, db *sql.DB, tables []string, secretKey string) ([]CanaryReport, error) {
	var reports []CanaryReport
	for _, ref := range encryptedColumns {
		if !tableExists(tables, ref.Table) {
			continue
		}
		report, err := canaryColumn(ctx, db, ref, secretKey)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// canaryColumn scans one protected column in rowid pages and verifies every
// distinct ciphertext token it contains. The store layer's decryptSecret
// silently returns an empty string on failure, so this scan is the only
// place that can distinguish intact ciphertext from ciphertext the resolved
// key can no longer open.
func canaryColumn(ctx context.Context, db *sql.DB, ref encryptedColumnRef, secretKey string) (CanaryReport, error) {
	report := CanaryReport{Table: ref.Table, Column: ref.Column, Severity: string(ref.Severity)}
	seen := make(map[string]bool)
	failures := 0
	var lastRowID int64
	for {
		query := fmt.Sprintf("SELECT rowid, %q FROM %q WHERE rowid > ? ORDER BY rowid LIMIT %d",
			ref.Column, ref.Table, canaryBatchSize)
		rows, err := db.QueryContext(ctx, query, lastRowID)
		if err != nil {
			return report, fmt.Errorf("scan %s.%s: %w", ref.Table, ref.Column, err)
		}
		fetched := 0
		for rows.Next() {
			var value sql.NullString
			if err := rows.Scan(&lastRowID, &value); err != nil {
				_ = rows.Close()
				return report, fmt.Errorf("scan %s.%s row: %w", ref.Table, ref.Column, err)
			}
			fetched++
			report.RowsScanned++
			if !value.Valid {
				continue
			}
			for _, token := range ciphertextTokenPattern.FindAllString(value.String, -1) {
				if seen[token] {
					continue
				}
				seen[token] = true
				report.DistinctCiphertexts++
				if !server.VerifySecretCiphertext(secretKey, token) {
					failures++
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return report, fmt.Errorf("scan %s.%s: %w", ref.Table, ref.Column, err)
		}
		_ = rows.Close()
		if fetched < canaryBatchSize {
			break
		}
	}
	report.FailedCiphertexts = failures
	return report, nil
}
