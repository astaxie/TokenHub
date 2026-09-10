package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeUpdatePreservesPreviousNamedPlugin(t *testing.T) {
	for _, preserveRollback := range []bool{false, true} {
		name := "without_rollback"
		if preserveRollback {
			name = "with_rollback"
		}
		t.Run(name, func(t *testing.T) {
			runtime := NewRuntime(t.TempDir())
			install := func(id, version string, options InstallOptions) Package {
				t.Helper()
				archive := pluginZip(t, map[string]zipFixtureFile{
					"plugin.yaml":  {Body: minimalPluginManifest(id, "Replacement fixture", version), Mode: 0o644},
					"identity.txt": {Body: id + ":" + version, Mode: 0o644},
				})
				pkg, err := runtime.InstallZipArchive(archive, options)
				if err != nil {
					t.Fatal(err)
				}
				return pkg
			}
			install("example.foo", "1.0.0", InstallOptions{})
			neighbor := install("example.foo.previous", "1.0.0", InstallOptions{InitialState: PackageState{Status: StatusDisabled}})
			updated := install("example.foo", "2.0.0", InstallOptions{Replace: true, PreserveRollback: preserveRollback})
			if updated.Manifest.Version != "2.0.0" {
				t.Fatalf("updated version = %q", updated.Manifest.Version)
			}
			pkg, found, err := runtime.DescribeInstalledPackage(neighbor.Manifest.ID)
			if err != nil || !found || pkg.State.Status != StatusDisabled {
				t.Fatalf("unrelated plugin after update: found=%t package=%+v error=%v", found, pkg, err)
			}
			identity, err := os.ReadFile(filepath.Join(pkg.Dir, "identity.txt"))
			if err != nil || string(identity) != "example.foo.previous:1.0.0" {
				t.Fatalf("unrelated package contents = %q, error=%v", identity, err)
			}
			if preserveRollback {
				previous, err := readPackage(rollbackPackageDir(runtime.Dir, "example.foo"))
				if err != nil || previous.Manifest.Version != "1.0.0" {
					t.Fatalf("rollback package = %+v, error=%v", previous, err)
				}
			}
			assertNoReplacementReservations(t, runtime.Dir)
		})
	}
}

func TestReplacePackageDirRestoresPackageAfterActivationFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "example.foo")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "identity.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replacePackageDir(filepath.Join(root, "missing"), target, target, true, ""); err == nil {
		t.Fatal("replacement with a missing source succeeded")
	}
	identity, err := os.ReadFile(filepath.Join(target, "identity.txt"))
	if err != nil || string(identity) != "original" {
		t.Fatalf("restored package contents = %q, error=%v", identity, err)
	}
	assertNoReplacementReservations(t, root)
}

func assertNoReplacementReservations(t *testing.T, root string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".replace-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("replacement reservations = %v, error=%v", paths, err)
	}
}
