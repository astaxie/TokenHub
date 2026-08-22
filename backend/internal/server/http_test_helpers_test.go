package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer() http.Handler {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		panic(err)
	}
	return New(store).Handler()
}

func configureTestSMTPChannel(t *testing.T, store *GormStore) <-chan string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 10)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSMTPConnection(conn, messages)
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("notification-channels", AdminResource{
		Name:   "Test SMTP",
		Status: StatusActive,
		Fields: map[string]any{
			"type":      "email",
			"smtp_host": host,
			"smtp_port": port,
			"smtp_from": "tokenhub@example.com",
		},
	})
	return messages
}

func serveTestSMTPConnection(conn net.Conn, messages chan<- string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(response string) bool {
		if _, err := writer.WriteString(response + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !write("220 localhost ESMTP") {
		return
	}
	var message strings.Builder
	readingData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		if readingData {
			if command == "." {
				messages <- message.String()
				message.Reset()
				readingData = false
				if !write("250 queued") {
					return
				}
				continue
			}
			message.WriteString(strings.TrimPrefix(command, "."))
			message.WriteByte('\n')
			continue
		}
		switch {
		case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
			if !write("250 localhost") {
				return
			}
		case command == "DATA":
			readingData = true
			if !write("354 end with <CRLF>.<CRLF>") {
				return
			}
		case command == "QUIT":
			_ = write("221 bye")
			return
		default:
			if !write("250 ok") {
				return
			}
		}
	}
}

func assertPasswordResetEmail(t *testing.T, messages <-chan string, recipient string) {
	t.Helper()
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: "+recipient) || !strings.Contains(message, "#reset_token=") || strings.Contains(message, "?reset_token=") {
			t.Fatalf("unexpected password reset email: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for password reset email to %s", recipient)
	}
}

type responseBody struct {
	Code int
	Body string
}

func doJSON(t *testing.T, handler http.Handler, method string, path string, payload any, token string) responseBody {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("content-type", "application/json")
	}
	if token == "" && strings.HasPrefix(path, "/api/admin") {
		token = "dev_admin_token"
	}
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return responseBody{Code: rr.Code, Body: rr.Body.String()}
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func stringifyValueForTest(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value)
	return strings.Trim(string(data), `"`)
}

func newResourceRoutedStore(t *testing.T, providerType string) (*GormStore, string, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Resource Ops App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "resource-ops-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_resource_ops_"+providerType)
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_" + providerType, Name: "Resource Ops Provider", Type: providerType, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_" + providerType,
		ProviderID:     provider.ID,
		Name:           "Resource Ops Instance",
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		Priority:       1,
		Weight:         100,
		RateLimitRPM:   0,
		TokenLimitTPM:  100000,
		MaxConcurrency: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "resource-ops-chat",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           "priority_only",
	})
	return store, secret, resource.ID
}

func findResource(t *testing.T, store *GormStore, id string) ProviderResource {
	t.Helper()
	for _, resource := range store.ListProviderResources() {
		if resource.ID == id {
			return resource
		}
	}
	t.Fatalf("resource %s not found", id)
	return ProviderResource{}
}

type captureAdapter struct {
	seenKey     string
	seenOptions map[string]string
}

func (a *captureAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Chat(ctx, provider, providerModel, req)
}

func (a *captureAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.ChatStream(ctx, provider, providerModel, req, w)
}

func (a *captureAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Responses(ctx, provider, providerModel, req)
}

func (a *captureAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Embeddings(ctx, provider, providerModel, req)
}

func testJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

type failingAdapter struct{}

func (a failingAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	return Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}
