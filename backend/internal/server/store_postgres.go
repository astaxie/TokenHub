package server

// IsPostgreSQL reports whether the current store uses PostgreSQL.
func (s *GormStore) IsPostgreSQL() bool {
	return s.dbDriver == "postgres"
}

// IsSQLite reports whether the current store uses SQLite.
func (s *GormStore) IsSQLite() bool {
	return s.dbDriver == "sqlite"
}

// CreatePostgreSQLBackup creates a backup for PostgreSQL (using pg_dump).
func (s *GormStore) CreatePostgreSQLBackup(createdBy string, expireDays int) (SQLiteBackupRecord, error) {
	// PostgreSQL backups require the external tool pg_dump.
	// This implementation returns an unsupported error and recommends using an external backup tool.
	return SQLiteBackupRecord{}, NewHTTPError(501, "backup_not_supported",
		"PostgreSQL backup requires external tools like pg_dump. Please use your database backup solution.")
}

// RestorePostgreSQLBackup restores a PostgreSQL backup.
func (s *GormStore) RestorePostgreSQLBackup(id string, restoredBy string) (SQLiteBackupRecord, error) {
	return SQLiteBackupRecord{}, NewHTTPError(501, "restore_not_supported",
		"PostgreSQL restore requires external tools. Please use your database restore solution.")
}
