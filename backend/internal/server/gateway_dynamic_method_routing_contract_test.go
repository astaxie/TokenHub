package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type dynamicGatewayMethodRoute struct {
	method string
	wrong  string
	path   string
}

var dynamicGatewayMethodRoutes = []dynamicGatewayMethodRoute{
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1/models/gpt-4.1-mini"},
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1/responses/resp_test"},
	{method: http.MethodPost, wrong: http.MethodGet, path: "/v1/responses/resp_test/cancel"},
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1/image-jobs/imgjob_test"},
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1/image-assets/imgasset_test/content"},
}

func TestDynamicGatewayMethodRoutesRejectWrongMethods(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	requestLogsBefore := len(store.ListRequestLogs())
	auditEventsBefore := len(store.ListAuditEvents())
	imageJobsBefore := len(store.ListImageJobs(1000))

	for _, route := range dynamicGatewayMethodRoutes {
		t.Run(route.wrong+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrong, route.path, "")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.method)
		})
	}

	if got := len(store.ListRequestLogs()); got != requestLogsBefore {
		t.Fatalf("wrong methods created request logs: got %d, want %d", got, requestLogsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditEventsBefore {
		t.Fatalf("wrong methods created audit events: got %d, want %d", got, auditEventsBefore)
	}
	if got := len(store.ListImageJobs(1000)); got != imageJobsBefore {
		t.Fatalf("wrong methods created image jobs: got %d, want %d", got, imageJobsBefore)
	}
}

func TestDynamicGatewayMethodRoutesRejectMethodMatrix(t *testing.T) {
	app := newTestServer()
	for _, route := range dynamicGatewayMethodRoutes {
		for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if method == route.method || method == route.wrong || (method == http.MethodHead && route.method == http.MethodGet) {
				continue
			}
			t.Run(method+" "+route.path, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "")
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.method)
			})
		}
	}
}

func TestDynamicGatewayMethodRoutesPreserveTrailingSlash(t *testing.T) {
	app := newTestServer()
	for _, route := range dynamicGatewayMethodRoutes {
		path := route.path + "/"
		t.Run(route.wrong+" "+path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrong, path, "")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.method)

			response = methodRoutingRequest(app, route.method, path, "")
			if route.path == "/v1/image-assets/imgasset_test/content" {
				assertJSONError(t, response, http.StatusNotFound, "image_asset_not_found")
			} else {
				assertJSONError(t, response, http.StatusUnauthorized, "invalid_api_key")
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestDynamicGatewayGETRoutesRejectRealHEAD(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	t.Cleanup(server.Close)

	for _, route := range dynamicGatewayMethodRoutes {
		if route.method != http.MethodGet {
			continue
		}
		for _, path := range []string{route.path, route.path + "/"} {
			t.Run(path, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()

				assertDynamicRealHEAD(t, response, http.StatusMethodNotAllowed, http.MethodGet)
			})
		}
	}
}

func TestDynamicResponseInvalidPathPreservesRealHEADNotFound(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	t.Cleanup(server.Close)

	for _, path := range []string{
		"/v1/responses/resp_test%2Fretry",
		"/v1/responses/resp_test%2Fcancel%2Fextra",
	} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			assertDynamicRealHEAD(t, response, http.StatusNotFound, "")
		})
	}
}

func TestDynamicResponsePOSTRoutesRejectRealHEAD(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	t.Cleanup(server.Close)

	for _, path := range []string{"/v1/responses/resp_test/cancel", "/v1/responses/resp_test/cancel/", "/v1/responses/resp_test%2Fcancel", "/v1/responses/compact"} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			assertDynamicRealHEAD(t, response, http.StatusMethodNotAllowed, http.MethodPost)
		})
	}
}

func TestDynamicGatewayMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range dynamicGatewayMethodRoutes {
		t.Run(route.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.method)
			response := httptest.NewRecorder()

			app.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestDynamicGatewayMethodRoutesReachHandlers(t *testing.T) {
	app := newTestServer()
	for _, route := range dynamicGatewayMethodRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.method, route.path, "")
			if route.path == "/v1/image-assets/imgasset_test/content" {
				assertJSONError(t, response, http.StatusNotFound, "image_asset_not_found")
			} else {
				assertJSONError(t, response, http.StatusUnauthorized, "invalid_api_key")
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestDynamicGatewayRoutesPreserveMalformedPathErrors(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name     string
		path     string
		wantCode string
		token    string
	}{
		{name: "empty model", path: "/v1/models/", wantCode: "model_not_found", token: "thk_demo_local"},
		{name: "empty response", path: "/v1/responses/", wantCode: "response_not_found"},
		{name: "unknown response action", path: "/v1/responses/resp_test/retry", wantCode: "response_not_found"},
		{name: "extra response segment", path: "/v1/responses/resp_test/cancel/extra", wantCode: "response_not_found"},
		{name: "empty image job", path: "/v1/image-jobs/", wantCode: "image_job_not_found", token: "thk_demo_local"},
		{name: "extra image job segment", path: "/v1/image-jobs/imgjob_test/extra", wantCode: "image_job_not_found", token: "thk_demo_local"},
		{name: "empty image asset", path: "/v1/image-assets/", wantCode: "image_asset_not_found"},
		{name: "unknown image asset action", path: "/v1/image-assets/imgasset_test/metadata", wantCode: "image_asset_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodGet, test.path, test.token)
			assertJSONError(t, response, http.StatusNotFound, test.wantCode)
			assertAllowHeader(t, response, "")
		})
	}
}

