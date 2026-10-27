package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type claudeSubscriptionPending struct {
	internalID string
	externalID string
	tool       claudeSubscriptionTool
	arguments  map[string]any
	ready      chan struct{}
	registered bool
	claimed    bool
	result     chan string
}

type claudeSubscriptionRun struct {
	adapter                                                    *ClaudeSubscriptionAdapter
	resourceID, scope, originalSession, root, configDir, token string
	caller, model, accessToken                                 string
	providerID                                                 string
	continuationHash                                           [32]byte
	tools                                                      []claudeSubscriptionTool
	history                                                    []ChatMessage
	ctx                                                        context.Context
	cancel                                                     context.CancelFunc
	command                                                    *exec.Cmd
	input                                                      io.WriteCloser
	server                                                     *http.Server
	exited                                                     chan struct{}
	events                                                     chan claudeSubscriptionEvent
	mu                                                         sync.Mutex
	persistMu                                                  sync.Mutex
	cleanupDone                                                chan struct{}
	waiting                                                    map[string]*claudeSubscriptionPending
	internal                                                   map[string]*claudeSubscriptionPending
	busy, closed                                               bool
	timer                                                      *time.Timer
}

func (a *ClaudeSubscriptionAdapter) start(ctx context.Context, provider Provider, model string, credentials ProviderResourceCredentials, scope string, continuationHash [32]byte, tools []claudeSubscriptionTool, request ChatCompletionRequest) (*claudeSubscriptionRun, error) {
	if strings.TrimSpace(credentials.AccessToken) == "" && strings.TrimSpace(credentials.SessionJSON) == "" {
		return nil, NewHTTPError(400, "claude_subscription_credentials_required", "Import a Claude Code subscription token or account session")
	}
	if credentials.SessionJSON != "" {
		if err := validateClaudeSubscriptionSession(credentials.SessionJSON); err != nil {
			return nil, err
		}
	}
	instructions, prompt, err := claudeSubscriptionPrompt(request.Messages)
	if err != nil {
		return nil, err
	}
	executable := a.Executable
	if executable == "" {
		executable, err = exec.LookPath("claude")
		if err != nil {
			return nil, NewHTTPError(503, "claude_cli_unavailable", "Install the genuine Claude Code CLI on the TokenHub host")
		}
	}
	resourceID := provider.Options["resource_id"]
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	caller, _ := ctx.Value(subscriptionCallerKey{}).(string)
	run := &claudeSubscriptionRun{caller: caller, model: model, accessToken: credentials.AccessToken, providerID: provider.ID, adapter: a, resourceID: resourceID, scope: scope, originalSession: credentials.SessionJSON, continuationHash: continuationHash, tools: tools, history: append([]ChatMessage(nil), request.Messages...), ctx: runCtx, cancel: cancel, exited: make(chan struct{}), cleanupDone: make(chan struct{}), events: make(chan claudeSubscriptionEvent, 128), waiting: make(map[string]*claudeSubscriptionPending), internal: make(map[string]*claudeSubscriptionPending), busy: true}
	a.mu.Lock()
	if a.runs == nil {
		a.runs = make(map[string]*claudeSubscriptionRun)
		a.calls = make(map[string]*claudeSubscriptionRun)
	}
	if current := a.runs[resourceID]; current != nil {
		a.mu.Unlock()
		cancel()
		current.mu.Lock()
		closed := current.closed
		current.mu.Unlock()
		if closed {
			select {
			case <-current.cleanupDone:
				return a.start(ctx, provider, model, credentials, scope, continuationHash, tools, request)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, NewHTTPError(429, "claude_subscription_busy", "Claude subscription account is already running a request or waiting for tool results")
	}
	if len(a.runs) >= 16 {
		a.mu.Unlock()
		cancel()
		return nil, NewHTTPError(429, "claude_subscription_busy", "Claude subscription account is already running a request or waiting for tool results")
	}
	a.runs[resourceID] = run
	a.mu.Unlock()
	if err := run.launchWithLease(ctx, provider, executable, model, instructions, prompt, credentials, request); err != nil {
		run.close()
		return nil, err
	}
	go func() { <-run.ctx.Done(); run.close() }()
	return run, nil
}

func (r *claudeSubscriptionRun) launch(executable string, initialArgs []string, model, instructions string, prompt []map[string]any, credentials ProviderResourceCredentials, request ChatCompletionRequest) error {
	root, err := os.MkdirTemp("", "tokenhub-claude-")
	if err != nil {
		return err
	}
	r.root = root
	r.configDir = filepath.Join(root, "config")
	for _, dir := range []string{r.configDir, filepath.Join(root, "work")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
	}
	token, err := randomHex(32)
	if err != nil {
		return err
	}
	r.token = token
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	r.server = &http.Server{Handler: http.HandlerFunc(r.handleMCP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = r.server.Serve(listener) }()
	config := map[string]any{"mcpServers": map[string]any{"tokenhub": map[string]any{"type": "http", "url": "http://" + listener.Addr().String() + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + token}}}}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	mcpPath := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(mcpPath, encoded, 0600); err != nil {
		return err
	}
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"disableAllHooks":true}`), 0600); err != nil {
		return err
	}
	if credentials.SessionJSON != "" {
		if err := os.WriteFile(filepath.Join(r.configDir, ".credentials.json"), []byte(credentials.SessionJSON), 0600); err != nil {
			return err
		}
	}
	args := append([]string(nil), initialArgs...)
	args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--verbose", "--model", model, "--tools", "", "--strict-mcp-config", "--mcp-config", mcpPath, "--setting-sources", "", "--settings", settingsPath, "--dangerously-skip-permissions", "--no-session-persistence", "--disable-slash-commands", "--no-chrome")
	if instructions != "" {
		systemPath := filepath.Join(root, "system.txt")
		if err := os.WriteFile(systemPath, []byte(instructions), 0600); err != nil {
			return err
		}
		args = append(args, "--append-system-prompt-file", systemPath)
	}
	if request.ReasoningEffort != nil && strings.TrimSpace(*request.ReasoningEffort) != "" {
		args = append(args, "--effort", strings.TrimSpace(*request.ReasoningEffort))
	}
	command := exec.CommandContext(r.ctx, executable, args...)
	command.Dir = filepath.Join(root, "work")
	command.Env = claudeSubscriptionEnv(root, r.configDir, credentials)
	if request.requestedMaxTokens() > 0 {
		command.Env = append(command.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS="+strconv.FormatInt(request.requestedMaxTokens(), 10))
	}
	configureClaudeSubscriptionProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	r.input = input
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	command.Stderr = io.Discard
	r.command = command
	if err := command.Start(); err != nil {
		return NewHTTPError(503, "claude_cli_start_failed", "Claude Code could not be started")
	}
	go func() { r.readOutput(output); _ = command.Wait(); close(r.exited) }()
	payload := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}
	if err := json.NewEncoder(input).Encode(payload); err != nil {
		return NewHTTPError(502, "claude_cli_input_failed", "Claude Code did not accept the request")
	}
	return nil
}

func claudeSubscriptionEnv(root, config string, credentials ProviderResourceCredentials) []string {
	env := []string{"HOME=" + root, "CLAUDE_CONFIG_DIR=" + config, "CLAUDE_SECURESTORAGE_CONFIG_DIR=" + config, "XDG_CONFIG_HOME=" + config, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"}
	for _, key := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SYSTEMROOT", "WINDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	if credentials.SessionJSON == "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+credentials.AccessToken)
	}
	return env
}

func validateClaudeSubscriptionSession(raw string) error {
	var payload struct {
		OAuth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if len(raw) > 512<<10 || json.Unmarshal([]byte(raw), &payload) != nil || strings.TrimSpace(payload.OAuth.AccessToken) == "" {
		return NewHTTPError(400, "claude_subscription_session_invalid", "Claude session JSON must contain claudeAiOauth.accessToken")
	}
	return nil
}

func (r *claudeSubscriptionRun) persist(ctx context.Context) error {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	if r.originalSession == "" {
		return nil
	}
	raw, err := readClaudeSubscriptionSession(r.configDir)
	if err != nil {
		return NewHTTPError(502, "claude_subscription_session_unavailable", "Claude Code did not retain account credentials")
	}
	if err := validateClaudeSubscriptionSession(string(raw)); err != nil {
		return NewHTTPError(401, "provider_resource_reauthorization_required", "Claude Code account needs authorization again")
	}
	if strings.TrimSpace(string(raw)) == strings.TrimSpace(r.originalSession) {
		return nil
	}
	if r.adapter.SaveSession == nil {
		return NewHTTPError(503, "claude_subscription_session_store_unavailable", "Updated Claude Code credentials could not be saved")
	}
	if err := r.adapter.SaveSession(ctx, r.resourceID, providerClaudeSubscription, r.originalSession, string(raw)); err != nil {
		return err
	}
	r.mu.Lock()
	r.originalSession = string(raw)
	binding, _ := json.Marshal([]string{r.resourceID, r.caller, r.accessToken, r.originalSession, r.model})
	sum := sha256.Sum256(binding)
	r.scope = hex.EncodeToString(sum[:])
	r.mu.Unlock()
	return nil
}

func (r *claudeSubscriptionRun) arm(timeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		if r.timer != nil {
			r.timer.Stop()
		}
		r.timer = time.AfterFunc(timeout, r.close)
	}
}
func (r *claudeSubscriptionRun) close() {
	r.cancel()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
	r.cancel()
	r.adapter.mu.Lock()
	for id, run := range r.adapter.calls {
		if run == r {
			delete(r.adapter.calls, id)
		}
	}
	r.adapter.mu.Unlock()
	if r.server != nil {
		_ = r.server.Close()
	}
	if r.input != nil {
		_ = r.input.Close()
	}
	if r.command != nil && r.command.Process != nil {
		killClaudeSubscriptionProcess(r.command)
	}
	go r.cleanup()
}
