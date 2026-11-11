package dbupgrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"tokenhub/backend/internal/server"
)

// canaryBatchSize bounds the rows fetched per query so a large table is
// verified with constant memory.
const canaryBatchSize = 500

// runCanary verifies that every registered protected column present in the
// source decrypts under secretKey. Distinct protected values are verified
// once; repeated values across rows cost nothing.
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
// distinct protected value it stores, as a complete value: a scalar column
// holds one whole ciphertext, and a JSON column such as providers.headers
// embeds individually encrypted strings. The store layer's decryptSecret
// silently returns an empty string on failure, so this scan is the only
// place that can distinguish intact ciphertext from ciphertext the resolved
// key can no longer open.
func canaryColumn(ctx context.Context, db *sql.DB, ref encryptedColumnRef, secretKey string) (CanaryReport, error) {
	verifier := newColumnVerifier(secretKey, ref)
	var lastRowID int64
	for {
		query := fmt.Sprintf("SELECT rowid, %q FROM %q WHERE rowid > ? ORDER BY rowid LIMIT %d",
			ref.Column, ref.Table, canaryBatchSize)
		rows, err := db.QueryContext(ctx, query, lastRowID)
		if err != nil {
			return verifier.report, fmt.Errorf("scan %s.%s: %w", ref.Table, ref.Column, err)
		}
		fetched := 0
		for rows.Next() {
			var value sql.NullString
			if err := rows.Scan(&lastRowID, &value); err != nil {
				_ = rows.Close()
				return verifier.report, fmt.Errorf("scan %s.%s row: %w", ref.Table, ref.Column, err)
			}
			fetched++
			verifier.report.RowsScanned++
			if !value.Valid {
				continue
			}
			if ref.JSON {
				verifier.verifyJSONDocument(value.String)
			} else {
				verifier.verifyProtectedString(value.String)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return verifier.report, fmt.Errorf("scan %s.%s: %w", ref.Table, ref.Column, err)
		}
		_ = rows.Close()
		if fetched < canaryBatchSize {
			break
		}
	}
	verifier.report.FailedCiphertexts = verifier.failures
	return verifier.report, nil
}

// columnVerifier accumulates one protected column's canary result and
// verifies each distinct protected value exactly once.
type columnVerifier struct {
	secretKey string
	report    CanaryReport
	seen      map[string]bool
	failures  int
}

func newColumnVerifier(secretKey string, ref encryptedColumnRef) *columnVerifier {
	return &columnVerifier{
		secretKey: secretKey,
		report:    CanaryReport{Table: ref.Table, Column: ref.Column, Severity: string(ref.Severity)},
		seen:      make(map[string]bool),
	}
}

// verifyProtectedString counts one candidate value and verifies it when it
// carries the protected marker. Values without the marker hold no
// ciphertext and pass; anything else must be one complete ciphertext that
// decrypts, so a truncated value, appended garbage, or the marker embedded
// mid-value fails instead of verifying only a ciphertext-looking prefix.
func (v *columnVerifier) verifyProtectedString(value string) {
	if !strings.Contains(value, server.SecretCiphertextPrefix) {
		return
	}
	if !v.countValue(value) {
		return
	}
	if !protectedStringDecrypts(v.secretKey, value) {
		v.failures++
	}
}

// verifyJSONDocument verifies every protected string inside a JSON document
// column whose values are encrypted individually, such as
// providers.headers. A document that carries the protected marker but
// cannot be parsed cannot be verified and counts as one failed value.
func (v *columnVerifier) verifyJSONDocument(document string) {
	if !strings.Contains(document, server.SecretCiphertextPrefix) {
		return
	}
	var node any
	if err := json.Unmarshal([]byte(document), &node); err != nil {
		if v.countValue(document) {
			v.failures++
		}
		return
	}
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		case string:
			v.verifyProtectedString(typed)
		}
	}
	walk(node)
}

// countValue dedupes one protected value, reporting whether it is new.
func (v *columnVerifier) countValue(value string) bool {
	if v.seen[value] {
		return false
	}
	v.seen[value] = true
	v.report.DistinctCiphertexts++
	return true
}

// protectedStringDecrypts reports whether a marker-carrying value is one
// complete ciphertext that decrypts under the key. A value that only
// embeds the marker mid-string is malformed — indistinguishable from
// corruption — and fails.
func protectedStringDecrypts(secretKey, value string) bool {
	if !strings.HasPrefix(value, server.SecretCiphertextPrefix) {
		return false
	}
	return server.VerifySecretCiphertext(secretKey, value)
}
