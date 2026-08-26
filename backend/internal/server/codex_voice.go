package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	// codexVoiceModelName is the internal governance model used for authorization,
	// routing, quotas, and audit. It is never written into the upstream session.
	codexVoiceModelName = "codex-voice"
	// codexVoiceDefaultSidebandUpstreamBaseURL is OpenAI's Codex Voice sideband base.
	codexVoiceDefaultSidebandUpstreamBaseURL = "https://api.openai.com/v1"
	// codexVoiceAffinityKind isolates Voice call bindings from other adapter sessions.
	codexVoiceAffinityKind = "codex_voice_call"
	// codexVoiceHeaderMaxCount limits forwarded end-to-end header values.
	codexVoiceHeaderMaxCount = 128
	// codexVoiceHeaderValueMaxBytes limits one forwarded header value.
	codexVoiceHeaderValueMaxBytes = 16 << 10
	// codexVoiceHeadersMaxBytes limits the aggregate forwarded header size.
	codexVoiceHeadersMaxBytes = 64 << 10
)

// codexVoiceSelection holds the Codex subscription resource pinned to one
// Voice call. Credentials remain in memory and must not enter logs, responses,
// or persisted diagnostic payloads.
type codexVoiceSelection struct {
	Provider    Provider
	Resource    ProviderResource
	Credentials ProviderResourceCredentials
}

type codexVoiceCallResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	CallID     string
	Selection  codexVoiceSelection
}

type codexVoiceAuditSummary struct {
	Operation string `json:"operation"`
	BodyBytes int    `json:"body_bytes"`
}

// codexVoiceResponseFilteringTransport sanitizes upstream response headers
// before ReverseProxy removes the Connection declarations needed to identify
// extension hop-by-hop headers.
type codexVoiceResponseFilteringTransport struct {
	next http.RoundTripper
}

func (transport codexVoiceResponseFilteringTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if response != nil {
		stripCodexVoiceSensitiveResponseHeaders(response.Header, response.StatusCode == http.StatusSwitchingProtocols)
	}
	return response, err
}

// codexVoiceStrippedRequestHeaders lists headers that cannot cross the gateway
// security boundary. Other end-to-end headers pass through by default so new
// experimental Voice headers do not require a synchronized allowlist update.
var codexVoiceStrippedRequestHeaders = map[string]bool{
	"authorization":               true,
	"api-key":                     true,
	"x-api-key":                   true,
	"x-goog-api-key":              true,
	"chatgpt-account-id":          true,
	"cookie":                      true,
	"cookie2":                     true,
	"content-length":              true,
	"host":                        true,
	"forwarded":                   true,
	"proxy-authenticate":          true,
	"proxy-authorization":         true,
	"proxy-connection":            true,
	"x-forwarded-for":             true,
	"x-forwarded-host":            true,
	"x-forwarded-port":            true,
	"x-forwarded-proto":           true,
	"x-real-ip":                   true,
	"openai-organization":         true,
	"openai-project":              true,
	"x-tokenhub-upstream-account": true,
}

func (s *Server) handleCodexVoiceCallCreate(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	admittedAt := time.Now().UTC()
	if !capabilityAllowedByScopes(project, key, AccessCapabilityCodexVoice) {
		err := ErrModelNotAllowed
		requestID := s.finishRejectedCall(r, admittedAt, project, key, codexVoiceModelName, false, err, codexVoiceAuditSummary{Operation: "call_create"})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	body, err := s.readCodexVoiceRequestBody(w, r)
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, codexVoiceModelName, false, err, codexVoiceAuditSummary{Operation: "call_create"})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	auditPayload := codexVoiceAuditSummary{Operation: "call_create", BodyBytes: len(body)}
	call, err := s.admitRoutedCall(w, r, project, key, codexVoiceModelName, false, 0)
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, codexVoiceModelName, false, err, auditPayload)
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, codexVoiceModelName, auditPayload)
	if !ok {
		return
	}
	routed.Routes = s.codexVoiceRoutes(routed.Routes)
	if len(routed.Routes) == 0 {
		err := NewHTTPError(http.StatusServiceUnavailable, "codex_voice_resource_unavailable", "No routed Codex subscription resource is available for Voice")
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	result, route, _, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, candidate RouteSelection, _ bool, _ int) (codexVoiceCallResult, Usage, error) {
		result, err := s.executeCodexVoiceCall(ctx, r, body, candidate)
		return result, Usage{}, err
	})
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	selection := result.Selection
	if err := s.commitCodexVoiceBinding(r.Context(), key, result.CallID, selection); err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           route,
		Attempts:        attempts,
		StatusCode:      result.StatusCode,
		RequestPayload:  auditPayload,
		ResponsePayload: map[string]any{"call_created": true},
	})
	copyCodexVoiceResponseHeaders(w.Header(), result.Header)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	w.Header().Set("x-tokenhub-provider", selection.Provider.ID)
	w.Header().Set("x-tokenhub-provider-resource-id", selection.Resource.ID)
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}

