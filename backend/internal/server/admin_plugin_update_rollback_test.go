package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminRejectedPluginUpdatePreservesRollback(t *testing.T) {
	for _, layout := range []string{"canonical", "legacy"} {
		for _, rejection := range []string{"checksum", "dependency"} {
			t.Run(layout+"/"+rejection, func(t *testing.T) {
				pluginDir := t.TempDir()
				id := "example.update-rollback"
				manifest := adminPluginManifest(id, "Rollback fixture", "2.0.0")
				archive := adminPluginZip(t, map[string]string{"plugin.yaml": manifest})
				upstream := newPluginDownloadTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(archive)
				}))
				defer upstream.Close()
				dirName := id
				if layout == "legacy" {
					dirName = "manually-installed"
				}
				writeServerPluginManifest(t, filepath.Join(pluginDir, dirName), adminPluginManifest(id, "Rollback fixture", "1.0.0"))
				server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
				server.pluginInstallClient = upstream.Client()
				endpoint := "/api/admin/plugins/" + id + "/update"
				first := doJSON(t, server.Handler(), http.MethodPost, endpoint, map[string]any{
					"download_url": upstream.URL + "/plugin.zip", "checksum_sha256": adminSHA256Hex(archive),
				}, "dev_admin_token")
				if first.Code != http.StatusOK {
					t.Fatalf("first update = %d: %s", first.Code, first.Body)
				}
				manifest = adminPluginManifest(id, "Rollback fixture", "3.0.0")
				if rejection == "dependency" {
					manifest += "\ndependencies:\n  - id: example.unavailable\n    version: '>=1.0.0'\n"
				}
				archive = adminPluginZip(t, map[string]string{"plugin.yaml": manifest})
				checksum := adminSHA256Hex(archive)
				if rejection == "checksum" {
					checksum = strings.Repeat("0", 64)
				}
				rejected := doJSON(t, server.Handler(), http.MethodPost, endpoint, map[string]any{
					"download_url": upstream.URL + "/plugin.zip", "checksum_sha256": checksum,
				}, "dev_admin_token")
				if rejection == "checksum" {
					assertResponseBodyJSONError(t, rejected, http.StatusBadRequest, "plugin_checksum_mismatch")
				} else {
					assertResponseBodyJSONError(t, rejected, http.StatusConflict, "plugin_dependency_unsatisfied")
				}
				runtime := pluginmeta.NewRuntime(pluginDir)
				current, found, err := runtime.DescribeInstalledPackage(id)
				if err != nil || !found || current.Manifest.Version != "2.0.0" || current.State.RollbackVersion != "1.0.0" {
					t.Fatalf("current package after rejection: found=%t package=%+v error=%v", found, current, err)
				}
				previous, found, err := runtime.DescribeRollbackPackage(id)
				if err != nil || !found || previous.Manifest.Version != "1.0.0" {
					t.Fatalf("rollback after rejection: found=%t package=%+v error=%v", found, previous, err)
				}
				rolledBack := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/"+id+"/rollback", nil, "dev_admin_token")
				if rolledBack.Code != http.StatusOK {
					t.Fatalf("rollback = %d: %s", rolledBack.Code, rolledBack.Body)
				}
				restored, found, err := runtime.DescribeInstalledPackage(id)
				if err != nil || !found || restored.Manifest.Version != "1.0.0" {
					t.Fatalf("restored package: found=%t version=%q error=%v", found, restored.Manifest.Version, err)
				}
			})
		}
	}
}
