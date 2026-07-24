package server

import (
	_ "gorm.io/driver/postgres"
)

// This file exists to ensure PostgreSQL driver dependency is retained in go.mod
// It will be removed once actual PostgreSQL integration is implemented
