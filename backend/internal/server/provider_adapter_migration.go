package server

import (
	"context"
	"sync"

	"gorm.io/gorm"
)

type providerAdapterStartupMigration func(context.Context, *gorm.DB) error

var (
	providerAdapterStartupMigrationMu sync.RWMutex
	providerAdapterStartupMigrations  []providerAdapterStartupMigration
)

func registerProviderAdapterStartupMigration(migration providerAdapterStartupMigration) {
	if migration == nil {
		return
	}
	providerAdapterStartupMigrationMu.Lock()
	defer providerAdapterStartupMigrationMu.Unlock()
	providerAdapterStartupMigrations = append(providerAdapterStartupMigrations, migration)
}

func registeredProviderAdapterStartupMigrations() []providerAdapterStartupMigration {
	providerAdapterStartupMigrationMu.RLock()
	defer providerAdapterStartupMigrationMu.RUnlock()
	return append([]providerAdapterStartupMigration(nil), providerAdapterStartupMigrations...)
}

func (s *GormStore) NormalizeProviderAdapterTypes(ctx context.Context) error {
	migrations := registeredProviderAdapterStartupMigrations()
	if len(migrations) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, migration := range migrations {
			if err := migration(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
}
