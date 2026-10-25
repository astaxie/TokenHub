package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func claudeSignInFixture(t *testing.T, mode string) *ClaudeSubscriptionAdapter {
	t.Helper()
	adapter := &ClaudeSubscriptionAdapter{Executable: os.Args[0], ExecutableArgs: []string{"-test.run=^TestClaudeSignInHelperProcess$", "--", "tokenhub-claude-signin-fixture", mode}}
	t.Cleanup(adapter.Close)
	return adapter
}

func claudeSignInResult(t *testing.T, adapter *ClaudeSubscriptionAdapter, started map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := adapter.continueSignIn(context.Background(), "admin", started["session_id"].(string), started["state"].(string), "", false)
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] == "complete" {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Claude Code sign-in did not finish")
	return nil
}

func TestClaudeSignInUsesIsolatedCLIAndBindsPasteToActorAndState(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "host-token")
	t.Setenv("ANTHROPIC_API_KEY", "host-api-key")
	adapter := claudeSignInFixture(t, "code")
	started, err := adapter.startSignIn(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	id, state := started["session_id"].(string), started["state"].(string)
	for _, test := range []struct{ actor, state string }{{"other-admin", state}, {"admin", "wrong-state"}} {
		if _, err := adapter.continueSignIn(context.Background(), test.actor, id, test.state, "fixture-code#fixture-cli-state", false); AsHTTPError(err).Code != "invalid_oauth_state" {
			t.Fatalf("unbound exchange accepted: %v", err)
		}
	}
	if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, "fixture-code#other-cli-state", false); AsHTTPError(err).Code != "claude_signin_callback_invalid" {
		t.Fatalf("foreign CLI state accepted: %v", err)
	}
	adapter.mu.Lock()
	root := adapter.signIns[id].root
	adapter.mu.Unlock()
	if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, "fixture-code#fixture-cli-state", false); err != nil {
		t.Fatal(err)
	}
	result := claudeSignInResult(t, adapter, started)
	if !strings.Contains(result["session_json"].(string), "fixture-refresh") || result["catalog"] == nil {
		t.Fatal("authorization lost the CLI session or catalog")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("completed sign-in retained its temporary credentials")
	}
	if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, "", false); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("completed login replayed: %v", err)
	}
}

func TestClaudeSignInRelaysOnlyItsExactCallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the printed paste-code flow")
	}
	adapter := claudeSignInFixture(t, "callback")
	started, err := adapter.startSignIn(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started["auth_url"].(string))
	callback, _ := url.Parse(authorization.Query().Get("redirect_uri"))
	callback.RawQuery = url.Values{"code": {"fixture-code"}, "state": {"fixture-cli-state"}}.Encode()
	id, state := started["session_id"].(string), started["state"].(string)
	for _, edit := range []func(*url.URL){func(u *url.URL) { u.Host = "example.com:" + u.Port() }, func(u *url.URL) { u.Path = "/foreign" }, func(u *url.URL) { u.RawQuery = "code=fixture-code&state=foreign" }, func(u *url.URL) { u.Host = "localhost:1" }} {
		changed := *callback
		edit(&changed)
		if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, changed.String(), false); AsHTTPError(err).Code != "claude_signin_callback_invalid" {
			t.Fatalf("foreign callback accepted: %v", err)
		}
	}
	if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, callback.String(), false); err != nil {
		t.Fatal(err)
	}
	claudeSignInResult(t, adapter, started)
}

func TestClaudeSignInCancellationAndRequestDeadlineReapProcesses(t *testing.T) {
	adapter := claudeSignInFixture(t, "code")
	started, err := adapter.startSignIn(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	id, state := started["session_id"].(string), started["state"].(string)
	adapter.mu.Lock()
	login := adapter.signIns[id]
	adapter.mu.Unlock()
	if _, err := adapter.continueSignIn(context.Background(), "other-admin", id, state, "", true); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("foreign cancellation accepted: %v", err)
	}
	if _, err := adapter.continueSignIn(context.Background(), "admin", id, state, "", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-login.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled CLI process was not reaped")
	}
	if _, err := os.Stat(login.root); !os.IsNotExist(err) {
		t.Fatal("cancelled authorization left a temporary directory")
	}
	idle := claudeSignInFixture(t, "idle")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := idle.startSignIn(ctx, "admin"); err != context.DeadlineExceeded {
		t.Fatalf("authorization start ignored cancellation: %v", err)
	}
	idle.Close()
	idle.mu.Lock()
	remaining := len(idle.signIns)
	idle.mu.Unlock()
	if remaining != 0 {
		t.Fatal("cancelled authorization retained a live process slot")
	}
}

func TestClaudeSignInHelperProcess(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "tokenhub-claude-signin-fixture" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" {
		return
	}
	if strings.Join(os.Args[len(os.Args)-3:], " ") != "auth login --claudeai" || os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" || !strings.Contains(os.Getenv("HOME"), "tokenhub-claude-signin-") {
		os.Exit(12)
	}
	if mode == "idle" {
		time.Sleep(time.Minute)
		os.Exit(12)
	}
	callback := "https://console.anthropic.com/oauth/code/callback"
	var completion chan struct{}
	if mode == "callback" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(12)
		}
		completion = make(chan struct{})
		callback = "http://" + listener.Addr().String() + "/callback"
		server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" || r.URL.Query().Get("code") != "fixture-code" || r.URL.Query().Get("state") != "fixture-cli-state" {
				w.WriteHeader(400)
				return
			}
			_, _ = fmt.Fprint(w, "Complete")
			close(completion)
		})}
		go func() { _ = server.Serve(listener) }()
		defer func() { _ = server.Close() }()
	}
	link := "https://claude.ai/oauth/authorize?" + url.Values{"state": {"fixture-cli-state"}, "redirect_uri": {callback}}.Encode()
	if mode == "callback" {
		if exec.Command(os.Getenv("BROWSER"), link).Run() != nil {
			os.Exit(12)
		}
		<-completion
	} else {
		fmt.Println(link)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || scanner.Text() != "fixture-code#fixture-cli-state" {
			os.Exit(12)
		}
	}
	encoded, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]string{"accessToken": "fixture-access", "refreshToken": "fixture-refresh"}})
	if os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), encoded, 0600) != nil {
		os.Exit(12)
	}
	os.Exit(0)
}