func (s *Server) readCodexVoiceRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	limit := s.config.MaxMultimodalRequestBytes
	if limit <= 0 {
		limit = defaultMaxMultimodalRequestBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, NewHTTPError(http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("request body exceeds %d bytes", limit))
		}
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if len(body) == 0 {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_request", "request body is required")
	}
	return body, nil
}

func (s *Server) codexVoiceRoutes(routes []RouteSelection) []RouteSelection {
	routes = s.routesWithAdapterCapability(routes, AdapterCapabilityCodexVoice)
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if route.Provider.Type == ProviderOpenAICodex && route.Resource != nil && isOpenAIAccountResource(route.Resource.ResourceType) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func codexVoiceSelectionForRoute(route RouteSelection) (codexVoiceSelection, error) {
	if route.Provider.Type != ProviderOpenAICodex || route.Resource == nil || !isOpenAIAccountResource(route.Resource.ResourceType) {
		return codexVoiceSelection{}, NewHTTPError(http.StatusServiceUnavailable, "codex_voice_resource_unavailable", "The routed resource does not support Codex Voice")
	}
	credentials := ProviderResourceCredentials{
		AccessToken: strings.TrimSpace(route.Provider.APIKey),
		AccountID:   strings.TrimSpace(route.Provider.Options["account_id"]),
	}
	if credentials.AccessToken == "" || credentials.AccountID == "" {
		return codexVoiceSelection{}, NewHTTPError(http.StatusServiceUnavailable, "codex_voice_credentials_incomplete", "Codex Voice resource credentials are incomplete")
	}
	return codexVoiceSelection{Provider: route.Provider, Resource: *route.Resource, Credentials: credentials}, nil
}

func (s *Server) executeCodexVoiceCall(ctx context.Context, incoming *http.Request, body []byte, route RouteSelection) (codexVoiceCallResult, error) {
	route, err := s.prepareRouteForUpstream(ctx, route)
	if err != nil {
		return codexVoiceCallResult{}, err
	}
	selection, err := codexVoiceSelectionForRoute(route)
	if err != nil {
		return codexVoiceCallResult{}, err
	}
	response, err := s.createCodexVoiceCall(ctx, incoming, body, selection)
	if err != nil {
		return codexVoiceCallResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		return codexVoiceCallResult{}, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_voice_response_failed", "Codex Voice response could not be read"),
			Disposition: ProviderErrorTransientSame,
		}
	}
	if response.StatusCode >= http.StatusBadRequest {
		return codexVoiceCallResult{}, classifyCodexHTTPError(response.StatusCode, response.Header, data)
	}
	callID := codexVoiceCallID(response.Header.Get("Location"))
	if callID == "" {
		return codexVoiceCallResult{}, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_voice_call_location_missing", "Codex Voice call response is missing a call ID"),
			Disposition: ProviderErrorTransientSame,
		}
	}
	return codexVoiceCallResult{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       data,
		CallID:     callID,
		Selection:  selection,
	}, nil
}

