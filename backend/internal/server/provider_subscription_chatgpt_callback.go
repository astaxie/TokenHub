package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type chatGPTSubscriptionCallback struct {
	Code     string `json:"code,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Denied   bool   `json:"denied,omitempty"`
}

type chatGPTSubscriptionCallbackStore interface {
	captureChatGPTSubscriptionCallback(context.Context, string, string, chatGPTSubscriptionCallback) error
}

func chatGPTSubscriptionAutomaticRedirect(publicBase string) string {
	target, err := url.Parse(strings.TrimSpace(publicBase))
	if err != nil || target.Scheme != "http" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return ""
	}
	switch strings.ToLower(target.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
	default:
		return ""
	}
	if target.Port() != "" {
		port, err := strconv.Atoi(target.Port())
		if err != nil || port < 1 || port > 65535 {
			return ""
		}
		target.Host = net.JoinHostPort("127.0.0.1", target.Port())
	} else {
		target.Host = "127.0.0.1"
	}
	target.Path = "/auth/callback"
	return target.String()
}

func (s *Server) handleChatGPTSubscriptionCallback(w http.ResponseWriter, r *http.Request) {
	callbackURL := chatGPTSubscriptionAutomaticRedirect(s.config.PublicBaseURL)
	store, supported := s.store.(chatGPTSubscriptionCallbackStore)
	if callbackURL == "" || !supported {
		chatGPTSubscriptionCallbackPage(w, http.StatusNotFound)
		return
	}
	if len(r.URL.RawQuery) > 32<<10 {
		chatGPTSubscriptionCallbackPage(w, http.StatusBadRequest)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	state, code, clientID, denied := query.Get("state"), query.Get("code"), query.Get("client_id"), query.Get("error")
	if err != nil || len(query["state"]) != 1 || state == "" || len(state) > 256 || len(code) > 8192 || len(clientID) > 1024 || len(denied) > 256 || len(query["code"]) > 1 || len(query["client_id"]) > 1 || len(query["error"]) > 1 ||
		(denied != "" && (code != "" || clientID != "")) || (denied == "" && (strings.TrimSpace(code) == "" || clientID == "dynamic_agent_client")) {
		chatGPTSubscriptionCallbackPage(w, http.StatusBadRequest)
		return
	}
	callback := chatGPTSubscriptionCallback{Code: code, ClientID: clientID, Denied: denied != ""}
	if err := store.captureChatGPTSubscriptionCallback(r.Context(), state, callbackURL, callback); err != nil {
		chatGPTSubscriptionCallbackPage(w, AsHTTPError(err).Status)
		return
	}
	chatGPTSubscriptionCallbackPage(w, http.StatusOK)
}

func chatGPTSubscriptionCallbackPage(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if status == http.StatusOK {
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><title>TokenHub authorization</title><h1>Authorization received</h1><p>Return to TokenHub to finish adding the account.</p><p lang="zh-CN">授权结果已收到，请返回 TokenHub 继续添加账号。</p></html>`)
	} else {
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><title>TokenHub authorization</title><h1>Authorization callback unavailable</h1><p>Return to TokenHub and check the authorization status.</p><p lang="zh-CN">此授权回调不可用，请返回 TokenHub 查看授权状态。</p></html>`)
	}
}
