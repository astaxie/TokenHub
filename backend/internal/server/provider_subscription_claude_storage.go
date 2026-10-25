package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Claude Code scopes macOS keychain items to CLAUDE_CONFIG_DIR. Read only the
// random directory TokenHub created for this run, never the host's CLI account.
func claudeSubscriptionKeychainService(config string) string {
	sum := sha256.Sum256([]byte(norm.NFC.String(config)))
	return "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]
}

func claudeSubscriptionKeychainAccount() string {
	if current, err := user.Current(); err == nil && regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(current.Username) {
		return current.Username
	}
	return "claude-code-user"
}

func readClaudeSubscriptionSession(config string) ([]byte, error) {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", claudeSubscriptionKeychainService(config), "-a", claudeSubscriptionKeychainAccount(), "-w")
		if raw, err := command.Output(); err == nil && len(raw) <= 1<<20 {
			raw = bytes.TrimSpace(raw)
			if decoded, err := hex.DecodeString(string(raw)); err == nil && json.Valid(decoded) {
				raw = decoded
			}
			if json.Valid(raw) {
				return raw, nil
			}
		}
	}
	file, err := os.Open(filepath.Join(config, ".credentials.json"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, (512<<10)+1))
}

func clearClaudeSubscriptionSession(config string) {
	if config == "" {
		return
	}
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password", "-s", claudeSubscriptionKeychainService(config), "-a", claudeSubscriptionKeychainAccount()).Run()
	}
}
