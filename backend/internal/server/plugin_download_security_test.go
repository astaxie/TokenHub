package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginAssetsRejectUnsafeRedirects(t *testing.T) {
	for _, target := range []string{"http://93.184.216.34/archive.zip", "http://127.0.0.1/private", "https://127.0.0.1/private", "https://10.1.2.3/private", "https://169.254.169.254/latest/meta-data", "https://[::1]/private"} {
		t.Run(target, func(t *testing.T) {
			upstream := newPluginDownloadTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) }))
			defer upstream.Close()
			server := &Server{pluginInstallClient: upstream.Client()}
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			if _, err := server.downloadPluginInstallArchive(request, upstream.URL+"/archive.zip"); err == nil {
				t.Fatal("archive followed unsafe redirect")
			}
			if _, err := server.downloadPluginInstallSignature(request, upstream.URL+"/signature"); err == nil {
				t.Fatal("signature followed unsafe redirect")
			}
		})
	}
}

func TestPluginAssetsRejectPrivateInitialURLsAndAllowSafeRedirect(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, target := range []string{"http://127.0.0.1/file", "https://localhost/file", "https://127.0.0.1/file", "https://10.0.0.1/file", "https://169.254.169.254/file"} {
		if _, err := server.downloadPluginInstallArchive(request, target); err == nil {
			t.Fatalf("private URL allowed: %s", target)
		}
	}
	upstream := newPluginDownloadTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/asset", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("package"))
	}))
	defer upstream.Close()
	server.pluginInstallClient = upstream.Client()
	data, err := server.downloadPluginInstallArchive(request, upstream.URL+"/start")
	if err != nil || string(data) != "package" {
		t.Fatalf("safe same-origin redirect: %q %v", data, err)
	}
}
