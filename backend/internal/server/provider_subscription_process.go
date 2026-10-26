package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tokenhub/backend/internal/subscriptionworker"
)

const subscriptionWorkerMessageLimit = 32 << 20

type subscriptionWorkerEvent struct {
	ID         string                     `json:"id"`
	Event      string                     `json:"event"`
	Result     json.RawMessage            `json:"result"`
	Status     int                        `json:"status"`
	Headers    http.Header                `json:"headers"`
	Data       string                     `json:"data"`
	RequestID  string                     `json:"request_id"`
	UpdateID   string                     `json:"update_id"`
	ResourceID string                     `json:"resource_id"`
	URL        string                     `json:"url"`
	Method     string                     `json:"method"`
	Body       string                     `json:"body"`
	Auth       json.RawMessage            `json:"auth"`
	Error      *subscriptionWorkerFailure `json:"error"`
}

type subscriptionWorkerProcess struct {
	ctx           context.Context
	cancel        context.CancelFunc
	cmd           *exec.Cmd
	input         io.WriteCloser
	writeMu       sync.Mutex
	callGate      chan struct{}
	mu            sync.Mutex
	pending       map[string]chan subscriptionWorkerEvent
	secrets       map[string]*subscriptionWorkerSecrets
	networks      map[string]context.CancelFunc
	client        *http.Client
	directory     string
	done          chan struct{}
	auth          string
	saveAuth      func(context.Context, string, string) error
	operationCtx  context.Context
	closed        bool
	lastUsed      time.Time
	models        []subscriptionWorkerModel
	modelsFetched time.Time
}

func startSubscriptionWorker(client *http.Client, auth string, save func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, NewHTTPError(503, "subscription_runtime_unavailable", "Node.js 22.23.1 or later is required for this subscription")
	}
	versionCtx, versionCancel := context.WithTimeout(context.Background(), 3*time.Second)
	versionCmd := exec.CommandContext(versionCtx, node, "--version")
	versionCmd.Env = []string{"LANG=C.UTF-8"}
	version, versionErr := versionCmd.Output()
	versionCancel()
	if versionErr != nil || !subscriptionNodeVersionSupported(string(version)) {
		return nil, NewHTTPError(503, "subscription_runtime_unavailable", "Node.js 22.23.1 or later is required for this subscription")
	}
	directory, err := os.MkdirTemp("", "tokenhub-subscription-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if err := subscriptionworker.WriteRuntime(directory); err != nil {
		cleanup()
		return nil, err
	}
	home := filepath.Join(directory, "account")
	if err := os.Mkdir(home, 0o700); err != nil {
		cleanup()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, node, "--max-old-space-size=256", filepath.Join(directory, "worker.mjs"))
	cmd.Dir = home
	// Never inherit NODE_OPTIONS, provider tokens, real home directories, proxy
	// credentials, or a developer's agent sessions into the bundled runtime.
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "TMPDIR=" + home, "LANG=C.UTF-8"}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		cleanup()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		cancel()
		cleanup()
		return nil, err
	}
	p := &subscriptionWorkerProcess{ctx: ctx, cancel: cancel, cmd: cmd, input: input, client: subscriptionClient(client), directory: directory,
		pending: map[string]chan subscriptionWorkerEvent{}, networks: map[string]context.CancelFunc{}, callGate: make(chan struct{}, 1), done: make(chan struct{}), auth: auth, saveAuth: save, lastUsed: time.Now()}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		cancel()
		cleanup()
		return nil, NewHTTPError(503, "subscription_runtime_unavailable", "The subscription runtime could not start")
	}
	go p.read(output)
	return p, nil
}

