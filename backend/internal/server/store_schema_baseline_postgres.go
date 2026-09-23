//go:build integration

package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"tokenhub/backend/internal/dbschema"
)

// exportPostgresBaselineStatements rebuilds the frozen PostgreSQL baseline by
// running the structural part of the frozen schema flow on a throwaway schema
// through a recording driver and keeping the DDL statements it executed, in
// execution order. The seed row for the commit-sequence trigger is appended
// from the scratch database, mirroring the SQLite baseline dump.
func exportPostgresBaselineStatements(ctx context.Context, adminDB *sql.DB, databaseURL string) ([]string, error) {
	// PostgreSQL folds unquoted identifiers to lowercase; keep the schema
	// name lowercase so the search_path runtime parameter resolves it.
	schemaName := "tokenhub_pg_baseline_" + strings.ToLower(NewID(""))
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", dbschema.QuoteIdent(schemaName))); err != nil {
		return nil, fmt.Errorf("create postgres baseline scratch schema: %w", err)
	}
	defer func() {
		_, _ = adminDB.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", dbschema.QuoteIdent(schemaName)))
	}()

	recorder := &sqlRecorder{}
	recording, err := openRecordingPostgres(databaseURL, schemaName, recorder)
	if err != nil {
		return nil, err
	}
	defer recording.Close()
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: recording}), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open postgres baseline recording handle: %w", err)
	}
	if err := migrateSchemaObjects(gormDB, "postgres"); err != nil {
		return nil, fmt.Errorf("build postgres baseline scratch schema: %w", err)
	}
	statements := recorder.ddlStatements()

	statements, err = appendAnalyticsSequenceSeeds(ctx, recording, "postgres", "false", "true", statements)
	if err != nil {
		return nil, err
	}
	return statements, nil
}

// sqlRecorder collects every statement the wrapped connection prepares.
type sqlRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *sqlRecorder) record(statement string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, statement)
}

// ddlStatements returns the recorded statements that are DDL, in execution
// order; introspection reads and session settings are dropped.
func (r *sqlRecorder) ddlStatements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefixes := []string{
		"create table", "create index", "create unique index",
		"create function", "create or replace function",
		"alter table", "drop index", "do ",
	}
	var ddl []string
	for _, statement := range r.statements {
		trimmed := strings.TrimSpace(strings.ToLower(statement))
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				ddl = append(ddl, strings.TrimRight(strings.TrimSpace(statement), ";"))
				break
			}
		}
	}
	return ddl
}

// openRecordingPostgres opens a *sql.DB on the pgx stdlib driver that records
// every prepared statement into the recorder. Because the wrapper exposes no
// fast-path exec interfaces, database/sql routes all statements through
// Prepare, which is where the recording happens.
func openRecordingPostgres(databaseURL, schemaName string, recorder *sqlRecorder) (*sql.DB, error) {
	dsn, err := postgresSearchPathDSN(databaseURL, schemaName)
	if err != nil {
		return nil, err
	}
	driverName := "pgx-record-" + strings.ReplaceAll(NewID(""), "-", "_")
	sql.Register(driverName, &recordingDriver{inner: stdlib.GetDefaultDriver(), recorder: recorder})
	return sql.Open(driverName, dsn)
}

type recordingDriver struct {
	inner    driver.Driver
	recorder *sqlRecorder
}

func (d *recordingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: conn, recorder: d.recorder}, nil
}

type recordingConn struct {
	driver.Conn
	recorder *sqlRecorder
}

func (c *recordingConn) Prepare(query string) (driver.Stmt, error) {
	c.recorder.record(query)
	return c.Conn.Prepare(query)
}
