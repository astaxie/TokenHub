package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

const expectedMaxImageEditInputCount = 16

func TestNativeCodexJSONImageEditUsesSubscriptionRoute(t *testing.T) {
	server, store, secret := newNativeCodexImageEditTestServer(t)
	imageBytes := realPNGFixture(t)
	imageURL := "data:image/png;base64," + encodeBase64(imageBytes)
	images := make([]map[string]any, expectedMaxImageEditInputCount)
	for index := range images {
		images[index] = map[string]any{"image_url": imageURL}
	}

	var routedModel string
	var routedAction string
	var routedInputCount int
	server.imageRunner = func(_ context.Context, route RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
		routedModel = route.Route.ModelName
		routedAction = job.Action
		for _, asset := range store.ListImageAssets(job.ID) {
			if asset.Role == "input" {
				routedInputCount++
			}
		}
		return imageBytes, "", Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}, nil
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/edits", map[string]any{
		"model": openAIImageModelName, "prompt": "Combine all reference images.",
		"images": images, "quality": "low", "size": "1024x1024", "response_format": "url",
	}, secret, map[string]string{"x-codex-image-turn-id": "turn_native_edit"})
	if response.Code != http.StatusOK {
		t.Fatalf("native Codex JSON edit: status=%d body=%s", response.Code, response.Body)
	}
	if routedModel != codexImageModelName || routedAction != "edit" || routedInputCount != expectedMaxImageEditInputCount {
		t.Fatalf("unexpected native Codex edit route: model=%q action=%q inputs=%d", routedModel, routedAction, routedInputCount)
	}
	var success map[string]any
	if err := json.Unmarshal([]byte(response.Body), &success); err != nil {
		t.Fatal(err)
	}
	data, _ := success["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("native Codex edit must return one image: %+v", success)
	}
	first, _ := data[0].(map[string]any)
	if first["b64_json"] == "" || first["url"] != nil {
		t.Fatalf("native Codex edit must return b64_json: %+v", success)
	}
}

func TestNativeCodexJSONImageEditValidation(t *testing.T) {
	server, _, secret := newNativeCodexImageEditTestServer(t)
	imageURL := "data:image/png;base64," + encodeBase64(realPNGFixture(t))
	tooMany := make([]map[string]any, expectedMaxImageEditInputCount+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"image_url": imageURL}
	}
	tests := []struct {
		name      string
		payload   map[string]any
		headers   map[string]string
		status    int
		errorCode string
	}{
		{
			name: "rejects more than sixteen images", payload: map[string]any{
				"model": openAIImageModelName, "prompt": "too many", "images": tooMany,
			},
			headers: map[string]string{"x-codex-image-turn-id": "turn_too_many"},
			status:  http.StatusBadRequest, errorCode: "too_many_images",
		},
		{
			name: "rejects missing images", payload: map[string]any{
				"model": openAIImageModelName, "prompt": "missing",
			},
			headers: map[string]string{"x-codex-image-turn-id": "turn_missing"},
			status:  http.StatusBadRequest, errorCode: "missing_image",
		},
		{
			name: "rejects invalid base64", payload: map[string]any{
				"model": openAIImageModelName, "prompt": "invalid",
				"images": []map[string]any{{"image_url": "data:image/png;base64,not-base64"}},
			},
			headers: map[string]string{"Originator": "codex_cli_rs"},
			status:  http.StatusBadRequest, errorCode: "invalid_input_image",
		},
		{
			name: "keeps ordinary JSON unsupported", payload: map[string]any{
				"model": openAIImageModelName, "prompt": "not Codex",
				"images": []map[string]any{{"image_url": imageURL}},
			},
			status: http.StatusUnsupportedMediaType, errorCode: "invalid_content_type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/edits", test.payload, secret, test.headers)
			assertImageErrorResponse(t, response, test.status, test.errorCode)
		})
	}
}

func TestMultipartImageEditRejectsMoreThanSixteenImages(t *testing.T) {
	server, _, secret := newNativeCodexImageEditTestServer(t)
	imageBytes := realPNGFixture(t)
	server.imageRunner = func(_ context.Context, _ RouteSelection, _ ImageJob) ([]byte, string, Usage, error) {
		return imageBytes, "", Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}, nil
	}
	for _, test := range []struct {
		name      string
		count     int
		status    int
		errorCode string
	}{
		{name: "accepts sixteen images", count: expectedMaxImageEditInputCount, status: http.StatusOK},
		{name: "rejects seventeen images", count: expectedMaxImageEditInputCount + 1, status: http.StatusBadRequest, errorCode: "too_many_images"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := doMultipartImageEditWithCount(t, server.Handler(), secret, imageBytes, test.count)
			if test.errorCode == "" {
				if response.Code != test.status {
					t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body, test.status)
				}
				return
			}
			assertImageErrorResponse(t, response, test.status, test.errorCode)
		})
	}
}

func TestNativeCodexImageEditMapsOversizedJSONBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewBufferString("{}"))
	request.Body = http.MaxBytesReader(nil, request.Body, 1)
	_, _, err := decodeNativeCodexImageEdit(request)
	if err == nil {
		t.Fatal("oversized JSON request must fail")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusRequestEntityTooLarge || httpErr.Code != "image_edit_request_too_large" {
		t.Fatalf("oversized JSON error = status %d code %q", httpErr.Status, httpErr.Code)
	}
}

func newNativeCodexImageEditTestServer(t *testing.T) (*Server, *MemoryStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Native Codex Image Edit Project"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "native-codex-image-edit", Allowed: []string{codexImageModelName}, Status: StatusActive,
	}, "thk_native_codex_image_edit")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_native_codex_image_edit", Name: "Native Codex Image Edit",
		Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_native_codex_image_edit", ProviderID: provider.ID, Name: "Native Codex Image Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Options: map[string]string{codexImageCapabilityOption: codexImageCapabilitySupported},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: codexImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_native_codex_image_edit", ModelName: codexImageModelName, ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: codexImageUpstreamModel, Status: StatusActive,
		Priority: 1, Weight: 100,
	})
	server := NewWithConfig(store, Config{
		AdminToken: "test-admin-token", SecretKey: "native-codex-image-edit-secret", ImageStorageDir: t.TempDir(),
	})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server, store, secret
}

func assertImageErrorResponse(t *testing.T, response responseBody, status int, errorCode string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body, status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	errorPayload, _ := payload["error"].(map[string]any)
	if errorPayload["code"] != errorCode {
		t.Fatalf("error code=%v body=%s, want %q", errorPayload["code"], response.Body, errorCode)
	}
}

func doMultipartImageEditWithCount(t *testing.T, handler http.Handler, token string, imageBytes []byte, count int) responseBody {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", codexImageModelName)
	_ = writer.WriteField("prompt", "Combine all multipart reference images.")
	_ = writer.WriteField("response_format", "b64_json")
	for index := 0; index < count; index++ {
		part, err := writer.CreateFormFile("image[]", "reference.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(imageBytes); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("content-type", writer.FormDataContentType())
	request.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return responseBody{Code: response.Code, Body: response.Body.String()}
}
