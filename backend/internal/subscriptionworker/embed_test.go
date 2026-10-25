package subscriptionworker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRuntimeContainsOnlyBundledAssets(t *testing.T) {
	directory := t.TempDir()
	if err := WriteRuntime(directory); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"worker.mjs", "host.mjs", "cursor.mjs", "gcp-ai.mjs", "factory.mjs", "qwenwork.mjs", "vendor/PROVENANCE.json", "vendor/LICENSE", "vendor/cursor/native-session.mjs", "vendor/factory/client-phrases.json"} {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("runtime asset %s has permissions %o", path, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "worker.test.mjs")); !os.IsNotExist(err) {
		t.Fatal("test sources must not be extracted into the runtime")
	}
}
