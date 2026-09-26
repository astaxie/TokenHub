package dbupgrade

// encryptedColumns registers every column that persists enc:v1 ciphertext,
// with the severity of an upgrade that cannot decrypt it. The registry is
// derived from the store layer's encryptSecret call sites; a new protected
// column must be added here in the same change so the canary keeps covering
// it.
var encryptedColumns = []encryptedColumnRef{
	// Serving configuration: unreadable credentials break provider calls
	// immediately after cutover. providers.headers is a JSON map whose
	// sensitive values are encrypted individually, so the canary scans the
	// raw column text for embedded tokens rather than treating it as one
	// ciphertext.
	{Table: "providers", Column: "api_key", Severity: SeverityBlocker},
	{Table: "providers", Column: "headers", Severity: SeverityBlocker},
	{Table: "provider_resources", Column: "api_key", Severity: SeverityBlocker},
	{Table: "provider_resources", Column: "credential_blob", Severity: SeverityBlocker},
	{Table: "billing_connectors", Column: "credential_ciphertext", Severity: SeverityBlocker},
	{Table: "bootstrap_credentials", Column: "ciphertext", Severity: SeverityBlocker},
	// Historical artifacts: failed decryption loses past evidence, not
	// serving configuration.
	{Table: "image_jobs", Column: "prompt_ciphertext", Severity: SeverityWarning},
	{Table: "image_jobs", Column: "revised_prompt_ciphertext", Severity: SeverityWarning},
	{Table: "response_jobs", Column: "request_ciphertext", Severity: SeverityWarning},
	{Table: "response_jobs", Column: "result_ciphertext", Severity: SeverityWarning},
	// Short-lived OAuth session records expire on their own; they are
	// transient by design. GORM snake-cases the struct name with an
	// o_auth split.
	{Table: "provider_account_o_auth_session_records", Column: "code_verifier_encrypted", Severity: SeverityWarning},
}

type encryptedColumnRef struct {
	Table    string
	Column   string
	Severity Severity
}

// historyTables lists append-only evidence tables. They are copied last and
// in batches because they dominate the row count of a long-lived gateway.
var historyTables = []string{
	"usage_records",
	"request_logs",
	"request_payload_logs",
	"route_attempt_logs",
	"audit_events",
	"image_jobs",
	"response_jobs",
	"response_job_events",
	"billing_records",
	"billing_raw_snapshots",
	"billing_sync_runs",
}

// systemTables lists tables the target maintains itself. The migration
// ledger belongs to the target's own adoption flow, so an upgrade run never
// copies these rows.
var systemTables = []string{
	"schema_migrations",
	"migration_attempts",
	"analytics_sequences",
	"instance_heartbeats",
	"sqlite_backup_records",
}

// encryptedColumnsForTable returns the registered protected columns of one
// table.
func encryptedColumnsForTable(table string) []string {
	var columns []string
	for _, ref := range encryptedColumns {
		if ref.Table == table {
			columns = append(columns, ref.Column)
		}
	}
	return columns
}

// tableGroup classifies one table for the plan report.
func tableGroup(table string, hasEncryptedColumns bool) TableGroup {
	switch {
	case hasEncryptedColumns:
		return TableGroupProtected
	case contains(historyTables, table):
		return TableGroupHistory
	case contains(systemTables, table):
		return TableGroupSystem
	default:
		return TableGroupOther
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// registryTablesMissingFrom lists registry tables the source does not have,
// so schema drift against the encrypted-column registry is visible instead
// of silently skipping those columns' canary.
func registryTablesMissingFrom(tables []string) []string {
	var missing []string
	seen := make(map[string]bool)
	for _, ref := range encryptedColumns {
		if seen[ref.Table] || tableExists(tables, ref.Table) {
			continue
		}
		seen[ref.Table] = true
		missing = append(missing, ref.Table)
	}
	return missing
}
