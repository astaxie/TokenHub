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
		{"private memdb VFS", "file:private?vfs=memdb", false, true},
		{"escaped memdb VFS", "file:private?%76fs=%6demdb", false, true},
		{"bare private memdb VFS", "private?vfs=memdb&cache=shared", false, true},
		{"shared memdb VFS", "file:/shared?vfs=memdb", false, false},
		{"shared memdb private cache", "file:/shared?vfs=memdb&cache=private", false, false},
		{"escaped shared memdb VFS", "file:%2Fshared?vfs=memdb", false, false},
		{"backslash shared memdb VFS", "file:%5Cshared?vfs=memdb", false, false},
		{"bare shared memdb VFS", "/shared?vfs=memdb", false, false},
		{"relative memdb shared cache", "file:shared?vfs=memdb&cache=shared", false, false},
		{"memory mode overrides memdb sharing", "file:/private?vfs=memdb&mode=memory", false, true},
		{"shared memory mode with memdb", "file:/shared?vfs=memdb&mode=memory&cache=shared", false, false},
		{"memdb slash without name", "file:/?vfs=memdb", false, true},
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
