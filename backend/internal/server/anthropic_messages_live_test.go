package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	liveAnthropicPublicModel = "claude-sonnet-4-6"
	liveAnthropicProjectKey  = "thk_live_claude_code_e2e"
)

// TestClaudeCodeLiveE2E is an opt-in live validation. It keeps the upstream
// credential in process memory, exercises the SDK smoke test, and asks the
// installed Claude Code CLI to inspect this repository through TokenHub.
func TestClaudeCodeLiveE2E(t *testing.T) {
	upstreamAPIKey := strings.TrimSpace(os.Getenv("TOKENHUB_LIVE_ARK_API_KEY"))
	if upstreamAPIKey == "" {
		t.Skip("set TOKENHUB_LIVE_ARK_API_KEY to run the live Claude Code validation")
	}

	upstreamBaseURL := strings.TrimSpace(os.Getenv("TOKENHUB_LIVE_ARK_BASE_URL"))
	if upstreamBaseURL == "" {
		upstreamBaseURL = "https://ark-cn-beijing.bytedance.net/api/v3"
	}
	upstreamModel := strings.TrimSpace(os.Getenv("TOKENHUB_LIVE_ARK_MODEL"))
	if upstreamModel == "" {
		upstreamModel = "ep-20260110190602-xswg5"
	}

	st := NewMemoryStore()
	project := st.CreateProject(Project{Name: "claude-code-live", Status: StatusActive})
	if _, _, err := st.CreateAPIKey(project.ID, APIKey{
		Name:    "claude-code-live",
		Allowed: []string{liveAnthropicPublicModel},
		Status:  StatusActive,
	}, liveAnthropicProjectKey); err != nil {
		t.Fatal(err)
	}
	st.AddModel(Model{
		Name:                liveAnthropicPublicModel,
		Family:              "claude",
		Modality:            "chat",
		ContextWindow:       200000,
		InputModalities:     []string{"text", "image"},
		Capabilities:        []string{"chat", "vision", "tools"},
		InputPriceUSDPer1M:  0,
		OutputPriceUSDPer1M: 0,
		SupportedParameters: []string{"tools", "image_input"},
		Status:              StatusActive,
	})
	provider := st.AddProvider(Provider{
		ID:      "prv_ark_live",
		Name:    "ark-live",
		Type:    ProviderOpenAICompatible,
		BaseURL: upstreamBaseURL,
		Status:  StatusActive,
		Healthy: true,
	})
	st.AddRoute(ModelRoute{
		ID:            "route_ark_live",
		ModelName:     liveAnthropicPublicModel,
		ProviderID:    provider.ID,
		ProviderModel: upstreamModel,
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityOnly,
	})

	app := New(st)
	liveAdapter := liveCredentialAdapter{
		apiKey:   upstreamAPIKey,
		delegate: OpenAICompatibleAdapter{},
	}
	app.adapterRegistry.Register(
		ProviderOpenAICompatible,
		liveAdapter,
		AdapterCapabilityChat,
		AdapterCapabilityChatStream,
		AdapterCapabilityResponses,
		AdapterCapabilityEmbeddings,
		AdapterCapabilityProbe,
	)
	gateway := httptest.NewServer(app.Handler())
	t.Cleanup(gateway.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	t.Run("vision_message", func(t *testing.T) {
		requestBody := map[string]any{
			"model":      liveAnthropicPublicModel,
			"max_tokens": 256,
			"messages": []map[string]any{{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "image",
						"source": map[string]any{
							"type": "url",
							"url":  "https://ark-project.tos-cn-beijing.ivolces.com/images/view.jpeg",
						},
					},
					{"type": "text", "text": "这是哪里？请简短回答。"},
				},
			}},
		}
		response := postLiveAnthropicMessage(t, ctx, gateway.URL, requestBody)
		content, _ := response["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("expected vision response content, got %#v", response)
		}
		t.Logf("vision response: %s", truncateLiveOutput(mustJSON(response), 2000))
	})

	repositoryRoot := liveRepositoryRoot(t)
	commandEnvironment := liveCommandEnvironment(gateway.URL)

	t.Run("sdk_smoke", func(t *testing.T) {
		command := exec.CommandContext(ctx, "npm", "run", "test:anthropic-messages")
		command.Dir = filepath.Join(repositoryRoot, "sdk")
		command.Env = commandEnvironment
		output, err := command.CombinedOutput()
		t.Logf("SDK smoke output:\n%s", truncateLiveOutput(string(output), 6000))
		if err != nil {
			t.Fatalf("SDK Anthropic Messages smoke test failed: %v", err)
		}
	})

	t.Run("claude_code_repository_understanding", func(t *testing.T) {
		claudePath, err := exec.LookPath("claude")
		if err != nil {
			t.Fatalf("Claude Code executable is required for live validation: %v", err)
		}
		prompt := strings.Join([]string{
			"Understand this repository thoroughly.",
			"Collect focused evidence by issuing exactly five parallel Bash tool calls, one for each command:",
			"`sed -n '1,220p' README.md`;",
			"`sed -n '1,220p' docs/development/workflows/feature-dev.md`;",
			"`sed -n '1,120p' frontend/package.json`;",
			"`rg -n 'HandleFunc\\\\(\\\"/v1|handleChatCompletions|authenticate\\\\(' backend/internal/server/http.go`;",
			"and `rg -n 'type ProviderAdapter|OpenAICompatibleAdapter|AnthropicAdapter' backend/internal/server/providers.go`.",
			"After that single parallel tool round, do not call another tool; answer from the collected evidence.",
			"Explain the architecture, request-routing flow, security boundaries, and validation workflow.",
			"Identify two concrete engineering risks.",
			"Cite the relevant repository file path for every major claim.",
			"Do not modify any file.",
		}, " ")
		command := exec.CommandContext(
			ctx,
			claudePath,
			"-p",
			"--max-turns", "8",
			"--no-session-persistence",
			"--output-format", "json",
			"--tools=Bash",
			prompt,
		)
		command.Dir = repositoryRoot
		command.Env = commandEnvironment
		output, err := command.CombinedOutput()
		t.Logf("Claude Code output:\n%s", truncateLiveOutput(string(output), 12000))
		if err != nil {
			t.Fatalf("Claude Code repository query failed: %v", err)
		}

		var result struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			Result   string `json:"result"`
			NumTurns int    `json:"num_turns"`
			IsError  bool   `json:"is_error"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatalf("decode Claude Code JSON output: %v", err)
		}
		if result.IsError || result.Type != "result" {
			t.Fatalf("Claude Code returned an unsuccessful result: type=%q subtype=%q", result.Type, result.Subtype)
		}
		if result.NumTurns < 2 {
			t.Fatalf("expected a multi-turn tool-using query, got %d turn(s)", result.NumTurns)
		}
		for _, expected := range []string{"backend/", "frontend/", "README.md"} {
			if !strings.Contains(result.Result, expected) {
				t.Fatalf("Claude Code result did not cite %q", expected)
			}
		}
	})
}

type liveCredentialAdapter struct {
	apiKey   string
	delegate OpenAICompatibleAdapter
}

func (a liveCredentialAdapter) Chat(
	ctx context.Context,
	provider Provider,
	providerModel string,
	request ChatCompletionRequest,
) (any, Usage, error) {
	provider.APIKey = a.apiKey
	return a.delegate.Chat(ctx, provider, providerModel, request)
}

func (a liveCredentialAdapter) ChatStream(
	ctx context.Context,
	provider Provider,
	providerModel string,
	request ChatCompletionRequest,
	writer io.Writer,
) (Usage, error) {
	provider.APIKey = a.apiKey
	return a.delegate.ChatStream(ctx, provider, providerModel, request, writer)
}

func (a liveCredentialAdapter) Responses(
	ctx context.Context,
	provider Provider,
	providerModel string,
	request ResponsesRequest,
) (any, Usage, error) {
	provider.APIKey = a.apiKey
	return a.delegate.Responses(ctx, provider, providerModel, request)
}

func (a liveCredentialAdapter) Embeddings(
	ctx context.Context,
	provider Provider,
	providerModel string,
	request EmbeddingsRequest,
) (any, Usage, error) {
	provider.APIKey = a.apiKey
	return a.delegate.Embeddings(ctx, provider, providerModel, request)
}

func postLiveAnthropicMessage(
	t *testing.T,
	ctx context.Context,
	baseURL string,
	requestBody map[string]any,
) map[string]any {
	t.Helper()
	body, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+"/v1/messages",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+liveAnthropicProjectKey)
	request.Header.Set("anthropic-version", "2023-06-01")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Anthropic Messages returned %d: %s", response.StatusCode, responseBody)
	}
	var decoded map[string]any
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		t.Fatalf("decode Anthropic Messages response: %v; body=%s", err, responseBody)
	}
	return decoded
}

func liveRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve live test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func liveCommandEnvironment(gatewayURL string) []string {
	allowedInherited := map[string]struct{}{
		"COLORTERM":       {},
		"HOME":            {},
		"LANG":            {},
		"LC_ALL":          {},
		"LOGNAME":         {},
		"NO_COLOR":        {},
		"PATH":            {},
		"SHELL":           {},
		"TERM":            {},
		"TMPDIR":          {},
		"USER":            {},
		"XDG_CACHE_HOME":  {},
		"XDG_CONFIG_HOME": {},
	}
	overrides := map[string]string{
		"ANTHROPIC_API_KEY":                        "",
		"ANTHROPIC_AUTH_TOKEN":                     liveAnthropicProjectKey,
		"ANTHROPIC_BASE_URL":                       gatewayURL,
		"ANTHROPIC_MODEL":                          liveAnthropicPublicModel,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"CLAUDE_CODE_USE_BEDROCK":                  "",
		"CLAUDE_CODE_USE_VERTEX":                   "",
		"CLAUDE_CODE_USE_FOUNDRY":                  "",
		"DISABLE_AUTOUPDATER":                      "1",
		"TOKENHUB_API_KEY":                         liveAnthropicProjectKey,
		"TOKENHUB_BASE_URL":                        gatewayURL + "/v1",
		"TOKENHUB_MODEL":                           liveAnthropicPublicModel,
	}
	environment := make([]string, 0, len(allowedInherited)+len(overrides))
	for _, item := range os.Environ() {
		name, _, found := strings.Cut(item, "=")
		if _, allowed := allowedInherited[name]; !found || !allowed {
			continue
		}
		environment = append(environment, item)
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func TestLiveCommandEnvironmentExcludesCredentialsAndDefaultAliases(t *testing.T) {
	for name, value := range map[string]string{
		"TOKENHUB_LIVE_ARK_API_KEY":          "upstream-secret-must-not-leak",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":       "unexpected-opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":     "unexpected-sonnet",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":      "unexpected-haiku",
		"TOKENHUB_UNRELATED_TEST_CREDENTIAL": "unrelated-secret-must-not-leak",
	} {
		t.Setenv(name, value)
	}

	environment := map[string]string{}
	for _, item := range liveCommandEnvironment("http://127.0.0.1:9876") {
		name, value, found := strings.Cut(item, "=")
		if found {
			environment[name] = value
		}
	}
	for _, forbidden := range []string{
		"TOKENHUB_LIVE_ARK_API_KEY",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"TOKENHUB_UNRELATED_TEST_CREDENTIAL",
	} {
		if _, exists := environment[forbidden]; exists {
			t.Fatalf("sensitive or model-alias environment variable %q leaked to child process", forbidden)
		}
	}
	if environment["ANTHROPIC_MODEL"] != liveAnthropicPublicModel {
		t.Fatalf("expected documented ANTHROPIC_MODEL, got %q", environment["ANTHROPIC_MODEL"])
	}
	if environment["ANTHROPIC_AUTH_TOKEN"] != liveAnthropicProjectKey {
		t.Fatalf("expected synthetic TokenHub credential, got %q", environment["ANTHROPIC_AUTH_TOKEN"])
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(encoded)
}

func truncateLiveOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n... (truncated)"
}
