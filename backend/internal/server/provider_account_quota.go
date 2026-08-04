package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	openAIAccountQuotaURL     = "https://chatgpt.com/backend-api/wham/usage"
	openAIAccountQuotaBeta    = "codex-1"
	openAIAccountQuotaTimeout = 20 * time.Second
	openAIAccountQuotaTTL     = 60 * time.Second
)

type OpenAIAccountQuotaWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type OpenAIAccountRateLimit struct {
	Allowed         bool                      `json:"allowed"`
	LimitReached    bool                      `json:"limit_reached"`
	PrimaryWindow   *OpenAIAccountQuotaWindow `json:"primary_window,omitempty"`
	SecondaryWindow *OpenAIAccountQuotaWindow `json:"secondary_window,omitempty"`
}

type OpenAIAccountAdditionalRateLimit struct {
	LimitName      string                  `json:"limit_name"`
	MeteredFeature string                  `json:"metered_feature"`
	RateLimit      *OpenAIAccountRateLimit `json:"rate_limit,omitempty"`
}

type OpenAIAccountRateLimitResetCredits struct {
	AvailableCount int `json:"available_count"`
}

type OpenAIAccountQuota struct {
	UserID                string                              `json:"user_id,omitempty"`
	AccountID             string                              `json:"account_id,omitempty"`
	Email                 string                              `json:"email,omitempty"`
	PlanType              string                              `json:"plan_type,omitempty"`
	RateLimit             *OpenAIAccountRateLimit             `json:"rate_limit,omitempty"`
	AdditionalRateLimits  []OpenAIAccountAdditionalRateLimit  `json:"additional_rate_limits,omitempty"`
	RateLimitResetCredits *OpenAIAccountRateLimitResetCredits `json:"rate_limit_reset_credits,omitempty"`
	FetchedAt             int64                               `json:"fetched_at"`
}

func (s *Server) queryOpenAIAccountQuota(ctx context.Context, resourceID string) (OpenAIAccountQuota, error) {
	return s.queryOpenAIAccountQuotaCached(ctx, resourceID, false)
}

