package dbschema

import (
	"context"
	"database/sql"
	"testing"
)

const (
	originalMeteringChecksumForTest     = "4d282c33fb83adcf772a560ddb2b772116e9f6bbd3ecee0a68bda70fc447e50c"
	intermediateMeteringChecksumForTest = "tokenhub-schema-metering-evidence-v2"
)

// This fixture pins the SQL released in fdb197340 independently of the live
// registry, so a future registry change cannot silently change the test input.
func historicalMeteringMigrationForTest() Migration {
	return Migration{
		Version: 4,
		Name:    "add-metering-evidence",
		Phase:   PhaseExpand,
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS metering_entries (id text PRIMARY KEY, kind text NOT NULL, scope text NOT NULL, payload text NOT NULL, created_at timestamp NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_metering_entries_scope ON metering_entries (kind, scope, created_at)`,
		},
	}
}

func seedHistoricalMeteringLedger(t *testing.T, checksum string) (*sql.DB, Applied) {
	t.Helper()
	db := newTestDB(t)
	migration := historicalMeteringMigrationForTest()
	if got := migration.Checksum(); got != originalMeteringChecksumForTest {
		t.Fatalf("historical SQL checksum = %q, want %q", got, originalMeteringChecksumForTest)
	}
	runner := mustRunner(t, db, []Migration{migration})
	if _, err := runner.Adopt(context.Background(), nil); err != nil {
		t.Fatalf("create historical database: %v", err)
	}
	if checksum != originalMeteringChecksumForTest {
		if _, err := db.Exec("UPDATE schema_migrations SET checksum = ? WHERE version = 4", checksum); err != nil {
			t.Fatalf("set historical checksum: %v", err)
		}
	}
	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("read historical ledger: %v", err)
	}
	row := findApplied(status.Applied, 4)
	if row == nil {
		t.Fatal("historical migration was not recorded")
	}
	return db, *row
}

func TestHistoricalMeteringChecksumsPreserveAppliedLedger(t *testing.T) {
	for _, tc := range []struct{ name, checksum string }{
		{"original", originalMeteringChecksumForTest},
		{"intermediate", intermediateMeteringChecksumForTest},
	} {
		for _, operation := range []string{"verify", "migrate", "adopt"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				db, before := seedHistoricalMeteringLedger(t, tc.checksum)
				runner := mustRunner(t, db, []Migration{
					historicalMeteringMigrationForTest(),
					{Version: 5, Name: "following-expand", Statements: []string{"CREATE TABLE following_expand (id INTEGER PRIMARY KEY)"}},
				})
				ctx := context.Background()
				switch operation {
				case "verify":
					if err := runner.Verify(ctx); err != nil {
						t.Fatalf("verify historical ledger: %v", err)
					}
				case "migrate", "adopt":
					var result Result
					var err error
					if operation == "migrate" {
						result, err = runner.Migrate(ctx)
					} else {
						result, err = runner.Adopt(ctx, func(context.Context) error {
							t.Fatal("existing ledger must not rerun legacy adoption")
							return nil
						})
					}
					if err != nil {
						t.Fatalf("%s historical ledger: %v", operation, err)
					}
					if result.Adopted || len(result.Applied) != 1 || result.Applied[0].Version != 5 {
						t.Fatalf("%s must only apply the following expansion: %+v", operation, result)
					}
				}
				status, err := runner.Status(ctx)
				if err != nil {
					t.Fatalf("read resulting ledger: %v", err)
				}
				after := findApplied(status.Applied, 4)
				if after == nil || *after != before {
					t.Fatalf("historical ledger row changed: before=%+v after=%+v", before, after)
				}
				if got := tableExists(t, db, "following_expand"); got != (operation != "verify") {
					t.Fatalf("following expansion exists = %t after %s", got, operation)
				}
			})
		}
	}
}

func TestHistoricalMeteringChecksumExceptionRejectsUnrecognizedState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Migration, *Applied)
		code   string
	}{
		{"unknown_checksum", func(_ *Migration, row *Applied) { row.Checksum = "unrecognized-metering-checksum" }, ErrCodeChecksumMismatch},
		{"changed_sql", func(m *Migration, _ *Applied) { m.Statements[1] += " -- changed after release" }, ErrCodeChecksumMismatch},
		{"another_version", func(m *Migration, row *Applied) { m.Version, row.Version = 6, 6 }, ErrCodeChecksumMismatch},
		{"another_migration_name", func(m *Migration, row *Applied) { m.Name, row.Name = "different-migration", "different-migration" }, ErrCodeChecksumMismatch},
		{"different_ledger_name", func(_ *Migration, row *Applied) { row.Name = "different-migration" }, ErrCodeChecksumMismatch},
		{"another_phase", func(m *Migration, row *Applied) { m.Phase, row.Phase = PhaseContract, PhaseContract }, ErrCodeChecksumMismatch},
		{"different_ledger_phase", func(_ *Migration, row *Applied) { row.Phase = PhaseContract }, ErrCodeChecksumMismatch},
		{"dialect_specific", func(m *Migration, _ *Applied) { m.Dialect = DialectSQLite }, ErrCodeChecksumMismatch},
		{"different_dialect", func(m *Migration, _ *Applied) { m.Dialect = DialectPostgres }, ErrCodeUnknownApplied},
		{"dirty", func(_ *Migration, row *Applied) { row.Dirty = true }, ErrCodeDirtyState},
		{"checksum_override", func(m *Migration, _ *Applied) { m.ChecksumOverride = originalMeteringChecksumForTest }, ErrCodeChecksumMismatch},
		{"go_callback", func(m *Migration, _ *Applied) {
			m.Statements = nil
			m.Go = func(context.Context, MigrationExecer) error { return nil }
			m.ChecksumOverride = originalMeteringChecksumForTest
		}, ErrCodeChecksumMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, row := seedHistoricalMeteringLedger(t, intermediateMeteringChecksumForTest)
			migration := historicalMeteringMigrationForTest()
			tc.change(&migration, &row)
			if _, err := db.Exec("UPDATE schema_migrations SET version = ?, name = ?, phase = ?, checksum = ?, dirty = ? WHERE version = 4",
				row.Version, row.Name, row.Phase, row.Checksum, row.Dirty); err != nil {
				t.Fatalf("configure unrecognized ledger: %v", err)
			}
			runner := mustRunner(t, db, []Migration{
				migration,
				{Version: 7, Name: "blocked-expand", Statements: []string{"CREATE TABLE blocked_expand (id INTEGER PRIMARY KEY)"}},
			})
			ctx := context.Background()
			requireErrorCode(t, runner.Verify(ctx), tc.code)
			_, err := runner.Migrate(ctx)
			requireErrorCode(t, err, tc.code)
			_, err = runner.Adopt(ctx, nil)
			requireErrorCode(t, err, tc.code)
			if tableExists(t, db, "blocked_expand") {
				t.Fatal("unrecognized ledger must prevent pending expansion")
			}
			status, err := runner.Status(ctx)
			if err != nil {
				t.Fatalf("read rejected ledger: %v", err)
			}
			if after := findApplied(status.Applied, row.Version); after == nil || *after != row {
				t.Fatalf("rejected ledger row changed: before=%+v after=%+v", row, after)
			}
		})
	}
}
