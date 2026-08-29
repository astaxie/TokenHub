package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunProviderPassesGoProviderSample(t *testing.T) {
	root := moduleRoot(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"provider", "--package", filepath.Join(root, "samples", "provider-mock-go")}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run provider contract: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "provider contract passed (7 cases") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunActionPassesGoActionSample(t *testing.T) {
	root := moduleRoot(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"action", "--package", filepath.Join(root, "samples", "action-echo-go")}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run action contract: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "action contract passed (2 cases") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunHookPassesGoTraceHookSample(t *testing.T) {
	root := moduleRoot(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"hook", "--package", filepath.Join(root, "samples", "hook-trace-go")}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run hook contract: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "hook contract passed (1 cases") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunBackgroundPassesGoHeartbeatSample(t *testing.T) {
	root := moduleRoot(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"background", "--package", filepath.Join(root, "samples", "background-heartbeat-go")}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run background contract: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "background contract passed (2 cases") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunProviderRejectsManifestPathEscapes(t *testing.T) {
	root := moduleRoot(t)
	dir := t.TempDir()
	manifest, err := os.ReadFile(filepath.Join(root, "samples", "provider-mock-go", "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest = bytes.ReplaceAll(manifest, []byte("command: bin/provider-mock-go"), []byte("command: ../provider-mock-go"))
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run(context.Background(), []string{"provider", "--package", dir}, &stdout, &stderr)
	if err == nil {
		t.Fatal("manifest path escape was accepted")
	}
	if !strings.Contains(err.Error(), "must not escape") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunProviderRejectsMissingGatewayCapability(t *testing.T) {
	root := moduleRoot(t)
	dir := t.TempDir()
	copySampleManifest(t, filepath.Join(root, "samples", "provider-mock-go"), dir, "    - chat_stream\n", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"provider", "--package", dir}, &stdout, &stderr)
	if err == nil {
		t.Fatal("missing gateway capability was accepted")
	}
	if !strings.Contains(err.Error(), `gateway capabilities missing "chat_stream"`) {
		t.Fatalf("error = %v", err)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}

func copySampleManifest(t *testing.T, sourceDir string, targetDir string, old string, replacement string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(sourceDir, "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest = bytes.ReplaceAll(manifest, []byte(old), []byte(replacement))
	if err := os.WriteFile(filepath.Join(targetDir, "plugin.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
}
