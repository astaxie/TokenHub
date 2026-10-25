package server

// Code Assist protocol reference: github.com/yetone/magpie (MIT), copyright
// (c) 2026 yetone. This adapter uses TokenHub's own credential store and codecs.
//
// MIT License
//
// Copyright (c) 2026 yetone
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const (
	providerGeminiCLISubscription   = "gemini_cli_subscription"
	providerAntigravitySubscription = "antigravity_subscription"
	googleSubscriptionProdURL       = "https://cloudcode-pa.googleapis.com"
	googleSubscriptionDailyURL      = "https://daily-cloudcode-pa.googleapis.com"
	googleSubscriptionTokenURL      = "https://oauth2.googleapis.com/token"
	googleSubscriptionBodyLimit     = 4 << 20
)

// GoogleSubscriptionAdapter implements Code Assist's OAuth-only envelope API.
// OrganizationID is the optional Google Cloud project ID on the account.
// Clients are supplied by the host so all requests retain its SSRF policy.
type GoogleSubscriptionAdapter struct {
	ProviderType       string
	Client             *http.Client
	StreamClient       *http.Client
	StreamIdleTimeout  time.Duration
	BaseURL            string
	LoadURL            string
	TokenURL           string
	UserInfoURL        string
	OnboardPoll        time.Duration
	RefreshCredentials func(context.Context, string, bool) (ProviderResourceCredentials, error)
}

func newGoogleSubscriptionAdapter(providerType string, deps builtinProviderRuntimeDependencies) *GoogleSubscriptionAdapter {
	a := &GoogleSubscriptionAdapter{ProviderType: providerType, Client: deps.Client, StreamClient: deps.StreamClient, StreamIdleTimeout: deps.StreamIdleTimeout}
	if deps.Store != nil {
		a.RefreshCredentials = deps.Store.RefreshProviderResourceCredentials
	}
	return a
}

func googleSubscriptionRegistration(providerType string, adapter any) builtinProviderAdapter {
	a := GoogleSubscriptionAdapter{ProviderType: providerType}
	return builtinProviderAdapter{
		providerType: providerType, adapter: adapter, supportsCustomHeaders: boolPointer(false), apiKeyRequired: boolPointer(false),
		credentialsScope: providerCredentialsScopeResource, credentialRefreshProfile: providerType + "_oauth", routeRequiresResource: true,
		routeProtocols: []string{providerRouteProtocolChatCompletions},
		resourceTypes:  []pluginmeta.ManifestProviderResourceType{{Type: a.resourceType(), DisplayName: a.name() + " Account", AuthModes: []string{"oauth"}, Default: true, Defaults: map[string]string{"auth_type": "oauth", "base_url": a.baseURL(), "max_concurrency": "3"}}},
		catalogEntry:   &pluginProviderCatalogEntry{ID: a.catalogID(), Name: a.name(), DisplayName: a.name(), Type: providerType, BaseURL: a.baseURL(), DocURL: a.docURL(), Categories: []string{"gemini"}, Source: "google-code-assist-live", ModelsAccountRequiredErrorCode: "google_subscription_account_required", ModelsAccountRequiredErrorMessage: "Connect a Google subscription account before loading models"},
		capabilities:   []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityOAuth},
	}
}

