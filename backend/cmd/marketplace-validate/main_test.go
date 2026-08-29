package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunValidatesMarketplaceIndexes(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "official-valid", "index.json"),
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "third-party-valid", "index.json"),
	}, &stdout)
	if err != nil {
		t.Fatalf("validate marketplace indexes: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "official-valid") || !strings.Contains(output, "third-party-valid") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestRunRejectsInvalidMarketplaceIndex(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "incompatible-api", "index.json"),
	}, &stdout)
	if err == nil {
		t.Fatal("invalid marketplace index validated successfully")
	}
	if !strings.Contains(err.Error(), "unsupported tokenhub.plugin_api") {
		t.Fatalf("error = %q", err.Error())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output for failed validation", stdout.String())
	}
}

func TestRunRequiresAtLeastOnePath(t *testing.T) {
	var stdout bytes.Buffer
	err := run(nil, &stdout)
	if err == nil {
		t.Fatal("run succeeded without input paths")
	}
	if !strings.Contains(err.Error(), "usage: marketplace-validate") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunReportsReadFailures(t *testing.T) {
	var stdout bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.json")
	err := run([]string{missing}, &stdout)
	if err == nil {
		t.Fatal("missing marketplace index validated successfully")
	}
	if !strings.Contains(err.Error(), missing) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %q", err.Error())
	}
}
