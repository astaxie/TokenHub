package server

import "testing"

func TestSQLiteDatabaseStorageClassification(t *testing.T) {
	tests := []struct {
		name            string
		dsn             string
		wal             bool
		connectionLocal bool
	}{
		{"bare memory", ":memory:", false, true},
		{"bare memory ignores shared cache", ":memory:?cache=shared", false, true},
		{"private memory URI", "file::memory:", false, true},
		{"escaped private memory URI", "file:%3Amemory%3A", false, true},
		{"escaped shared memory URI", "file:%3Amemory%3A?cache=shared", false, false},
		{"named private memory", "file:named?mode=memory", false, true},
		{"named shared memory", "file:named?mode=memory&cache=shared", false, false},
		{"escaped memory options", "file:named?%6dode=%6demory&%63ache=%73hared", false, false},
		{"memory name is case sensitive", "file::Memory:", true, false},
		{"escaped name is case sensitive", "file:%3AMemory%3A", true, false},
		{"mode key is case sensitive", "file:named?MODE=memory", true, false},
		{"cache key is case sensitive", "file:named?mode=memory&CACHE=shared", false, true},
		{"bare file ignores memory mode", "database.db?mode=memory&cache=shared", true, false},
		{"relative file URI", "file:database.db", true, false},
		{"absolute file URI", "file:///tmp/database.db", true, false},
		{"absolute memory filename is a file", "file:///tmp/:memory:", true, false},
		{"empty URI is temporary and private", "file:?cache=shared", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sqliteSupportsWAL(test.dsn); got != test.wal {
				t.Errorf("sqliteSupportsWAL(%q) = %v, want %v", test.dsn, got, test.wal)
			}
			if got := sqliteDatabaseIsConnectionLocal(test.dsn); got != test.connectionLocal {
				t.Errorf("sqliteDatabaseIsConnectionLocal(%q) = %v, want %v", test.dsn, got, test.connectionLocal)
			}
		})
	}
}