func (p *subscriptionWorkerProcess) read(output io.Reader) {
	defer func() {
		p.cancel()
		_ = p.input.Close()
		_ = p.cmd.Wait()
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		_ = os.RemoveAll(p.directory)
		close(p.done)
	}()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), subscriptionWorkerMessageLimit)
	for scanner.Scan() {
		var event subscriptionWorkerEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			return
		}
		switch event.Event {
		case "network.request":
			p.startNetwork(event)
		case "network.cancel":
			p.mu.Lock()
			cancel := p.networks[event.RequestID]
			p.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case "auth.update":
			// Process rotations in order. The worker waits for this durable ack
			// before using a newly issued credential.
			p.updateAuth(event)
		default:
			p.mu.Lock()
			ch := p.pending[event.ID]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- event:
				case <-p.ctx.Done():
					return
				}
			}
		}
	}
}

func (p *subscriptionWorkerProcess) send(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > subscriptionWorkerMessageLimit-1 {
		return NewHTTPError(413, "subscription_payload_too_large", "Subscription request exceeds the runtime limit")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.input.Write(append(encoded, '\n'))
	return err
}

func (p *subscriptionWorkerProcess) authSnapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.auth
}

func (p *subscriptionWorkerProcess) updateAuth(event subscriptionWorkerEvent) {
	ack := map[string]any{"operation": "auth.ack", "update_id": event.UpdateID}
	next := string(event.Auth)
	p.invocationSecrets(event.ID).addAuth(next)
	if len(next) > 512<<10 || !json.Valid(event.Auth) || strings.TrimSpace(next) == "null" {
		ack["error"] = "invalid_credentials"
	} else {
		previous := p.authSnapshot()
		var err error
		if p.saveAuth != nil {
			p.mu.Lock()
			saveCtx := p.operationCtx
			p.mu.Unlock()
			if saveCtx == nil {
				saveCtx = p.ctx
			}
			err = p.saveAuth(saveCtx, previous, next)
		}
		if err != nil {
			ack["error"] = "credential_persistence_failed"
		} else {
			p.mu.Lock()
			p.auth = next
			p.mu.Unlock()
		}
	}
	_ = p.send(ack)
}

func (p *subscriptionWorkerProcess) startNetwork(event subscriptionWorkerEvent) {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Minute)
	p.mu.Lock()
	if event.RequestID == "" || len(p.networks) >= 16 || p.networks[event.RequestID] != nil {
		p.mu.Unlock()
		cancel()
		_ = p.send(map[string]any{"operation": "network.error", "request_id": event.RequestID, "error": "upstream_request_failed"})
		return
	}
	// Register in stdout order so the next frame can cancel this request even
	// before the network goroutine is scheduled.
	p.networks[event.RequestID] = cancel
	p.mu.Unlock()
	go func() {
		defer func() { cancel(); p.mu.Lock(); delete(p.networks, event.RequestID); p.mu.Unlock() }()
		p.network(ctx, event)
	}()
}

func (p *subscriptionWorkerProcess) network(ctx context.Context, event subscriptionWorkerEvent) {
	failed := func(err error) {
		_ = p.send(subscriptionWorkerNetworkFailure(event.RequestID, err))
	}
	body, err := base64.StdEncoding.DecodeString(event.Body)
	if err != nil || len(body) > 16<<20 {
		failed(err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, event.Method, event.URL, strings.NewReader(string(body)))
	if err != nil || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Fragment != "" {
		failed(err)
		return
	}
	for name, value := range event.Headers {
		req.Header[name] = append([]string(nil), value...)
	}
	p.invocationSecrets(event.ID).addHeaders(req.Header)
	resp, err := p.client.Do(req)
	if err != nil {
		failed(err)
		return
	}
	defer resp.Body.Close()
	headers := resp.Header.Clone()
	if p.send(map[string]any{"operation": "network.response", "request_id": event.RequestID, "status": resp.StatusCode, "headers": headers}) != nil {
		return
	}
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 && p.send(map[string]any{"operation": "network.chunk", "request_id": event.RequestID, "data": base64.StdEncoding.EncodeToString(buffer[:n])}) != nil {
			return
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				_ = p.send(map[string]any{"operation": "network.end", "request_id": event.RequestID})
			} else {
				failed(readErr)
			}
			return
		}
	}
}

