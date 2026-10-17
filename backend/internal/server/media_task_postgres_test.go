//go:build integration

package server

import "testing"

func TestMediaTaskPostgresPersistence(t *testing.T) {
	first, second, _ := openSharedPostgresStores(t)
	testMediaTaskBindingPersistence(t, first, second)
}
