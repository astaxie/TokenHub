package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type subscriptionWorkerAuthorization struct {
	mu                                                 sync.Mutex
	actorID, providerID, state, poolKey, workerSession string
	expires                                            time.Time
	process                                            *subscriptionWorkerProcess
	release                                            func()
}

type subscriptionWorkerAuthInput struct {
	MethodID    string            `json:"method_id"`
	Inputs      map[string]string `json:"inputs"`
	Key         string            `json:"key"`
	SessionID   string            `json:"session_id"`
	State       string            `json:"state"`
	Code        string            `json:"code"`
	CallbackURL string            `json:"callback_url"`
}

func (a *SubscriptionWorkerAdapter) authorizationMethods(ctx context.Context) (map[string]any, error) {
	p, release, err := a.Pool.acquire(ctx, NewID("methods"), "", nil)
	if err != nil {
		return nil, err
	}
	defer release()
	defer p.cancel()
	return a.describeMethods(ctx, p, "methods")
}

func (a *SubscriptionWorkerAdapter) describeMethods(ctx context.Context, p *subscriptionWorkerProcess, resourceID string) (map[string]any, error) {
	raw, err := p.invoke(ctx, a.command(resourceID, "describe"), nil)
	if err != nil {
		return nil, err
	}
	var info map[string]any
	if json.Unmarshal(raw, &info) != nil {
		return nil, NewHTTPError(502, "subscription_methods_invalid", "Subscription login methods are unavailable")
	}
	methods, _ := info["methods"].([]any)
	for _, value := range methods {
		method, ok := value.(map[string]any)
		if !ok {
			continue
		}
		method["id"] = method["method_id"]
		prompts, _ := method["prompts"].([]any)
		for _, value := range prompts {
			if prompt, ok := value.(map[string]any); ok {
				prompt["label"] = prompt["message"]
			}
		}
		if method["type"] == "api" && method["keyless"] != true {
			method["prompts"] = append(prompts, map[string]any{"key": "__tokenhub_api_key", "label": "API key", "type": "password"})
		}
	}
	return map[string]any{"methods": methods}, nil
}

func (a *SubscriptionWorkerAdapter) startAuthorization(ctx context.Context, actorID string, input subscriptionWorkerAuthInput) (map[string]any, error) {
	if actorID == "" {
		return nil, NewHTTPError(403, "subscription_actor_required", "An authenticated administrator is required")
	}
	key := NewID("subscription_login")
	p, release, err := a.Pool.acquire(ctx, key, "", nil)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			p.cancel()
			release()
		}
	}()
	description, err := a.describeMethods(ctx, p, key)
	if err != nil {
		return nil, err
	}
	methods, _ := description["methods"].([]any)
	methodID := strings.TrimSpace(input.MethodID)
	if methodID == "" && len(methods) > 0 {
		if first, ok := methods[0].(map[string]any); ok {
			methodID, _ = first["method_id"].(string)
		}
	}
	if methodID == "" {
		return nil, NewHTTPError(400, "subscription_import_required", "This subscription requires an explicit session JSON import")
	}
	command := a.command(key, "auth.start")
	command["method_id"] = methodID
	inputs := cloneStringMap(input.Inputs)
	if inputs == nil {
		inputs = map[string]string{}
	}
	token := firstNonEmpty(input.Key, inputs["__tokenhub_api_key"])
	delete(inputs, "__tokenhub_api_key")
	command["inputs"] = inputs
	if token != "" {
		command["key"] = token
	}
	raw, err := p.invoke(ctx, command, nil)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return nil, NewHTTPError(502, "subscription_authorization_invalid", "Subscription authorization could not start")
	}

	nativeID, _ := result["session_id"].(string)
	if nativeID == "" && result["state"] != "complete" {
		return nil, NewHTTPError(502, "subscription_authorization_invalid", "Subscription authorization session is missing")
	}
	state, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	session := &subscriptionWorkerAuthorization{actorID: actorID, providerID: a.Definition.ID, state: state, poolKey: key, workerSession: nativeID, expires: time.Now().Add(10 * time.Minute), process: p, release: release}
	a.Pool.mu.Lock()
	a.Pool.sessions[key] = session
	a.Pool.mu.Unlock()
	keep = true
	flow := "auto"
	if result["method"] == "code" {
		flow = "authorization_code"
	}
	authURL, _ := result["url"].(string)
	localListener, _ := result["local_listener"].(bool)
	if authURL != "" {
		if !subscriptionAuthorizationURLAllowed(authURL, localListener) {
			a.endAuthorization(key, session)
			return nil, NewHTTPError(502, "subscription_authorization_url_invalid", "Subscription authorization returned an invalid URL")
		}
	}
	return map[string]any{"session_id": key, "state": state, "flow": flow, "auth_url": authURL, "local_listener": localListener, "instructions": result["instructions"], "poll_interval": 3, "expires_at": session.expires.UTC().Format(time.RFC3339)}, nil
}