func (s *Server) queryOpenAIAccountQuotaCached(ctx context.Context, resourceID string, force bool) (OpenAIAccountQuota, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return OpenAIAccountQuota{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return OpenAIAccountQuota{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	descriptor, supported := s.adapterRegistry.Describe(provider.Type)
	if !supported || !adapterSupports(descriptor, AdapterCapabilityQuota) {
		return OpenAIAccountQuota{}, NewHTTPError(http.StatusBadRequest, "provider_resource_quota_unsupported", "Quota is only available for OpenAI subscription resources")
	}
	if !force {
		if quota, ok := s.cachedOpenAIAccountQuota(resourceID, openAIAccountQuotaTTL); ok {
			return quota, nil
		}
	}

	var quota OpenAIAccountQuota
	err := s.store.RunClusterOperation(ctx, "provider-quota:"+resourceID, func(leaseCtx context.Context) error {
		if !force {
			if cached, cachedOK := s.cachedOpenAIAccountQuota(resourceID, openAIAccountQuotaTTL); cachedOK {
				quota = cached
				return nil
			}
		}
		startedAt := time.Now()
		fetched, fetchErr := s.fetchOpenAIAccountQuota(leaseCtx, resourceID)
		if fetchErr != nil {
			s.store.RecordProviderObservation(ProviderObservation{
				ProviderID:  provider.ID,
				ResourceID:  resourceID,
				AdapterType: provider.Type,
				Source:      "quota",
				Operation:   "quota",
				Success:     false,
				LatencyMS:   time.Since(startedAt).Milliseconds(),
				ErrorCode:   AsHTTPError(fetchErr).Code,
			})
			return fetchErr
		}
		encoded, encodeErr := json.Marshal(fetched)
		if encodeErr != nil {
			return encodeErr
		}
		fetchedAt := time.Unix(fetched.FetchedAt, 0).UTC()
		if saveErr := s.store.SaveProviderResourceQuota(resourceID, provider.Type, string(encoded), fetchedAt); saveErr != nil {
			return saveErr
		}
		s.store.RecordProviderObservation(ProviderObservation{
			ProviderID:  provider.ID,
			ResourceID:  resourceID,
			AdapterType: provider.Type,
			Source:      "quota",
			Operation:   "quota",
			Success:     true,
			LatencyMS:   time.Since(startedAt).Milliseconds(),
		})
		quota = fetched
		return nil
	})
	if err != nil {
		if AsHTTPError(err).Status >= http.StatusInternalServerError {
			if stale, staleOK := s.cachedOpenAIAccountQuota(resourceID, 0); staleOK {
				return stale, nil
			}
		}
		return OpenAIAccountQuota{}, err
	}
	return quota, nil
}

func (s *Server) cachedOpenAIAccountQuota(resourceID string, ttl time.Duration) (OpenAIAccountQuota, bool) {
	observation, ok := s.store.GetProviderResourceObservation(resourceID)
	if !ok || observation.QuotaFetchedAt == nil || strings.TrimSpace(observation.QuotaSnapshot) == "" {
		return OpenAIAccountQuota{}, false
	}
	if ttl > 0 && time.Since(observation.QuotaFetchedAt.UTC()) > ttl {
		return OpenAIAccountQuota{}, false
	}
	var quota OpenAIAccountQuota
	if json.Unmarshal([]byte(observation.QuotaSnapshot), &quota) != nil {
		return OpenAIAccountQuota{}, false
	}
	return quota, true
}

func (s *Server) fetchOpenAIAccountQuota(ctx context.Context, resourceID string) (OpenAIAccountQuota, error) {
	creds, err := s.store.RefreshProviderResourceCredentials(ctx, resourceID, false)
	if err != nil {
		return OpenAIAccountQuota{}, err
	}
	quotaURL := firstNonEmpty(s.codexSubscription.QuotaURL, openAIAccountQuotaURL)
	quota, status, err := fetchOpenAIAccountQuotaWithClient(ctx, s.codexSubscription.Client, quotaURL, creds)
	if status != http.StatusUnauthorized {
		return quota, err
	}

	refreshed, refreshErr := s.store.RefreshProviderResourceCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return OpenAIAccountQuota{}, refreshErr
	}
	quota, _, err = fetchOpenAIAccountQuotaWithClient(ctx, s.codexSubscription.Client, quotaURL, refreshed)
	return quota, err
}

func (s *Server) providerResourceByID(resourceID string) (ProviderResource, bool) {
	for _, resource := range s.store.ListProviderResources() {
		if resource.ID == resourceID {
			return resource, true
		}
	}
	return ProviderResource{}, false
}

func fetchOpenAIAccountQuotaWithClient(ctx context.Context, client *http.Client, endpoint string, creds ProviderResourceCredentials) (OpenAIAccountQuota, int, error) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	accountID := strings.TrimSpace(creds.AccountID)
	if accessToken == "" {
		return OpenAIAccountQuota{}, 0, NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	if accountID == "" {
		return OpenAIAccountQuota{}, 0, NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}

	callCtx, cancel := context.WithTimeout(ctx, openAIAccountQuotaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OpenAIAccountQuota{}, 0, NewHTTPError(http.StatusBadGateway, "openai_quota_request_failed", "Failed to create OpenAI quota request")
	}
	setOpenAIAccountQuotaHeaders(req, accessToken, accountID)

	if client == nil {
		client = &http.Client{Timeout: openAIAccountQuotaTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return OpenAIAccountQuota{}, 0, NewHTTPError(http.StatusBadGateway, "openai_quota_request_failed", fmt.Sprintf("OpenAI quota request failed: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OpenAIAccountQuota{}, resp.StatusCode, openAIQuotaUpstreamError(resp.StatusCode, resp.Body)
	}
	var quota OpenAIAccountQuota
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&quota); err != nil {
		return OpenAIAccountQuota{}, resp.StatusCode, NewHTTPError(http.StatusBadGateway, "openai_quota_invalid_response", "OpenAI quota endpoint returned an invalid response")
	}
	quota.FetchedAt = time.Now().UTC().Unix()
	return quota, resp.StatusCode, nil
}

func setOpenAIAccountQuotaHeaders(req *http.Request, accessToken string, accountID string) {
	req.Host = req.URL.Host
	req.Header.Set("authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("openai-beta", openAIAccountQuotaBeta)
	req.Header.Set("oai-language", "zh-CN")
	req.Header.Set("originator", "Codex Desktop")
	req.Header.Set("accept", "application/json")
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("priority", "u=4, i")
}

func openAIQuotaUpstreamError(status int, body io.Reader) error {
	message := "OpenAI quota endpoint rejected the request"
	if _, _, upstreamMessage := openAIQuotaUpstreamErrorDetails(body); upstreamMessage != "" {
		message = upstreamMessage
	}
	switch status {
	case http.StatusUnauthorized:
		return NewHTTPError(http.StatusBadGateway, "openai_quota_unauthorized", message)
	case http.StatusForbidden:
		return NewHTTPError(http.StatusBadGateway, "openai_quota_forbidden", message)
	case http.StatusTooManyRequests:
		return NewHTTPError(http.StatusTooManyRequests, "openai_quota_rate_limited", message)
	default:
		return NewHTTPError(http.StatusBadGateway, "openai_quota_upstream_error", fmt.Sprintf("OpenAI quota endpoint returned %d: %s", status, message))
	}
}
