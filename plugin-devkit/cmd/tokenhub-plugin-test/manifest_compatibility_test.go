package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedManifestSchemaIsCurrent(t *testing.T) {
	command := exec.Command("go", "run", "../../../backend/cmd/pluginmanifestgen/main.go", "-root", "../../..", "-check")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("canonical schema drift: %v\n%s", err, output)
	}
}

func TestProductionPackageManifestsDecode(t *testing.T) {
	fixtures := "../../../backend/internal/plugin/testdata"
	count := 0
	err := filepath.WalkDir(fixtures, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != "plugin.yaml" {
			return nil
		}
		count++
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			if _, err := readManifest(filepath.Dir(path)); err != nil {
				t.Fatalf("production manifest rejected: %v", err)
			}
		})
		return nil
	})
	if err != nil || count == 0 {
		t.Fatalf("read production fixtures: count=%d err=%v", count, err)
	}
}
