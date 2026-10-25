package server

import (
	"context"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func claudeSubscriptionBrowserOpener(root string) (string, error) {
	// On Windows the CLI prints its paste-code URL when no browser is available.
	// Avoid invoking a command shell with an authorization URL as shell input.
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "no-browser.exe"), nil
	}
	path := filepath.Join(root, "open-browser")
	err := os.WriteFile(path, []byte("#!/bin/sh\numask 077\nprintf '%s' \"$1\" > \"$TOKENHUB_CLAUDE_OPENED_URL\"\n"), 0700)
	return path, err
}

func claudeSignInLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// submit runs with the login lock held. Only the callback advertised by this
// isolated CLI process is reachable; the CLI still validates its PKCE exchange.
func (s *claudeSubscriptionSignIn) submit(ctx context.Context, raw string) error {
	raw = strings.TrimSpace(raw)
	invalid := NewHTTPError(400, "claude_signin_callback_invalid", "Paste the code or localhost callback URL from this Claude Code sign-in")
	if len(raw) > 16<<10 || strings.ContainsAny(raw, "\r\n\t ") {
		return invalid
	}
	authorization, err := url.Parse(s.authURL)
	if err != nil {
		return invalid
	}
	state := authorization.Query().Get("state")
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "http" || parsed.User != nil || parsed.Fragment != "" || s.callback == nil || !claudeSignInLoopback(parsed.Hostname()) || parsed.Port() != s.callback.Port() || parsed.EscapedPath() != s.callback.EscapedPath() || parsed.Query().Get("code") == "" || subtle.ConstantTimeCompare([]byte(parsed.Query().Get("state")), []byte(state)) != 1 {
			return invalid
		}
		address := "127.0.0.1"
		if s.callback.Hostname() == "::1" {
			address = "::1"
		}
		parsed.Host = net.JoinHostPort(address, s.callback.Port())
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return invalid
		}
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		response, err := client.Do(request)
		if err != nil {
			return NewHTTPError(502, "claude_signin_callback_failed", "Claude Code did not accept the callback; retry authorization")
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10))
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return NewHTTPError(400, "claude_signin_callback_rejected", "Claude Code rejected the callback; check the authorization result")
		}
		return nil
	}
	code, codeState, ok := strings.Cut(raw, "#")
	if !ok || code == "" || codeState == "" || subtle.ConstantTimeCompare([]byte(codeState), []byte(state)) != 1 {
		return invalid
	}
	if _, err := io.WriteString(s.input, raw+"\n"); err != nil {
		return NewHTTPError(502, "claude_signin_closed", "Claude Code sign-in ended; start authorization again")
	}
	return nil
}
