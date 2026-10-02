// Package dbupgrade plans the offline SQLite-to-PostgreSQL database
// upgrade. The plan phase is read-only: it inventories the source database,
// verifies that protected ciphertext decrypts under the resolved secret key,
// and classifies the target database so an upgrade run never starts against
// an unexpected target. The copy, re-encryption, and verification phases
// specified in docs/development/database-upgrade-design.md consume this
// package's Plan as their contract.
package dbupgrade

// Severity classifies a canary failure by how it blocks an upgrade run.
type Severity string

const (
	// SeverityBlocker marks ciphertext whose loss would break serving
	// configuration after cutover.
	SeverityBlocker Severity = "blocker"
	// SeverityWarning marks ciphertext that only affects historical
	// artifacts; the upgrade may proceed with the operator's acknowledgement.
	SeverityWarning Severity = "warning"
)

// TableGroup buckets source tables for the copy order and the plan report.
type TableGroup string

const (
	// TableGroupProtected holds tables with columns carrying enc:v1
	// ciphertext.
	TableGroupProtected TableGroup = "protected"
	// TableGroupHistory holds append-only request, usage, and billing
	// evidence tables.
	TableGroupHistory TableGroup = "history"
	// TableGroupSystem holds migration ledger and runtime coordination
	// tables that the target maintains itself.
	TableGroupSystem TableGroup = "system"
	// TableGroupOther holds everything not classified above.
	TableGroupOther TableGroup = "other"
)

// SecretKeySource names where the upgrade resolved the source secret key.
type SecretKeySource string

const (
	// SecretKeySourceFlag is an explicitly passed key.
	SecretKeySourceFlag SecretKeySource = "flag"
	// SecretKeySourceEnvironment is TOKENHUB_SECRET_KEY from the process
	// environment.
	SecretKeySourceEnvironment SecretKeySource = "environment"
	// SecretKeySourceSidecar is the .secret-key file beside a file-backed
	// SQLite database.
	SecretKeySourceSidecar SecretKeySource = "sidecar-file"
	// SecretKeySourceNone means no key resolved; the canary cannot run.
	SecretKeySourceNone SecretKeySource = "none"
)

// TargetState classifies the target database for an upgrade run.
type TargetState string

const (
	// TargetStateEmpty is a database with no tables; the run adopts the
	// PostgreSQL baseline schema into it.
	TargetStateEmpty TargetState = "empty"
	// TargetStateTokenhub is a database with a TokenHub migration ledger.
	TargetStateTokenhub TargetState = "tokenhub"
	// TargetStateUnrecognized is a non-empty database without a TokenHub
	// ledger; the run refuses it.
	TargetStateUnrecognized TargetState = "unrecognized"
)

// TableReport describes one source table.
type TableReport struct {
	Name             string   `json:"name"`
	Group            string   `json:"group"`
	RowCount         int64    `json:"row_count"`
	EncryptedColumns []string `json:"encrypted_columns,omitempty"`
}

// CanaryReport describes the ciphertext verification of one protected
// column.
type CanaryReport struct {
	Table               string `json:"table"`
	Column              string `json:"column"`
	Severity            string `json:"severity"`
	RowsScanned         int64  `json:"rows_scanned"`
	DistinctCiphertexts int    `json:"distinct_ciphertexts"`
	FailedCiphertexts   int    `json:"failed_ciphertexts"`
}

// SourceReport describes the upgrade source database.
type SourceReport struct {
	Driver    string        `json:"driver"`
	Tables    []TableReport `json:"tables"`
	TotalRows int64         `json:"total_rows"`
}

// SecretReport describes secret key resolution and canary results.
type SecretReport struct {
	KeySource string         `json:"key_source"`
	Canary    []CanaryReport `json:"canary"`
	// Verified is true when every protected column's ciphertext decrypts,
	// or when no protected column holds any ciphertext.
	Verified bool `json:"verified"`
}

// TargetReport describes the classified target database.
type TargetReport struct {
	Driver        string      `json:"driver"`
	State         TargetState `json:"state"`
	LedgerVersion int64       `json:"ledger_version,omitempty"`
	HasData       bool        `json:"has_data"`
	Detail        string      `json:"detail,omitempty"`
}

// Plan is the complete read-only upgrade preflight result.
type Plan struct {
	Source   SourceReport `json:"source"`
	Secret   SecretReport `json:"secret"`
	Target   TargetReport `json:"target"`
	Blockers []string     `json:"blockers"`
	Warnings []string     `json:"warnings"`
}

// Options configures BuildPlan.
type Options struct {
	// SourceURL is the SQLite database URL to upgrade from.
	SourceURL string
	// TargetURL is the PostgreSQL database URL to upgrade into.
	TargetURL string
	// SecretKey, when non-empty, overrides secret key resolution.
	SecretKey string
}
