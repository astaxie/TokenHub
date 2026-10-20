package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SQLite writes stay serialized. Small, read-only WAL pools prevent auditing
// and analytics from occupying every connection needed by authentication,
// routing, settings refreshes and readiness checks.
const sqliteCriticalReadConnections = 2

type namedDatabasePool struct {
	name string
	db   *gorm.DB
}

func (s *GormStore) databasePools() []namedDatabasePool {
	pools := make([]namedDatabasePool, 0, 3)
	seen := make(map[*sql.DB]bool)
	for _, pool := range []namedDatabasePool{{"primary", s.db}, {"read", s.readDB}, {"analytics", s.analyticsDB}} {
		if pool.db != nil {
			if db, err := pool.db.DB(); err == nil {
				if seen[db] {
					continue
				}
				seen[db] = true
			}
			pools = append(pools, pool)
		}
	}
	return pools
}

func (s *GormStore) criticalReadDatabase() *gorm.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *GormStore) withAnalyticsReadSnapshot(ctx context.Context, read func(*gorm.DB) error) error {
	db := s.analyticsDB.WithContext(ctx)
	if s.dbDriver == "postgres" {
		return db.Transaction(read, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	}
	if s.analyticsDB.Statement.ConnPool == s.db.Statement.ConnPool {
		// Private in-memory SQLite has only one connection. Holding it excludes
		// every writer and keeps the snapshot stable without a transaction.
		// A transaction tied to ctx would discard that sole connection on
		// cancellation and destroy the database. Connection still honors ctx
		// while waiting for the pool; queries retain their own request context.
		return db.Connection(func(conn *gorm.DB) error {
			return read(conn.Session(&gorm.Session{NewDB: true}))
		})
	}
	return db.Transaction(read)
}

// Driver DSN options are applied whenever database/sql opens a connection,
// including replacements after cancellation or connection lifetime expiry.
func sqliteConnectionDSN(dsn string, readOnly bool) (string, error) {
	path, rawQuery, _ := strings.Cut(dsn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("invalid SQLite connection options: %w", err)
	}
	// Remove aliases, which otherwise override their canonical counterparts in
	// go-sqlite3 and can silently disable the store's required guarantees.
	query.Del("_fk")
	query.Del("_timeout")
	query.Set("_foreign_keys", "1")
	query.Set("_busy_timeout", "5000")
	query.Set("_query_only", "0")
	if sqliteSupportsWAL(dsn) {
		query.Del("_journal")
		query.Set("_journal_mode", "WAL")
	}
	if readOnly {
		query.Set("_busy_timeout", "1000")
		query.Set("_query_only", "1")
		// Read transactions must remain deferred even when the write DSN was
		// configured to acquire its writer lock immediately.
		query.Set("_txlock", "deferred")
	}
	return path + "?" + query.Encode(), nil
}

func openCriticalReadDatabase(driver, dsn string) (*gorm.DB, error) {
	// Private/shared in-memory SQLite databases use different locking and
	// connection-lifetime semantics; preserve their existing primary pool.
	if driver != "sqlite" || !sqliteSupportsWAL(dsn) {
		return nil, nil
	}
	return openSQLiteReadDatabase(dsn, sqliteCriticalReadConnections)
}

func openSQLiteReadDatabase(dsn string, maxOpen int) (*gorm.DB, error) {
	dsn, err := sqliteConnectionDSN(dsn, true)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(maxOpen)
	pool.SetMaxIdleConns(maxOpen)
	return db, nil
}

func openAnalyticsDatabase(driver string, dsn string, config Config, primary *gorm.DB) (*gorm.DB, error) {
	if driver == "sqlite" {
		// A private in-memory database belongs to its connection. Opening a
		// separate pool creates an empty database, so analytics must share the
		// existing primary pool without enabling query_only on its writer.
		if sqliteDatabaseIsConnectionLocal(dsn) {
			return primary, nil
		}
		return openSQLiteReadDatabase(dsn, 1)
	}
	if driver != "postgres" {
		return nil, fmt.Errorf("unsupported analytics database driver: %s", driver)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	poolSize := min(2, maxInt(1, defaultInt(config.DBMaxOpenConns, 25)))
	pool.SetMaxOpenConns(poolSize)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(time.Duration(defaultInt(config.DBConnMaxLifetimeMinutes, 30)) * time.Minute)
	return db, nil
}

func sqliteSupportsWAL(dsn string) bool {
	path, options := sqliteDatabaseLocation(dsn)
	return path != "" && path != ":memory:" && options.Get("mode") != "memory" && options.Get("vfs") != "memdb"
}

func sqliteDatabaseIsConnectionLocal(dsn string) bool {
	path, options := sqliteDatabaseLocation(dsn)
	if path == "" || path == ":memory:" || options.Get("mode") == "memory" {
		return path == "" || options.Get("cache") != "shared"
	}
	// memdb shares named stores with a leading slash or backslash, or through
	// SQLite's shared cache. Other names belong to the opening connection.
	if options.Get("vfs") == "memdb" {
		sharedName := len(path) > 1 && (path[0] == '/' || path[0] == '\\')
		return !sharedName && options.Get("cache") != "shared"
	}
	return false
}

// Match SQLite URI decoding without changing case. go-sqlite3 removes query
// options from non-file DSNs before opening SQLite, so mode/cache have no
// effect there; vfs is passed separately and still applies to bare DSNs.
func sqliteDatabaseLocation(dsn string) (string, url.Values) {
	path, rawQuery, _ := strings.Cut(dsn, "?")
	if !strings.HasPrefix(path, "file:") {
		options, _ := url.ParseQuery(rawQuery)
		options.Del("mode")
		options.Del("cache")
		return path, options
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return path, nil
	}
	path = parsed.Path
	if parsed.Opaque != "" {
		path, err = url.PathUnescape(parsed.Opaque)
		if err != nil {
			return "", nil
		}
	}
	return path, parsed.Query()
}
