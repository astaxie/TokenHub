package tokenhub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tokenhub/backend/internal/server"
)

const maxAdminAPIResponseBytes = 8 << 20 // 8 MiB

type AdminAPIClient struct {
	baseURL    string
	parsedBase *url.URL
	token      string
	httpClient *http.Client
}

// ApprovalRequiredError reports that an Admin API write created a pending
// approval request instead of applying the requested mutation.
type ApprovalRequiredError struct {
	Method   string
	Endpoint string
	Approval json.RawMessage
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("admin api %s %s requires approval", e.Method, e.Endpoint)
}

// NewAdminAPIClient validates the base URL eagerly so a malformed value — for
// example a user-supplied --to flag — surfaces as a controlled error at
// construction time instead of panicking on first request. A nil httpClient
// receives a client with an explicit total timeout; callers that pass a
// client must configure their own timeout.
func NewAdminAPIClient(baseURL string, token string, httpClient *http.Client) (*AdminAPIClient, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	base, err := url.Parse(baseURL)
	if err != nil || base == nil || base.Scheme == "" || base.Host == "" {
		if err == nil {
			err = fmt.Errorf("base url %q must include a scheme and host", baseURL)
		}
		return nil, fmt.Errorf("invalid admin api base url %q: %w", baseURL, err)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &AdminAPIClient{
		baseURL:    baseURL,
		parsedBase: base,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *AdminAPIClient) endpoint(parts ...string) string {
	base := new(url.URL)
	*base = *c.parsedBase
	encoded := make([]string, 0, len(parts)+8)
	for _, segment := range strings.Split(strings.Trim(base.Path, "/"), "/") {
		if strings.TrimSpace(segment) != "" {
			encoded = append(encoded, url.PathEscape(segment))
		}
	}
	if len(parts) > 0 {
		// First part is the API path — split by "/" and escape each segment.
		for _, segment := range strings.Split(strings.Trim(parts[0], "/"), "/") {
			if segment == "" {
				continue
			}
			encoded = append(encoded, url.PathEscape(segment))
		}
		// Subsequent parts are resource IDs or sub-resource names.
		// Escape each as a whole segment to preserve "/" within IDs.
		for _, part := range parts[1:] {
			part = strings.TrimSpace(part)
			if part != "" {
				encoded = append(encoded, url.PathEscape(part))
			}
		}
	}
	base.RawPath = "/" + strings.Join(encoded, "/")
	base.Path, _ = url.PathUnescape(base.RawPath)
	return base.String()
}

func (c *AdminAPIClient) doJSON(ctx context.Context, method string, endpoint string, reqBody any, out any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.token) != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAdminAPIResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(responseBody)) > maxAdminAPIResponseBytes {
		return fmt.Errorf("admin api %s %s response body exceeds %d bytes", method, endpoint, maxAdminAPIResponseBytes)
	}
	if response.StatusCode >= 400 {
		return fmt.Errorf("admin api %s %s failed: status=%d body=%s", method, endpoint, response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if response.StatusCode == http.StatusAccepted {
		var pending struct {
			ApprovalRequired bool            `json:"approval_required"`
			Approval         json.RawMessage `json:"approval"`
		}
		if err := json.Unmarshal(responseBody, &pending); err == nil && pending.ApprovalRequired {
			return &ApprovalRequiredError{
				Method:   method,
				Endpoint: endpoint,
				Approval: pending.Approval,
			}
		}
	}
	if out == nil || len(responseBody) == 0 {
		return nil
	}
	return json.Unmarshal(responseBody, out)
}

type listResponse[T any] struct {
	Data []T `json:"data"`
}

type providerCreateResult struct {
	Provider server.Provider `json:"provider"`
}

// providerWriteRequest is the Admin API request payload for creating or
// updating a provider. server.Provider cannot be marshalled directly because
// its APIKey field is tagged `json:"-"`, which would silently drop the
// credential resolved by the sink. This DTO mirrors the server-side
// ProviderCreateRequest and carries api_key explicitly.
type providerWriteRequest struct {
	ID               string            `json:"id,omitempty"`
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	BaseURL          string            `json:"base_url,omitempty"`
	APIKey           string            `json:"api_key,omitempty"`
	Status           string            `json:"status,omitempty"`
	Healthy          *bool             `json:"healthy,omitempty"`
	Priority         int               `json:"priority"`
	Headers          map[string]string `json:"headers,omitempty"`
	SensitiveHeaders []string          `json:"sensitive_headers,omitempty"`
	Options          map[string]string `json:"options,omitempty"`
	CreateRoutes     *bool             `json:"create_routes,omitempty"`
}

func providerWriteRequestFrom(p server.Provider) providerWriteRequest {
	healthy := p.Healthy
	options := migrationProviderOptions(p.Options)
	// Routes are migrated as first-class bundle items, so the server must
	// not auto-create catalog routes as a side effect of provider writes.
	createRoutes := false
	return providerWriteRequest{
		ID:               p.ID,
		Name:             p.Name,
		Type:             p.Type,
		BaseURL:          p.BaseURL,
		APIKey:           p.APIKey,
		Status:           p.Status,
		Healthy:          &healthy,
		Priority:         p.Priority,
		Headers:          p.Headers,
		SensitiveHeaders: p.SensitiveHeaders,
		Options:          options,
		CreateRoutes:     &createRoutes,
	}
}

// migrationProviderOptions preserves the behavior of Providers that predate
// the attribution setting. The regular create API may choose a cache-friendly
// default for a new third-party Provider, while a migration must retain the
// source Provider's legacy preserve behavior when the option is absent.
func migrationProviderOptions(options map[string]string) map[string]string {
	next := make(map[string]string, len(options)+1)
	for key, value := range options {
		next[key] = value
	}
	if _, configured := next["claude_code_attribution_policy"]; !configured {
		next["claude_code_attribution_policy"] = "preserve"
	}
	return next
}

type apiKeyCreateResult struct {
	ID                   string `json:"id"`
	APIKey               string `json:"api_key"`
	Name                 string `json:"name"`
	ProjectID            string `json:"project_id"`
	PlainTextVisibleOnce bool   `json:"plain_text_visible_once"`
}

func (c *AdminAPIClient) ListProviders(ctx context.Context) ([]server.Provider, error) {
	var resp listResponse[server.Provider]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/providers"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateProvider(ctx context.Context, req server.Provider) (server.Provider, error) {
	var resp providerCreateResult
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/providers"), providerWriteRequestFrom(req), &resp); err != nil {
		return server.Provider{}, err
	}
	return resp.Provider, nil
}

func (c *AdminAPIClient) UpdateProvider(ctx context.Context, id string, req server.Provider) (server.Provider, error) {
	var resp providerCreateResult
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/providers", id), providerWriteRequestFrom(req), &resp); err != nil {
		return server.Provider{}, err
	}
	if resp.Provider.ID != "" {
		return resp.Provider, nil
	}
	return req, nil
}