func TestDynamicGatewayRoutesPreservePathAndMethodValidationOrder(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "response unknown action", method: http.MethodPost, path: "/v1/responses/resp_test/retry", wantStatus: http.StatusNotFound, wantCode: "response_not_found"},
		{name: "response extra segment", method: http.MethodDelete, path: "/v1/responses/resp_test/cancel/extra", wantStatus: http.StatusNotFound, wantCode: "response_not_found"},
		{name: "model empty", method: http.MethodPost, path: "/v1/models/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "image job empty", method: http.MethodPost, path: "/v1/image-jobs/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "image job extra segment", method: http.MethodDelete, path: "/v1/image-jobs/imgjob_test/extra", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "image asset unknown action", method: http.MethodPost, path: "/v1/image-assets/imgasset_test/metadata", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestDynamicGatewayRoutesPreserveEscapedPathSemantics(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name       string
		method     string
		path       string
		token      string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "encoded response cancel GET", method: http.MethodGet, path: "/v1/responses/resp_test%2Fcancel", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "encoded response cancel POST", method: http.MethodPost, path: "/v1/responses/resp_test%2Fcancel", wantStatus: http.StatusUnauthorized, wantCode: "invalid_api_key"},
		{name: "encoded response unknown action", method: http.MethodGet, path: "/v1/responses/resp_test%2Fretry", wantStatus: http.StatusNotFound, wantCode: "response_not_found"},
		{name: "encoded model wrong method", method: http.MethodPost, path: "/v1/models/provider%2Fmodel", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
		{name: "encoded image job separator", method: http.MethodGet, path: "/v1/image-jobs/imgjob_test%2Fextra", token: "thk_demo_local", wantStatus: http.StatusNotFound, wantCode: "image_job_not_found"},
		{name: "encoded image asset separator", method: http.MethodGet, path: "/v1/image-assets/imgasset_test%2Fextra/content", wantStatus: http.StatusNotFound, wantCode: "image_asset_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, test.token)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestDynamicModelRouteSupportsMultiplePathSegments(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_dynamic_model", Name: "Dynamic Model Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_dynamic_model", Name: "Dynamic Model Key", Allowed: []string{"provider/model"}, Status: StatusActive,
	}, "thk_dynamic_model")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{ID: "provider/model", Name: "provider/model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName: "provider/model", ProviderID: "prv_dynamic_model",
		ProviderModel: "provider/model", Status: StatusActive,
	})
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()

	response := methodRoutingRequest(app, http.MethodGet, "/v1/models/provider/model", secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"provider/model"`) {
		t.Fatalf("multi-segment model query: status=%d body=%s", response.Code, response.Body.String())
	}
	assertAllowHeader(t, response, "")
}

func TestDynamicResponseWrongMethodDoesNotCancelJob(t *testing.T) {
	config := responseJobTestConfig()
	store, secret := newBackgroundResponseTestStore(t, config)
	server := NewWithConfig(store, config)
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	key := store.ListAPIKeys()[0]
	project, ok := store.GetProject(key.ProjectID)
	if !ok {
		t.Fatal("response project not found")
	}
	job := createDynamicRouteResponseJob(t, store, project, key)

	response := methodRoutingRequest(server.Handler(), http.MethodGet, "/v1/responses/"+job.ID+"/cancel", secret)
	assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, response, http.MethodPost)

	retained, ok, err := store.GetResponseJob(job.ID)
	if err != nil || !ok || retained.Status != responseJobStatusQueued {
		t.Fatalf("wrong method changed response job: job=%+v ok=%v err=%v", retained, ok, err)
	}
}

func TestDynamicGatewayRoutesPreserveTrailingSlashSuccess(t *testing.T) {
	config := responseJobTestConfig()
	config.ImageStorageDir = t.TempDir()
	store, secret := newBackgroundResponseTestStore(t, config)
	server := NewWithConfig(store, config)
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := server.Handler()
	key := store.ListAPIKeys()[0]
	project, ok := store.GetProject(key.ProjectID)
	if !ok {
		t.Fatal("response project not found")
	}
	response := methodRoutingRequest(app, http.MethodGet, "/v1/models/gpt-background/", secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"gpt-background"`) {
		t.Fatalf("model query with trailing slash: status=%d body=%s", response.Code, response.Body.String())
	}
	assertAllowHeader(t, response, "")

	queryJob := createDynamicRouteResponseJob(t, store, project, key)
	response = methodRoutingRequest(app, http.MethodGet, "/v1/responses/"+queryJob.ID+"/", secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"`+queryJob.ID+`"`) {
		t.Fatalf("response query with trailing slash: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("x-request-id"); got != queryJob.ID {
		t.Fatalf("response query x-request-id = %q, want %q", got, queryJob.ID)
	}
	assertAllowHeader(t, response, "")

	cancelJob := createDynamicRouteResponseJob(t, store, project, key)
	response = methodRoutingRequest(app, http.MethodPost, "/v1/responses/"+cancelJob.ID+"/cancel/", secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("response cancellation with trailing slash: status=%d body=%s", response.Code, response.Body.String())
	}
	cancelled, ok, err := store.GetResponseJob(cancelJob.ID)
	if err != nil || !ok || cancelled.Status != responseJobStatusCancelled {
		t.Fatalf("response job was not cancelled: job=%+v ok=%v err=%v", cancelled, ok, err)
	}
	assertAllowHeader(t, response, "")

	imageJob, err := store.CreateImageJob(ImageJob{
		ProjectID: project.ID, APIKeyID: key.ID, RequestID: "req_dynamic_image",
		Status: imageJobStatusCompleted, Model: "gpt-background", Action: "generate",
	}, "dynamic route image")
	if err != nil {
		t.Fatal(err)
	}
	response = methodRoutingRequest(app, http.MethodGet, "/v1/image-jobs/"+imageJob.ID+"/", secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"`+imageJob.ID+`"`) {
		t.Fatalf("image job query with trailing slash: status=%d body=%s", response.Code, response.Body.String())
	}
	assertAllowHeader(t, response, "")

	assetBytes := []byte("dynamic route image bytes")
	relativePath := filepath.Join("dynamic-route", "output.bin")
	fullPath := filepath.Join(config.ImageStorageDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, assetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	asset, err := store.CreateImageAsset(ImageAsset{
		JobID: imageJob.ID, ProjectID: project.ID, Role: "output", RelativePath: relativePath,
		ContentType: "application/octet-stream", ByteSize: int64(len(assetBytes)),
	})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).Unix()
	signature := server.imageAssetSignature(asset, expires)
	path := "/v1/image-assets/" + asset.ID + "/content/?expires=" + strconv.FormatInt(expires, 10) + "&signature=" + signature
	response = methodRoutingRequest(app, http.MethodGet, path, "")
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), assetBytes) {
		t.Fatalf("image asset download with trailing slash: status=%d body=%q", response.Code, response.Body.Bytes())
	}
	if got := response.Header().Get("content-type"); got != asset.ContentType {
		t.Fatalf("image asset content-type = %q, want %q", got, asset.ContentType)
	}
	if got := response.Header().Get("content-length"); got != strconv.FormatInt(asset.ByteSize, 10) {
		t.Fatalf("image asset content-length = %q, want %d", got, asset.ByteSize)
	}
	if got := response.Header().Get("cache-control"); got != "private, max-age=86400" {
		t.Fatalf("image asset cache-control = %q", got)
	}
	if got := response.Header().Get("content-disposition"); got != "inline" {
		t.Fatalf("image asset content-disposition = %q, want inline", got)
	}
	assertAllowHeader(t, response, "")
}

