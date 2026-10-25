package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type claudeSubscriptionSignIn struct {
	mu                             sync.Mutex
	adapter                        *ClaudeSubscriptionAdapter
	id, actor, state, root, config string
	authURL, credentials           string
	callback                       *url.URL
	input                          io.WriteCloser
	command                        *exec.Cmd
	cancel                         context.CancelFunc
	expires                        time.Time
	done                           chan struct{}
	timer                          *time.Timer
	err                            error
	submitted, consumed            bool
}

var claudeSignInURLPattern = regexp.MustCompile(`https://[^\s\x1b<>"']+`)

func (a *ClaudeSubscriptionAdapter) startSignIn(ctx context.Context, actor string) (map[string]any, error) {
	executable := a.Executable
	if executable == "" {
		var err error
		executable, err = exec.LookPath("claude")
		if err != nil {
			return nil, NewHTTPError(503, "claude_cli_unavailable", "Install the genuine Claude Code CLI on the TokenHub host")
		}
	}
	state, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "tokenhub-claude-signin-")
	if err != nil {
		return nil, err
	}
	loginCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	login := &claudeSubscriptionSignIn{adapter: a, id: NewID("claude_login"), actor: actor, state: state, root: root, config: filepath.Join(root, "config"), cancel: cancel, expires: time.Now().Add(10 * time.Minute), done: make(chan struct{})}
	a.mu.Lock()
	if a.signIns == nil {
		a.signIns = make(map[string]*claudeSubscriptionSignIn)
	}
	if len(a.signIns) >= 8 {
		a.mu.Unlock()
		cancel()
		_ = os.RemoveAll(root)
		return nil, NewHTTPError(429, "claude_signin_busy", "Too many Claude Code sign-ins are pending")
	}
	if err := login.launch(loginCtx, executable, a.ExecutableArgs); err != nil {
		a.mu.Unlock()
		login.cancel()
		_ = os.RemoveAll(root)
		return nil, err
	}
	login.mu.Lock()
	login.timer = time.AfterFunc(time.Until(login.expires), func() { a.finishSignIn(login) })
	login.mu.Unlock()
	a.signIns[login.id] = login
	a.mu.Unlock()
	link, err := login.waitURL(ctx)
	if err != nil {
		a.finishSignIn(login)
		return nil, err
	}
	return map[string]any{"session_id": login.id, "state": state, "auth_url": link, "flow": "auto", "poll_interval": 2, "expires_at": login.expires.UTC().Format(time.RFC3339), "instructions": "Complete Claude Code authorization in the browser. If needed, paste its code or full localhost callback URL here."}, nil
}

func (s *claudeSubscriptionSignIn) launch(ctx context.Context, executable string, initialArgs []string) error {
	if err := os.Mkdir(s.config, 0700); err != nil {
		return err
	}
	opener, err := claudeSubscriptionBrowserOpener(s.root)
	if err != nil {
		return err
	}
	args := append(append([]string(nil), initialArgs...), "auth", "login", "--claudeai")
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = s.root
	command.Env = append(claudeSubscriptionEnv(s.root, s.config, ProviderResourceCredentials{SessionJSON: "login"}), "BROWSER="+opener, "TOKENHUB_CLAUDE_OPENED_URL="+filepath.Join(s.root, "opened-url"))
	configureClaudeSubscriptionProcess(command)
	command.Cancel = func() error { killClaudeSubscriptionProcess(command); return nil }
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return err
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return NewHTTPError(503, "claude_cli_start_failed", "Claude Code could not be started")
	}
	s.command, s.input = command, input
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			if link := claudeSignInURLPattern.FindString(scanner.Text()); link != "" {
				s.rememberURL(link, false)
			}
		}
		if scanner.Err() != nil {
			s.cancel()
		}
		err := command.Wait()
		_ = input.Close()
		var credentials []byte
		if err == nil {
			credentials, err = readClaudeSubscriptionSession(s.config)
			if err == nil {
				err = validateClaudeSubscriptionSession(string(credentials))
			}
		}
		clearClaudeSubscriptionSession(s.config)
		_ = os.RemoveAll(s.root)
		s.mu.Lock()
		if err != nil {
			s.err = NewHTTPError(502, "claude_signin_failed", "Claude Code sign-in did not complete; start authorization again")
		} else if !s.consumed {
			s.credentials = string(credentials)
		}
		close(s.done)
		if s.consumed {
			s.adapter.removeSignIn(s)
		}
		s.mu.Unlock()
		s.cancel()
	}()
	return nil
}

