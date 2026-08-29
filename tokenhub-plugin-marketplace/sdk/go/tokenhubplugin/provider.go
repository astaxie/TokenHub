package tokenhubplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	OperationChat            = "chat"
	OperationChatStream      = "chat_stream"
	OperationResponses       = "responses"
	OperationResponsesStream = "responses_stream"
	OperationEmbeddings      = "embeddings"
	OperationModels          = "models"
	OperationProbe           = "probe"
)

type ProviderInvocation struct {
	Operation     string              `json:"operation"`
	Provider      ProviderProjection  `json:"provider"`
	Resource      *ResourceProjection `json:"resource,omitempty"`
	ProviderModel string              `json:"provider_model"`
	ETag          string              `json:"etag,omitempty"`
	Request       json.RawMessage     `json:"request"`
	Credentials   ProviderCredentials `json:"credentials,omitempty"`
}

type ProviderProjection struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Status   string `json:"status,omitempty"`
	Healthy  bool   `json:"healthy,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

type ResourceProjection struct {
	ID             string               `json:"id,omitempty"`
	ProviderID     string               `json:"provider_id,omitempty"`
	Name           string               `json:"name,omitempty"`
	Group          string               `json:"group,omitempty"`
	ResourceType   string               `json:"resource_type,omitempty"`
	BaseURL        string               `json:"base_url,omitempty"`
	Region         string               `json:"region,omitempty"`
	Environment    string               `json:"environment,omitempty"`
	Status         string               `json:"status,omitempty"`
	Healthy        bool                 `json:"healthy,omitempty"`
	Priority       int                  `json:"priority,omitempty"`
	Weight         int                  `json:"weight,omitempty"`
	RateLimitRPM   int64                `json:"rate_limit_rpm,omitempty"`
	TokenLimitTPM  int64                `json:"token_limit_tpm,omitempty"`
	MaxConcurrency int64                `json:"max_concurrency,omitempty"`
	Credentials    *ProviderCredentials `json:"credentials,omitempty"`
}

type ProviderCredentials struct {
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

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens int64 `json:"completion_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
	InputTokens      int64 `json:"input_tokens,omitempty"`
	OutputTokens     int64 `json:"output_tokens,omitempty"`
}

type StreamEvent struct {
	Event string `json:"event,omitempty"`
	Data  any    `json:"data"`
}

type ProviderResult struct {
	Response any           `json:"response,omitempty"`
	Events   []StreamEvent `json:"events,omitempty"`
	Usage    *Usage        `json:"usage,omitempty"`
	Status   int           `json:"status,omitempty"`
	Catalog  any           `json:"catalog,omitempty"`
	Result   any           `json:"result,omitempty"`
	Error    *PluginError  `json:"error,omitempty"`
}

type PluginError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ProviderHandler func(context.Context, ProviderInvocation) (ProviderResult, error)

func ServeProvider(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, handler ProviderHandler) int {
	if handler == nil {
		fmt.Fprintln(stderr, "provider handler is required")
		return 2
	}
	var invocation ProviderInvocation
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invocation); err != nil {
		fmt.Fprintf(stderr, "decode provider invocation: %v\n", err)
		return 2
	}
	if strings.TrimSpace(invocation.Operation) == "" {
		fmt.Fprintln(stderr, "provider operation is required")
		return 2
	}
	result, err := handler(ctx, invocation)
	if err != nil {
		fmt.Fprintf(stderr, "execute provider operation %q: %v\n", invocation.Operation, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode provider result: %v\n", err)
		return 1
	}
	return 0
}

func DecodeRequest[T any](invocation ProviderInvocation) (T, error) {
	var value T
	if len(invocation.Request) == 0 {
		return value, fmt.Errorf("request payload is required")
	}
	if err := json.Unmarshal(invocation.Request, &value); err != nil {
		return value, fmt.Errorf("decode request payload: %w", err)
	}
	return value, nil
}
