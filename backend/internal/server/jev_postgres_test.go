//go:build integration

package server

import "testing"

func TestJevPostgresResponseBinding(t *testing.T) {
	first, second, _ := openSharedPostgresStores(t)
	testJevBindingPersistence(t, first, second)
	testJevBackgroundBindingAtomicCompletion(t, first)
}