func TestDynamicResponseRoutesPreserveCompactPriority(t *testing.T) {
	app := newTestServer()

	response := methodRoutingRequest(app, http.MethodGet, "/v1/responses/compact", "")
	assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, response, http.MethodPost)

	response = methodRoutingRequest(app, http.MethodPost, "/v1/responses/compact", "")
	assertJSONError(t, response, http.StatusUnauthorized, "invalid_api_key")
	assertAllowHeader(t, response, "")

	response = methodRoutingRequest(app, http.MethodPut, "/v1/responses/compact", "")
	assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, response, http.MethodPost)
}

func createDynamicRouteResponseJob(t *testing.T, store *GormStore, project Project, key APIKey) ResponseJob {
	t.Helper()
	requestJSON, err := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"gpt-background","input":"dynamic route"}`)})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateResponseJob(ResponseJob{
		ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project),
		Status:           responseJobStatusQueued, Phase: responseJobPhaseQueued, Model: "gpt-background",
	}, requestJSON)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func assertDynamicRealHEAD(t *testing.T, response *http.Response, wantStatus int, wantAllow string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("HEAD status = %d, want %d", response.StatusCode, wantStatus)
	}
	allowValues, present := response.Header[http.CanonicalHeaderKey("Allow")]
	if wantAllow == "" {
		if present {
			t.Fatalf("HEAD Allow is present with value %q, want absent", allowValues)
		}
	} else if got := response.Header.Get("Allow"); got != wantAllow {
		t.Fatalf("HEAD Allow = %q, want %q", got, wantAllow)
	}
	if contentType := response.Header.Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("HEAD content type = %q, want application/json", contentType)
	}
	if response.Header.Get("x-request-id") == "" {
		t.Fatal("HEAD x-request-id is empty")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD response body = %q, want empty", body)
	}
}
