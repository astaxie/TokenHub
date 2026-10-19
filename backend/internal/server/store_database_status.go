package server

import (
	"context"
	"log"
	"net/url"
	"os"
	"strings"
	"time"
)

func (s *GormStore) databasePoolStats() map[string]any {
	pools := make(map[string]any)
	for _, pool := range s.databasePools() {
		db, err := pool.db.DB()
		if err != nil {
			continue
		}
		stats := db.Stats()
		pools[pool.name] = map[string]any{
			"max_open_connections": stats.MaxOpenConnections,
			"open_connections":     stats.OpenConnections,
			"in_use":               stats.InUse,
			"idle":                 stats.Idle,
			"wait_count":           stats.WaitCount,
			"wait_duration_ms":     stats.WaitDuration.Milliseconds(),
		}
	}
	return pools
}

// GetDatabaseStatus is used only by the authenticated administration endpoint.
// Pool waits are cumulative counters, not the latency of the current request.
func (s *GormStore) GetDatabaseStatus() (map[string]interface{}, error) {
	dbType := s.db.Dialector.Name()
	_, dockerErr := os.Stat("/.dockerenv")
	status := map[string]interface{}{
		"database_type": dbType,
		"is_docker":     dockerErr == nil,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status["connection_ok"] = s.Ping(ctx) == nil
	status["pools"] = s.databasePoolStats()
	if dbType == "postgres" {
		var version string
		if err := s.db.WithContext(ctx).Raw("SELECT version()").Scan(&version).Error; err == nil {
			status["postgres_version"] = version
		}
	} else if sqliteSupportsWAL(s.sqliteDSN) {
		status["sqlite_files"] = sqliteFileSizes(s.sqliteDSN)
	}
	if databaseURL := os.Getenv("TOKENHUB_DATABASE_URL"); databaseURL != "" {
		status["database_url"] = redactDatabaseURL(databaseURL)
	}
	return status, nil
}

// Stat files without running a checkpoint, scanning tables or exposing paths.
func sqliteFileSizes(dsn string) map[string]int64 {
	path, _, _ := strings.Cut(dsn, "?")
	if strings.HasPrefix(path, "file:") {
		parsed, err := url.Parse(path)
		if err != nil {
			return nil
		}
		path = parsed.Path
		if path == "" {
			path = parsed.Opaque
		}
	}
	sizes := make(map[string]int64)
	for name, suffix := range map[string]string{"database_bytes": "", "wal_bytes": "-wal", "shm_bytes": "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil {
			sizes[name] = info.Size()
		}
	}
	return sizes
}

func (s *GormStore) Ping(ctx context.Context) error {
	pool, err := s.criticalReadDatabase().DB()
	if err != nil {
		return err
	}
	if err := pool.PingContext(ctx); err != nil {
		pingPool := "primary"
		if s.readDB != nil {
			pingPool = "read"
		}
		for _, named := range s.databasePools() {
			db, dbErr := named.db.DB()
			if dbErr != nil {
				continue
			}
			stats := db.Stats()
			log.Printf("[tokenhub] database ping failed: ping_pool=%s pool=%s max_open=%d open=%d in_use=%d idle=%d wait_count=%d wait_duration=%s error=%v",
				pingPool, named.name, stats.MaxOpenConnections, stats.OpenConnections, stats.InUse, stats.Idle, stats.WaitCount, stats.WaitDuration, err)
		}
		return err
	}
	return nil
}
