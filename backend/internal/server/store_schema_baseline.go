package server

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// exportSQLiteBaselineStatements rebuilds the frozen SQLite baseline SQL by
// running the structural part of the frozen schema flow on a throwaway
// in-memory database and dumping its sqlite_master entries in a deterministic
// order (tables, then indexes, then triggers, each by name). The dump is the
// source of dbschema's embedded 000001 baseline file; see
// TestSQLiteBaselineSQLIsCurrent for regeneration.
func exportSQLiteBaselineStatements(ctx context.Context) ([]string, error) {
	scratchDSN := fmt.Sprintf("file:%s?mode=memory&cache=shared", NewID("schemadump"))
	db, err := gorm.Open(sqlite.Open(scratchDSN), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open sqlite baseline dump database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	if err := migrateSchemaObjects(db, "sqlite"); err != nil {
		return nil, fmt.Errorf("build sqlite baseline dump: %w", err)
	}
	rows, err := sqlDB.QueryContext(ctx,
		"SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' "+
			"ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name")
	if err != nil {
		return nil, fmt.Errorf("dump sqlite baseline statements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var statements []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()

	// The request-log commit-sequence trigger reads a seed row that the frozen
	// flow creates alongside the schema; the baseline must carry it too or
	// fresh databases fail their first request-log insert. AnalyticsSequence
	// has only deterministic columns, so the row dumps to a stable INSERT.
	return appendAnalyticsSequenceSeeds(ctx, sqlDB, "sqlite", "0", "1", statements)
}

// appendAnalyticsSequenceSeeds reads the analytics_sequences seed rows from db
// and appends a stable INSERT per row to statements. The boolean column is
// written with the dialect's literals so the dump round-trips exactly.
func appendAnalyticsSequenceSeeds(ctx context.Context, db *sql.DB, label, falseLiteral, trueLiteral string, statements []string) ([]string, error) {
	seedRows, err := db.QueryContext(ctx,
		"SELECT name, last_value, sequence_offset, history_migrated FROM analytics_sequences ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("dump %s baseline seeds: %w", label, err)
	}
	defer func() { _ = seedRows.Close() }()
	for seedRows.Next() {
		var name string
		var lastValue, sequenceOffset int64
		var historyMigrated bool
		if err := seedRows.Scan(&name, &lastValue, &sequenceOffset, &historyMigrated); err != nil {
			return nil, err
		}
		historyFlag := falseLiteral
		if historyMigrated {
			historyFlag = trueLiteral
		}
		statements = append(statements, fmt.Sprintf(
			"INSERT INTO analytics_sequences (name, last_value, sequence_offset, history_migrated) VALUES ('%s', %d, %d, %s)",
			name, lastValue, sequenceOffset, historyFlag))
	}
	return statements, seedRows.Err()
}