func (c *AdminAPIClient) DeleteProvider(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/providers", id), nil, nil)
}

func (c *AdminAPIClient) ListProviderResources(ctx context.Context) ([]server.ProviderResource, error) {
	var resp listResponse[server.ProviderResource]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/provider-resources"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateProviderResource(ctx context.Context, req server.ProviderResource) (server.ProviderResource, error) {
	var resp server.ProviderResource
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/provider-resources"), req, &resp); err != nil {
		return server.ProviderResource{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) UpdateProviderResource(ctx context.Context, id string, req server.ProviderResource) (server.ProviderResource, error) {
	var resp server.ProviderResource
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/provider-resources", id), req, &resp); err != nil {
		return server.ProviderResource{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteProviderResource(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/provider-resources", id), nil, nil)
}

func (c *AdminAPIClient) ListModels(ctx context.Context) ([]server.Model, error) {
	var resp listResponse[server.Model]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/models"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateModel(ctx context.Context, req server.Model) (server.Model, error) {
	var resp server.Model
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/models"), req, &resp); err != nil {
		return server.Model{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) UpdateModel(ctx context.Context, name string, req server.Model) (server.Model, error) {
	var resp server.Model
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/models", name), req, &resp); err != nil {
		return server.Model{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteModel(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/models", name), nil, nil)
}

func (c *AdminAPIClient) ListRoutes(ctx context.Context) ([]server.ModelRoute, error) {
	var resp listResponse[server.ModelRoute]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/routing-rules"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateRoute(ctx context.Context, req server.ModelRoute) (server.ModelRoute, error) {
	var resp server.ModelRoute
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/routing-rules"), req, &resp); err != nil {
		return server.ModelRoute{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) UpdateRoute(ctx context.Context, id string, req server.ModelRoute) (server.ModelRoute, error) {
	var resp server.ModelRoute
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/routing-rules", id), req, &resp); err != nil {
		return server.ModelRoute{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteRoute(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/routing-rules", id), nil, nil)
}

func (c *AdminAPIClient) ListUsers(ctx context.Context) ([]server.AdminUser, error) {
	var resp listResponse[server.AdminUser]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/users"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// adminUserWriteRequest mirrors the PATCH /api/admin/users/{id} payload.
// The server keeps current values for empty username/name/email/role/status,
// but team_id and team_ids are applied unconditionally: omitting them clears
// the user's team assignment, so callers must always send the full desired
// membership.
type adminUserWriteRequest struct {
	Username string   `json:"username,omitempty"`
	Name     string   `json:"name,omitempty"`
	Email    string   `json:"email,omitempty"`
	Role     string   `json:"role,omitempty"`
	TeamID   string   `json:"team_id,omitempty"`
	TeamIDs  []string `json:"team_ids,omitempty"`
	Status   string   `json:"status,omitempty"`
}

func (c *AdminAPIClient) UpdateAdminUser(ctx context.Context, id string, req server.AdminUser) (server.AdminUser, error) {
	payload := adminUserWriteRequest{
		Username: req.Username,
		Name:     req.Name,
		Email:    req.Email,
		Role:     req.Role,
		TeamID:   req.TeamID,
		TeamIDs:  req.TeamIDs,
		Status:   req.Status,
	}
	var resp server.AdminUser
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/users", id), payload, &resp); err != nil {
		return server.AdminUser{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteAdminUser(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/users", id), nil, nil)
}

func (c *AdminAPIClient) ImportUsersCSV(ctx context.Context, content string) error {
	payload := map[string]any{
		"source":  "migration",
		"format":  "csv",
		"content": content,
	}
	return c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/users/import"), payload, &map[string]any{})
}

func (c *AdminAPIClient) ListProjects(ctx context.Context) ([]server.Project, error) {
	var resp listResponse[server.Project]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/projects"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateProject(ctx context.Context, req server.Project) (server.Project, error) {
	var resp server.Project
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/projects"), req, &resp); err != nil {
		return server.Project{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) UpdateProject(ctx context.Context, id string, req server.Project) (server.Project, error) {
	var resp server.Project
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/projects", id), req, &resp); err != nil {
		return server.Project{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteProject(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/projects", id), nil, nil)
}

// ListProviderModels and ImportProviderModels cover the provider model
// inventory. A route can only be created once its upstream model is imported
// for that provider, so the sink has to populate the inventory first.
func (c *AdminAPIClient) ListProviderModels(ctx context.Context) ([]server.ProviderModel, error) {
	var resp listResponse[server.ProviderModel]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/provider-models"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) ImportProviderModels(ctx context.Context, providerID string, upstreamModels []string) ([]server.ProviderModel, error) {
	models := make([]server.ProviderCatalogModel, 0, len(upstreamModels))
	for _, name := range upstreamModels {
		// The server keys the imported inventory on ProviderCatalogModel.ID,
		// so the upstream model name belongs there rather than in Name.
		models = append(models, server.ProviderCatalogModel{ID: name, Name: name})
	}
	// Publish is rejected outright by the server: external models and routes
	// are created as first-class bundle items instead.
	req := server.ProviderModelImportRequest{ProviderID: providerID, Models: models, Publish: false}
	var resp server.ProviderModelImportResult
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/provider-models/import"), req, &resp); err != nil {
		return nil, err
	}
	return resp.ProviderModels, nil
}

// ListResources, CreateResource and DeleteResource cover the generic admin
// resource endpoint. The migration uses them for teams, which have no
// dedicated endpoint of their own.
func (c *AdminAPIClient) ListResources(ctx context.Context, kind string) ([]server.AdminResource, error) {
	var resp listResponse[server.AdminResource]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/resources", kind), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateResource(ctx context.Context, kind string, req server.AdminResource) (server.AdminResource, error) {
	var resp server.AdminResource
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/resources", kind), req, &resp); err != nil {
		return server.AdminResource{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteResource(ctx context.Context, kind string, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/resources", kind, id), nil, nil)
}

func (c *AdminAPIClient) ListAPIKeys(ctx context.Context) ([]server.APIKey, error) {
	var resp listResponse[server.APIKey]
	if err := c.doJSON(ctx, http.MethodGet, c.endpoint("/api/admin/api-keys"), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *AdminAPIClient) CreateProjectKey(ctx context.Context, projectID string, req map[string]any) (apiKeyCreateResult, error) {
	var resp apiKeyCreateResult
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/api/admin/projects", projectID, "keys"), req, &resp); err != nil {
		return apiKeyCreateResult{}, err
	}
	return resp, nil
}

// apiKeyWriteRequest mirrors the PATCH /api/admin/api-keys/{id} payload.
// The Admin API decodes limits into a pointer and treats a present object as
// an explicit assignment, so marshalling server.APIKey — whose Limits is a
// value field that always serializes — would clear the target's quota
// whenever the bundle carries none. Fields the bundle does not own are
// omitted so the server keeps its current values.
// The slice fields deliberately omit `omitempty`: a nil slice encodes as null
// and the server keeps its current value, while a non-nil empty slice encodes
// as [] and really clears the list. With omitempty both encode to nothing, so
// a bundle asking to clear allowed_models would report an update that changed
// nothing.
type apiKeyWriteRequest struct {
	Name            string              `json:"name,omitempty"`
	Group           string              `json:"group,omitempty"`
	AllowedModels   []string            `json:"allowed_models"`
	ModelAccessMode string              `json:"model_access_mode,omitempty"`
	IPAllowlist     []string            `json:"ip_allowlist"`
	Limits          *server.QuotaLimits `json:"limits,omitempty"`
	RateLimitRPM    *int64              `json:"rate_limit_rpm,omitempty"`
	TokenLimitTPM   *int64              `json:"token_limit_tpm,omitempty"`
	Status          string              `json:"status,omitempty"`
	ExpiresAt       *time.Time          `json:"expires_at,omitempty"`
}

func (c *AdminAPIClient) UpdateAPIKey(ctx context.Context, id string, req server.APIKey) (server.APIKey, error) {
	payload := apiKeyWriteRequest{
		Name:            req.Name,
		Group:           req.Group,
		AllowedModels:   req.Allowed,
		ModelAccessMode: req.ModelAccessMode,
		IPAllowlist:     req.IPAllowlist,
		RateLimitRPM:    req.RateLimitRPM,
		TokenLimitTPM:   req.TokenLimitTPM,
		Status:          req.Status,
		ExpiresAt:       req.ExpiresAt,
	}
	if req.Limits != (server.QuotaLimits{}) {
		limits := req.Limits
		payload.Limits = &limits
	}
	var resp server.APIKey
	if err := c.doJSON(ctx, http.MethodPatch, c.endpoint("/api/admin/api-keys", id), payload, &resp); err != nil {
		return server.APIKey{}, err
	}
	return resp, nil
}

func (c *AdminAPIClient) DeleteAPIKey(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.endpoint("/api/admin/api-keys", id), nil, nil)
}