func (s *Server) createCodexVoiceCall(ctx context.Context, incoming *http.Request, body []byte, selection codexVoiceSelection) (*http.Response, error) {
	endpoint, err := codexVoiceCallEndpoint(selection.Provider, selection.Resource, incoming.URL.Query())
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "codex_voice_request_failed", err.Error())
	}
	headers, err := codexVoiceForwardHeaders(incoming.Header)
	if err != nil {
		return nil, err
	}
	request.Header = headers
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(selection.Credentials.AccessToken))
	request.Header.Set("ChatGPT-Account-ID", strings.TrimSpace(selection.Credentials.AccountID))
	if contentType := boundedCodexVoiceHeaderValue(incoming.Header.Get("Content-Type")); contentType != "" {
		request.Header.Set("Content-Type", contentType)
	} else {
		request.Header.Set("Content-Type", "application/json")
	}
	setCodexVoiceIdentityFallbacks(request.Header)
	client := s.codexSubscription.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_voice_request_failed", fmt.Sprintf("Codex Voice request failed: %v", err)),
			Disposition: ProviderErrorTransientSame,
		}
	}
	return response, nil
}

func codexVoiceCallEndpoint(provider Provider, resource ProviderResource, incomingQuery url.Values) (string, error) {
	baseURL := firstNonEmpty(strings.TrimSpace(resource.BaseURL), strings.TrimSpace(provider.BaseURL), openAICodexBaseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusBadGateway, "codex_voice_upstream_invalid", "Codex Voice upstream URL is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/realtime/calls"
	query := parsed.Query()
	for name, values := range incomingQuery {
		for _, value := range values {
			query.Add(name, value)
		}
	}
	if query.Get("intent") == "" {
		query.Set("intent", "quicksilver")
	}
	if query.Get("architecture") == "" {
		query.Set("architecture", "avas")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func codexVoiceForwardHeaders(incoming http.Header) (http.Header, error) {
	result := make(http.Header)
	connectionHeaders := connectionHeaderNames(incoming)
	count := 0
	totalBytes := 0
	for name, values := range incoming {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if codexVoiceRequestHeaderStripped(normalized, connectionHeaders) {
			continue
		}
		for _, value := range values {
			if len(value) > codexVoiceHeaderValueMaxBytes || strings.ContainsAny(value, "\r\n") {
				return nil, NewHTTPError(http.StatusRequestHeaderFieldsTooLarge, "codex_voice_headers_too_large", "Codex Voice request headers exceed the allowed size")
			}
			value = boundedCodexVoiceHeaderValue(value)
			if value == "" {
				continue
			}
			count++
			totalBytes += len(name) + len(value)
			if count > codexVoiceHeaderMaxCount || totalBytes > codexVoiceHeadersMaxBytes {
				return nil, NewHTTPError(http.StatusRequestHeaderFieldsTooLarge, "codex_voice_headers_too_large", "Codex Voice request headers exceed the allowed size")
			}
			result.Add(name, value)
		}
	}
	return result, nil
}

func codexVoiceRequestHeaderStripped(name string, connectionHeaders map[string]bool) bool {
	if name == "" || codexVoiceStrippedRequestHeaders[name] || connectionHeaders[name] || strings.HasPrefix(name, "x-tokenhub-") {
		return true
	}
	switch name {
	case "connection", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func connectionHeaderNames(headers http.Header) map[string]bool {
	result := make(map[string]bool)
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
				result[name] = true
			}
		}
	}
	return result
}

func boundedCodexVoiceHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > codexVoiceHeaderValueMaxBytes || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func setCodexVoiceIdentityFallbacks(headers http.Header) {
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", openAICodexUserAgent)
	}
	if headers.Get("Version") == "" {
		headers.Set("Version", openAICodexVersion)
	}
	if headers.Get("Originator") == "" {
		headers.Set("Originator", "codex_cli_rs")
	}
}

func copyCodexVoiceResponseHeaders(target http.Header, source http.Header) {
	connectionHeaders := connectionHeaderNames(source)
	for name, values := range source {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "set-cookie" || normalized == "content-length" || codexVoiceRequestHeaderStripped(normalized, connectionHeaders) {
			continue
		}
		for _, value := range values {
			if value = boundedCodexVoiceHeaderValue(value); value != "" {
				target.Add(name, value)
			}
		}
	}
}

func stripCodexVoiceSensitiveResponseHeaders(headers http.Header, preserveUpgrade bool) {
	connectionHeaders := connectionHeaderNames(headers)
	for name := range headers {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if preserveUpgrade && normalized == "upgrade" {
			continue
		}
		if normalized == "set-cookie" || codexVoiceRequestHeaderStripped(normalized, connectionHeaders) {
			headers.Del(name)
		}
	}
	if preserveUpgrade {
		headers.Set("Connection", "Upgrade")
	}
}

func codexVoiceCallID(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	parsed, err := url.Parse(location)
	if err == nil && parsed.Path != "" {
		location = path.Base(strings.TrimRight(parsed.Path, "/"))
	}
	location = strings.TrimSpace(location)
	if location == "" || location == "." || location == "/" || validateSessionIdentifier(location, "codex_voice_call_id_invalid", "Codex Voice call ID") != nil {
		return ""
	}
	return location
}

func (s *Server) codexVoiceAffinityKey(key APIKey, callID string) string {
	base := deriveSessionAffinityKey(s.config.SecretKey, key.ID, "codex_voice\x00"+callID)
	return base
}

func (s *Server) commitCodexVoiceBinding(ctx context.Context, key APIKey, callID string, selection codexVoiceSelection) error {
	base := s.codexVoiceAffinityKey(key, callID)
	_, _, err := s.store.CommitAdapterSessionBinding(ctx, AdapterSessionBinding{
		AdapterType:     ProviderOpenAICodex,
		AffinityKind:    codexVoiceAffinityKind,
		ProviderID:      selection.Provider.ID,
		AffinityKeyHash: providerScopedAffinityKeyHash(base, selection.Provider.ID),
		ResourceID:      selection.Resource.ID,
	}, noBindingGeneration)
	if err != nil {
		return NewHTTPError(http.StatusBadGateway, "codex_voice_affinity_failed", "Codex Voice call could not be bound to its subscription resource")
	}
	return nil
}

func (s *Server) handleCodexVoiceLiveSideband(w http.ResponseWriter, r *http.Request) {
	s.handleCodexVoiceSideband(w, r, r.PathValue("call_id"), true)
}

func (s *Server) handleCodexVoiceRealtimeSideband(w http.ResponseWriter, r *http.Request) {
	s.handleCodexVoiceSideband(w, r, r.URL.Query().Get("call_id"), false)
}

func (s *Server) handleCodexVoiceSideband(w http.ResponseWriter, r *http.Request, callID string, frameless bool) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !capabilityAllowedByScopes(project, key, AccessCapabilityCodexVoice) {
		writeError(w, r, ErrModelNotAllowed)
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" || validateSessionIdentifier(callID, "codex_voice_call_id_invalid", "Codex Voice call ID") != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "codex_voice_call_id_invalid", "Codex Voice call ID is invalid"))
		return
	}
	if !webSocketUpgradeRequested(r.Header) {
		writeError(w, r, NewHTTPError(http.StatusUpgradeRequired, "codex_voice_websocket_required", "Codex Voice sideband requires a WebSocket upgrade"))
		return
	}
	if _, err := codexVoiceForwardHeaders(r.Header); err != nil {
		writeError(w, r, err)
		return
	}
	selection, binding, err := s.resolveCodexVoiceBinding(r.Context(), key, callID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	target, err := s.codexVoiceSidebandURL(callID, frameless)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.touchCodexVoiceBinding(r.Context(), binding)
	s.proxyCodexVoiceWebSocket(w, r, target, selection)
}

func webSocketUpgradeRequested(headers http.Header) bool {
	if !strings.EqualFold(strings.TrimSpace(headers.Get("Upgrade")), "websocket") {
		return false
	}
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func (s *Server) resolveCodexVoiceBinding(ctx context.Context, key APIKey, callID string) (codexVoiceSelection, AdapterSessionBinding, error) {
	base := s.codexVoiceAffinityKey(key, callID)
	for _, provider := range s.store.ListProviders() {
		if provider.Type != ProviderOpenAICodex || provider.Status != StatusActive || !provider.Healthy {
			continue
		}
		binding, ok, err := s.store.GetAdapterSessionBinding(ctx, ProviderOpenAICodex, provider.ID, providerScopedAffinityKeyHash(base, provider.ID))
		if err != nil {
			return codexVoiceSelection{}, AdapterSessionBinding{}, err
		}
		if !ok || binding.AffinityKind != codexVoiceAffinityKind {
			continue
		}
		resource, ok := s.store.GetProviderResource(binding.ResourceID)
		if !ok || resource.ProviderID != provider.ID || resource.Status != StatusActive || !resource.Healthy {
			return codexVoiceSelection{}, AdapterSessionBinding{}, NewHTTPError(http.StatusServiceUnavailable, "codex_voice_bound_resource_unavailable", "The Codex Voice call subscription resource is unavailable")
		}
		credentials, err := s.store.RefreshProviderResourceCredentials(ctx, resource.ID, false)
		if err != nil {
			return codexVoiceSelection{}, AdapterSessionBinding{}, err
		}
		if strings.TrimSpace(credentials.AccessToken) == "" || strings.TrimSpace(credentials.AccountID) == "" {
			return codexVoiceSelection{}, AdapterSessionBinding{}, NewHTTPError(http.StatusServiceUnavailable, "codex_voice_credentials_incomplete", "Codex Voice resource credentials are incomplete")
		}
		return codexVoiceSelection{Provider: provider, Resource: resource, Credentials: credentials}, binding, nil
	}
	return codexVoiceSelection{}, AdapterSessionBinding{}, NewHTTPError(http.StatusNotFound, "codex_voice_call_not_found", "Codex Voice call was not found")
}

func (s *Server) touchCodexVoiceBinding(ctx context.Context, binding AdapterSessionBinding) {
	_, _, _ = s.store.CommitAdapterSessionBinding(ctx, binding, binding.Generation)
}

func (s *Server) codexVoiceSidebandURL(callID string, frameless bool) (*url.URL, error) {
	base := strings.TrimSpace(s.codexVoiceSidebandUpstreamBaseURL)
	if base == "" {
		base = codexVoiceDefaultSidebandUpstreamBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, NewHTTPError(http.StatusBadGateway, "codex_voice_sideband_upstream_invalid", "Codex Voice sideband upstream URL is invalid")
	}
	if frameless {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/live/" + url.PathEscape(callID)
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/realtime"
		query := parsed.Query()
		query.Set("intent", "quicksilver")
		query.Set("call_id", callID)
		parsed.RawQuery = query.Encode()
	}
	return parsed, nil
}

func (s *Server) proxyCodexVoiceWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL, selection codexVoiceSelection) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			upgrade := request.In.Header.Get("Upgrade")
			outboundURL := *target
			request.Out.URL = &outboundURL
			request.Out.Host = target.Host
			headers, err := codexVoiceForwardHeaders(request.In.Header)
			if err != nil {
				headers = make(http.Header)
			}
			request.Out.Header = headers
			if upgrade != "" {
				request.Out.Header.Set("Upgrade", upgrade)
				request.Out.Header.Set("Connection", "Upgrade")
			}
			request.Out.Header.Set("Authorization", "Bearer "+strings.TrimSpace(selection.Credentials.AccessToken))
			request.Out.Header.Set("ChatGPT-Account-ID", strings.TrimSpace(selection.Credentials.AccountID))
			setCodexVoiceIdentityFallbacks(request.Out.Header)
		},
		ModifyResponse: func(response *http.Response) error {
			stripCodexVoiceSensitiveResponseHeaders(response.Header, response.StatusCode == http.StatusSwitchingProtocols)
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			writeError(writer, request, NewHTTPError(http.StatusBadGateway, "codex_voice_sideband_failed", "Codex Voice sideband connection failed"))
		},
	}
	transport := http.DefaultTransport
	if s.codexSubscription != nil && s.codexSubscription.Client != nil && s.codexSubscription.Client.Transport != nil {
		transport = s.codexSubscription.Client.Transport
	}
	proxy.Transport = codexVoiceResponseFilteringTransport{next: transport}
	proxy.ServeHTTP(w, r)
	s.store.MarkProviderResourceUsed(selection.Resource.ID)
}