func (a *GoogleSubscriptionAdapter) antigravity() bool {
	return a.ProviderType == providerAntigravitySubscription
}
func (a *GoogleSubscriptionAdapter) resourceType() string {
	if a.antigravity() {
		return "antigravity_account"
	}
	return "gemini_cli_account"
}
func (a *GoogleSubscriptionAdapter) catalogID() string {
	if a.antigravity() {
		return "antigravity"
	}
	return "gemini-cli"
}
func (a *GoogleSubscriptionAdapter) name() string {
	if a.antigravity() {
		return "Antigravity"
	}
	return "Gemini CLI"
}
func (a *GoogleSubscriptionAdapter) docURL() string {
	if a.antigravity() {
		return "https://antigravity.google"
	}
	return "https://github.com/google-gemini/gemini-cli"
}
func (a *GoogleSubscriptionAdapter) baseURL() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	if a.antigravity() {
		return googleSubscriptionDailyURL
	}
	return googleSubscriptionProdURL
}
func (a *GoogleSubscriptionAdapter) userAgent(model string) string {
	if a.antigravity() {
		return "antigravity/hub/2.9.1 " + runtime.GOOS + "/" + runtime.GOARCH
	}
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "windows" {
		platform = "win32"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	return "GeminiCLI/0.61.0/" + firstNonEmpty(model, "gemini-2.5-pro") + " (" + platform + "; " + arch + "; terminal)"
}

func (a *GoogleSubscriptionAdapter) credentials(ctx context.Context, p Provider, r *ProviderResource) (ProviderResourceCredentials, error) {
	id := p.Options["resource_id"]
	if r != nil {
		id = r.ID
	}
	if id != "" && a.RefreshCredentials != nil {
		creds, err := a.RefreshCredentials(ctx, id, false)
		if err == nil && (creds.AccessToken == "" || creds.ExpiresAt == "") && creds.RefreshToken != "" {
			return a.RefreshCredentials(ctx, id, true)
		}
		return creds, err
	}
	creds := ProviderResourceCredentials{AccessToken: p.APIKey, OrganizationID: p.Options["organization_id"]}
	if r != nil {
		if r.Credentials != nil {
			creds = *r.Credentials
		} else {
			creds.AccessToken = firstNonEmpty(r.APIKey, creds.AccessToken)
			creds.OrganizationID = firstNonEmpty(r.Options["organization_id"], creds.OrganizationID)
		}
	}
	return creds, nil
}

func (a *GoogleSubscriptionAdapter) Chat(ctx context.Context, p Provider, model string, req ChatCompletionRequest) (any, Usage, error) {
	payload, err := buildGeminiRequest(model, req)
	if err != nil {
		return nil, Usage{}, err
	}
	creds, err := a.credentials(ctx, p, nil)
	if err != nil {
		return nil, Usage{}, err
	}
	envelope, err := a.envelope(ctx, p, creds, model, payload)
	if err != nil {
		return nil, Usage{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response, err := a.request(callCtx, a.inferenceBase(p), "streamGenerateContent?alt=sse", creds, envelope, true)
	if err != nil {
		return nil, Usage{}, err
	}
	defer response.Body.Close()
	return collectGoogleSubscriptionChat(response.Body, req.Model)
}

func (a *GoogleSubscriptionAdapter) ChatStream(ctx context.Context, p Provider, model string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	payload, err := buildGeminiRequest(model, req)
	if err != nil {
		return Usage{}, err
	}
	creds, err := a.credentials(ctx, p, nil)
	if err != nil {
		return Usage{}, err
	}
	envelope, err := a.envelope(ctx, p, creds, model, payload)
	if err != nil {
		return Usage{}, err
	}
	response, err := a.request(ctx, a.inferenceBase(p), "streamGenerateContent?alt=sse", creds, envelope, true)
	if err != nil {
		return Usage{}, err
	}
	defer response.Body.Close()
	p.APIKey = creds.AccessToken
	return streamGeminiChatForProvider(&googleSubscriptionSSEReader{events: newSSEDecoder(response.Body)}, newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req)), p)
}

func (a *GoogleSubscriptionAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Google subscription accounts use chat completions")
}
func (a *GoogleSubscriptionAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, unsupportedCapabilityError("Google subscription accounts do not expose embeddings")
}

func (a *GoogleSubscriptionAdapter) inferenceBase(p Provider) string {
	return strings.TrimRight(firstNonEmpty(p.BaseURL, a.baseURL()), "/")
}
func (a *GoogleSubscriptionAdapter) envelope(ctx context.Context, p Provider, creds ProviderResourceCredentials, model string, payload map[string]any) (map[string]any, error) {
	project, err := a.project(ctx, p, creds)
	if err != nil {
		return nil, err
	}
	if err := a.preparePayload(model, payload); err != nil {
		return nil, err
	}
	body := map[string]any{"model": model, "project": project.ID, "request": payload}
	if a.antigravity() {
		body["userAgent"], body["requestType"], body["requestId"] = "antigravity", "agent", NewID("agent")
		payload["sessionId"] = googleSubscriptionSessionID(payload)
	} else {
		body["user_prompt_id"] = NewID("prompt")
	}
	return body, nil
}

func (a *GoogleSubscriptionAdapter) request(ctx context.Context, base, method string, creds ProviderResourceCredentials, payload any, stream bool) (*http.Response, error) {
	if strings.TrimSpace(creds.AccessToken) == "" {
		return nil, NewHTTPError(400, "google_subscription_token_missing", "Google subscription access token is missing")
	}
	allowed := false
	for _, endpoint := range []string{googleSubscriptionProdURL, googleSubscriptionDailyURL, a.BaseURL, a.LoadURL} {
		if endpoint != "" && strings.TrimRight(base, "/") == strings.TrimRight(endpoint, "/") {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, NewHTTPError(400, "google_subscription_endpoint_not_allowed", "Google subscription credentials cannot be sent to this endpoint")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1internal:"+method, bytes.NewReader(data))
	if err != nil {
		return nil, NewHTTPError(400, "google_subscription_url_invalid", "Google subscription endpoint is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	body, _ := payload.(map[string]any)
	model, _ := body["model"].(string)
	req.Header.Set("User-Agent", a.userAgent(model))
	if a.Client == nil {
		return nil, NewHTTPError(503, "google_subscription_client_unavailable", "Google subscription HTTP client is unavailable")
	}
	response, err := sendUpstream(subscriptionClient(a.Client), subscriptionClient(firstGoogleSubscriptionClient(a.StreamClient, a.Client)), a.StreamIdleTimeout, req, stream)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, NewHTTPError(502, "google_subscription_request_failed", "Google subscription request failed")
	}
	if err = checkProviderResponseForProvider(response, Provider{APIKey: creds.AccessToken}); err != nil {
		return nil, err
	}
	return response, nil
}

func (a *GoogleSubscriptionAdapter) call(ctx context.Context, base, method string, creds ProviderResourceCredentials, payload, target any) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := a.request(callCtx, base, method, creds, payload, false)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, googleSubscriptionBodyLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return NewHTTPError(502, "google_subscription_read_failed", "Google subscription response could not be read")
	}
	if len(data) > googleSubscriptionBodyLimit || json.Unmarshal(data, target) != nil {
		return NewHTTPError(502, "google_subscription_invalid_response", "Google subscription returned an invalid response")
	}
	return nil
}

func googleSubscriptionUnwrap(body map[string]any) (map[string]any, error) {
	if _, ok := body["error"]; ok {
		return nil, NewHTTPError(502, "google_subscription_upstream_error", "Google subscription reported an upstream error")
	}
	inner, ok := body["response"].(map[string]any)
	if !ok {
		return nil, NewHTTPError(502, "google_subscription_invalid_response", "Google subscription response envelope is missing")
	}
	if _, ok = inner["error"]; ok {
		return nil, NewHTTPError(502, "google_subscription_upstream_error", "Google subscription reported an upstream error")
	}
	return inner, nil
}

type googleSubscriptionSSEReader struct {
	events  *sseDecoder
	pending []byte
}

func (r *googleSubscriptionSSEReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		event, err := r.events.Next()
		if err != nil {
			return 0, err
		}
		payload, err := decodeSSEData(event)
		if err != nil {
			return 0, err
		}
		if payload == nil {
			continue
		}
		inner, err := googleSubscriptionUnwrap(payload)
		if err != nil {
			return 0, err
		}
		data, err := json.Marshal(inner)
		if err != nil {
			return 0, err
		}
		r.pending = append(append([]byte("data: "), data...), '\n', '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func firstGoogleSubscriptionClient(stream, standard *http.Client) *http.Client {
	if stream != nil {
		return stream
	}
	return standard
}
