package plugin

import (
	"context"
	"strings"
	"time"
)

const defaultProviderCommandTimeout = 120 * time.Second

type ProviderCommandRunner struct {
	Dir     string
	Command string
	Timeout time.Duration
}

type ProviderCommandRequest struct {
	Operation     string                     `json:"operation"`
	Provider      ProviderCommandProvider    `json:"provider"`
	Resource      *ProviderCommandResource   `json:"resource,omitempty"`
	ProviderModel string                     `json:"provider_model"`
	ETag          string                     `json:"etag,omitempty"`
	Request       any                        `json:"request"`
	Credentials   ProviderCommandCredentials `json:"credentials,omitempty"`
}

type ProviderCommandProvider struct {
	ID        string    `json:"id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Type      string    `json:"type,omitempty"`
	BaseURL   string    `json:"base_url,omitempty"`
	Status    string    `json:"status,omitempty"`
	Healthy   bool      `json:"healthy,omitempty"`
	Priority  int       `json:"priority,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ProviderCommandResource struct {
	ID             string                      `json:"id,omitempty"`
	ProviderID     string                      `json:"provider_id,omitempty"`
	Name           string                      `json:"name,omitempty"`
	Group          string                      `json:"group,omitempty"`
	ResourceType   string                      `json:"resource_type,omitempty"`
	BaseURL        string                      `json:"base_url,omitempty"`
	Region         string                      `json:"region,omitempty"`
	Environment    string                      `json:"environment,omitempty"`
	Status         string                      `json:"status,omitempty"`
	Healthy        bool                        `json:"healthy,omitempty"`
	Priority       int                         `json:"priority,omitempty"`
	Weight         int                         `json:"weight,omitempty"`
	RateLimitRPM   int64                       `json:"rate_limit_rpm,omitempty"`
	TokenLimitTPM  int64                       `json:"token_limit_tpm,omitempty"`
	MaxConcurrency int64                       `json:"max_concurrency,omitempty"`
	Credentials    *ProviderCommandCredentials `json:"credentials,omitempty"`
	CreatedAt      time.Time                   `json:"created_at,omitempty"`
}

type ProviderCommandCredentials struct {
	AuthType       string `json:"auth_type,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	AccessToken    string `json:"access_token,omitempty"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	IDToken        string `json:"id_token,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	Scopes         string `json:"scopes,omitempty"`
	TokenType      string `json:"token_type,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	Email          string `json:"email,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	PlanType       string `json:"plan_type,omitempty"`
}

func NewProviderCommandRunner(dir string, command string) ProviderCommandRunner {
	return ProviderCommandRunner{
		Dir:     strings.TrimSpace(dir),
		Command: strings.TrimSpace(command),
		Timeout: defaultProviderCommandTimeout,
	}
}

func (r ProviderCommandRunner) ExecuteProviderCommand(ctx context.Context, invocation ProviderCommandRequest, output any) error {
	if r.Timeout <= 0 {
		r.Timeout = defaultProviderCommandTimeout
	}
	return RunCommandJSON(ctx, r.Dir, r.Command, r.Timeout, invocation, output)
}