func (s *claudeSubscriptionSignIn) rememberURL(raw string, preferred bool) bool {
	if len(raw) > 16<<10 {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	trusted := false
	for _, domain := range []string{"claude.ai", "claude.com", "anthropic.com"} {
		trusted = trusted || host == domain || strings.HasSuffix(host, "."+domain)
	}
	if !trusted || u.Query().Get("state") == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authURL == "" || preferred {
		s.authURL = u.String()
		s.callback = nil
		callback, err := url.Parse(u.Query().Get("redirect_uri"))
		if err == nil && callback.Scheme == "http" && callback.User == nil && callback.Port() != "" && claudeSignInLoopback(callback.Hostname()) {
			s.callback = callback
		}
	}
	return true
}

func (s *claudeSubscriptionSignIn) waitURL(ctx context.Context) (string, error) {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var printedAt time.Time
	for {
		opened := false
		if raw, err := os.ReadFile(filepath.Join(s.root, "opened-url")); err == nil && len(raw) <= 16<<10 {
			opened = s.rememberURL(string(raw), true)
		}
		s.mu.Lock()
		link := s.authURL
		s.mu.Unlock()
		if link != "" {
			if printedAt.IsZero() {
				printedAt = time.Now()
			}
			if opened || time.Since(printedAt) >= 300*time.Millisecond {
				return link, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", NewHTTPError(504, "claude_signin_url_timeout", "Claude Code did not produce an authorization link")
		case <-s.done:
			return "", NewHTTPError(502, "claude_signin_failed", "Claude Code ended before authorization started")
		case <-ticker.C:
		}
	}
}

func (a *ClaudeSubscriptionAdapter) continueSignIn(ctx context.Context, actor, id, state, raw string, cancel bool) (map[string]any, error) {
	a.mu.Lock()
	login := a.signIns[id]
	a.mu.Unlock()
	invalid := NewHTTPError(400, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	if login == nil {
		return nil, invalid
	}
	login.mu.Lock()
	defer login.mu.Unlock()
	if login.actor != actor || subtle.ConstantTimeCompare([]byte(login.state), []byte(state)) != 1 || !login.expires.After(time.Now()) || login.consumed {
		return nil, invalid
	}
	if cancel {
		login.consumed = true
		login.credentials = ""
		login.cancel()
		select {
		case <-login.done:
			a.removeSignIn(login)
		default:
		}
		return map[string]any{"status": "cancelled"}, nil
	}
	select {
	case <-login.done:
		login.consumed = true
		a.removeSignIn(login)
		if login.err != nil {
			return nil, login.err
		}
		credentials := login.credentials
		login.credentials = ""
		return map[string]any{"status": "complete", "session_json": credentials, "catalog": claudeSubscriptionCatalog()}, nil
	default:
	}
	if strings.TrimSpace(raw) != "" && !login.submitted {
		if err := login.submit(ctx, raw); err != nil {
			return nil, err
		}
		login.submitted = true
	}
	return map[string]any{"status": "pending", "poll_interval": 2}, nil
}

func (a *ClaudeSubscriptionAdapter) removeSignIn(login *claudeSubscriptionSignIn) {
	if login.timer != nil {
		login.timer.Stop()
	}
	a.mu.Lock()
	delete(a.signIns, login.id)
	a.mu.Unlock()
}

func (a *ClaudeSubscriptionAdapter) finishSignIn(login *claudeSubscriptionSignIn) {
	login.mu.Lock()
	login.consumed = true
	login.credentials = ""
	login.cancel()
	select {
	case <-login.done:
		a.removeSignIn(login)
	default:
	}
	login.mu.Unlock()
}

func (a *ClaudeSubscriptionAdapter) closeSignIns() {
	a.mu.Lock()
	logins := make([]*claudeSubscriptionSignIn, 0, len(a.signIns))
	for _, login := range a.signIns {
		logins = append(logins, login)
	}
	a.mu.Unlock()
	for _, login := range logins {
		a.finishSignIn(login)
		<-login.done
	}
}
