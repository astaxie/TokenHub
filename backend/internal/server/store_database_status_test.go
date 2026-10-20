package server

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteFileSizesDecodeFileURIPaths(t *testing.T) {
	for _, relative := range []bool{true, false} {
		name := "absolute"
		if relative {
			name = "relative"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tenant space \u00e9.db")
			uriPath := path
			if relative {
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				uriPath, err = filepath.Rel(cwd, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			dsn := "file:" + (&url.URL{Path: filepath.ToSlash(uriPath)}).EscapedPath()
			store, err := NewSQLiteStoreWithConfig(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			sizes := sqliteFileSizes(store.sqliteDSN)
			for key, suffix := range map[string]string{"database_bytes": "", "wal_bytes": "-wal", "shm_bytes": "-shm"} {
				info, err := os.Stat(path + suffix)
				if err != nil {
					t.Fatal(err)
				}
				if got, ok := sizes[key]; !ok || got != info.Size() {
					t.Errorf("%s = %d (present %t), want %d for %s URI", key, got, ok, info.Size(), name)
				}
			}
		})
	}
}
