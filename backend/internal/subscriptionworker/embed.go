// Package subscriptionworker contains the trusted, statically selected subscription
// adapters shipped with TokenHub. It is separate from the external plugin runtime.
package subscriptionworker

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed worker.mjs host.mjs registry.mjs network.mjs isolation.mjs validation.mjs grok.mjs gcp-ai.mjs cursor.mjs factory.mjs qwenwork.mjs vendor
var runtimeFiles embed.FS

// WriteRuntime extracts the versioned worker into a caller-owned private directory.
// The caller starts worker.mjs with a supported Node runtime and an isolated home.
func WriteRuntime(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return fs.WalkDir(runtimeFiles, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(directory, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		body, err := runtimeFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read subscription worker asset: %w", err)
		}
		return os.WriteFile(target, body, 0o600)
	})
}