func subscriptionAuthorizationURLAllowed(value string, localListener bool) bool {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	// This marker is emitted only by our bundled host after matching the URL
	// to a listener created by the same pending authorization session.
	if !localListener || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535
}

func (a *SubscriptionWorkerAdapter) endAuthorization(key string, session *subscriptionWorkerAuthorization) {
	a.Pool.mu.Lock()
	if a.Pool.sessions[key] != session {
		a.Pool.mu.Unlock()
		return
	}
	delete(a.Pool.sessions, key)
	a.Pool.mu.Unlock()
	session.process.cancel()
	session.release()
}

func (a *SubscriptionWorkerAdapter) continueAuthorization(ctx context.Context, actorID string, input subscriptionWorkerAuthInput, cancel bool) (map[string]any, error) {
	a.Pool.mu.Lock()
	session := a.Pool.sessions[input.SessionID]
	a.Pool.mu.Unlock()
	invalid := NewHTTPError(400, "subscription_session_invalid", "Subscription authorization session is invalid or expired")
	if session == nil || session.actorID != actorID || session.providerID != a.Definition.ID || !session.expires.After(time.Now()) || subtle.ConstantTimeCompare([]byte(session.state), []byte(input.State)) != 1 {
		return nil, invalid
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	a.Pool.mu.Lock()
	current := a.Pool.sessions[input.SessionID]
	a.Pool.mu.Unlock()
	if current != session || !session.expires.After(time.Now()) {
		return nil, invalid
	}
	if cancel {
		a.endAuthorization(input.SessionID, session)
		return map[string]any{"status": "cancelled"}, nil
	}
	if session.workerSession == "" {
		result, err := a.authorizationCredentials(ctx, session.process, session.poolKey)
		if err == nil {
			a.endAuthorization(input.SessionID, session)
		}
		return result, err
	}
	command := a.command(session.poolKey, "auth.status")
	command["session_id"] = session.workerSession
	if strings.TrimSpace(input.CallbackURL) != "" || strings.TrimSpace(input.Code) != "" {
		command["operation"] = "auth.callback"
		callback := strings.TrimSpace(input.CallbackURL)
		if u, err := url.Parse(callback); err == nil && u.Scheme != "" {
			command["url"] = callback
		} else {
			command["code"] = firstNonEmpty(strings.TrimSpace(input.Code), callback)
		}
		if _, err := session.process.invoke(ctx, command, nil); err != nil {
			return nil, err
		}
		command["operation"] = "auth.status"
		delete(command, "url")
		delete(command, "code")
	}
	raw, err := session.process.invoke(ctx, command, nil)
	if err != nil {
		a.endAuthorization(input.SessionID, session)
		return nil, err
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return nil, invalid
	}
	switch result["state"] {
	case "pending":
		return map[string]any{"status": "pending", "poll_interval": 3}, nil
	case "complete":
		credentials, err := a.authorizationCredentials(ctx, session.process, session.poolKey)
		if err == nil {
			a.endAuthorization(input.SessionID, session)
		}
		return credentials, err
	default:
		a.endAuthorization(input.SessionID, session)
		return nil, NewHTTPError(400, "subscription_authorization_failed", "Subscription authorization failed or expired; start again")
	}
}

func (a *SubscriptionWorkerAdapter) authorizationCredentials(ctx context.Context, p *subscriptionWorkerProcess, resourceID string) (map[string]any, error) {
	// Capture the newest auth after model discovery, which can rotate a refresh
	// token. A model-discovery outage must not discard a completed login.
	models, modelErr := a.workerModels(ctx, p, resourceID)
	auth := p.authSnapshot()
	if err := validateSubscriptionSession(auth); err != nil {
		return nil, err
	}
	result := map[string]any{"session_json": auth}
	if modelErr == nil {
		result["catalog"] = a.catalog(models)
	} else {
		result["catalog_unavailable"] = true
	}
	return result, nil
}