func (p *subscriptionWorkerProcess) invoke(ctx context.Context, command map[string]any, consume func(subscriptionWorkerEvent) error) (json.RawMessage, error) {
	select {
	case p.callGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, NewHTTPError(502, "subscription_runtime_stopped", "The subscription runtime stopped; retry the request")
	}
	defer func() { <-p.callGate }()
	id := NewID("subscription")
	command["id"] = id
	if options, ok := ctx.Value(subscriptionWorkerOptionsKey{}).(map[string]bool); ok {
		command["options"] = options
	}
	if auth := p.authSnapshot(); auth != "" {
		command["auth"] = json.RawMessage(auth)
		subscriptionWorkerSecretsFromContext(ctx).addAuth(auth)
	}
	ch := make(chan subscriptionWorkerEvent, 16)
	p.mu.Lock()
	p.pending[id] = ch
	if secrets := subscriptionWorkerSecretsFromContext(ctx); secrets != nil {
		if p.secrets == nil {
			p.secrets = make(map[string]*subscriptionWorkerSecrets)
		}
		p.secrets[id] = secrets
	}
	p.operationCtx = ctx
	p.lastUsed = time.Now()
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		delete(p.secrets, id)
		p.operationCtx = nil
		p.lastUsed = time.Now()
		p.mu.Unlock()
	}()
	if err := p.send(command); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			p.cancel()
			return nil, ctx.Err()
		case <-p.ctx.Done():
			return nil, NewHTTPError(502, "subscription_runtime_stopped", "The subscription runtime stopped; retry the request")
		case event := <-ch:
			switch event.Event {
			case "result":
				return event.Result, nil
			case "end":
				return nil, nil
			case "error":
				return nil, subscriptionWorkerInvocationError(event.Error)
			default:
				if consume != nil {
					if err := consume(event); err != nil {
						p.cancel()
						return nil, err
					}
				}
			}
		}
	}
}

func subscriptionNodeVersionSupported(version string) bool {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) != 3 {
		return false
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	patch, e3 := strconv.Atoi(parts[2])
	return e1 == nil && e2 == nil && e3 == nil && (major > 22 || major == 22 && (minor > 23 || minor == 23 && patch >= 1))
}

type subscriptionWorkerFailure struct {
	Code           string `json:"code"`
	Status         int    `json:"status"`
	SignIn         string `json:"sign_in"`
	Stage          string `json:"stage"`
	UpstreamStatus int    `json:"upstream_status"`
	UpstreamCode   *int64 `json:"upstream_code"`
}

func subscriptionWorkerInvocationError(failure *subscriptionWorkerFailure) error {
	status := http.StatusBadGateway
	if failure != nil {
		if failure.Code == "workbuddy_authorization_failed" {
			return subscriptionAuthorizationFailureDetails(failure)
		}
		if err := subscriptionWorkerEgressError(failure.Code); err != nil {
			return err
		}
		if failure.Status >= 400 && failure.Status <= 599 {
			status = failure.Status
		}
		switch failure.Code {
		case "invalid_credentials":
			status = http.StatusUnauthorized
		case "model_not_found":
			status = http.StatusNotFound
		case "invalid_request", "invalid_session", "callback_not_allowed", "local_credentials_not_supported", "method_not_supported", "protocol_mismatch":
			status = http.StatusBadRequest
		case "body_too_large", "credentials_too_large":
			status = http.StatusRequestEntityTooLarge
		case "credential_save_failed":
			status = http.StatusServiceUnavailable
		}
		if failure.SignIn == "expired" {
			status = http.StatusUnauthorized
		}
	}
	classification := classifyProviderStatus(status)
	message := "Subscription request failed; check the account and available models"
	if classification.disposition == ProviderErrorAuthBroken {
		message = "Subscription authorization expired or was rejected; sign in again"
	}
	return &ProviderInvocationError{Err: NewHTTPError(classification.status, classification.code, message), Disposition: classification.disposition}
}
