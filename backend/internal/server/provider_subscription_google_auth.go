package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *GoogleSubscriptionAdapter) ProviderResourceCredentialRefreshHandlers() []providerResourceCredentialRefreshRegistration {
	return []providerResourceCredentialRefreshRegistration{{ProviderType: a.ProviderType, Profile: a.ProviderType + "_oauth", RefreshLead: 2 * time.Minute, Refresh: a.refresh}}
}

func (a *GoogleSubscriptionAdapter) refresh(ctx context.Context, current ProviderResourceCredentials) (ProviderResourceCredentials, error) {
	if strings.TrimSpace(current.RefreshToken) == "" {
		return current, NewHTTPError(400, "provider_resource_refresh_token_missing", "Google subscription refresh token is missing")
	}
	clientID, clientSecret := strings.TrimSpace(current.ClientID), strings.TrimSpace(current.ClientSecret)
	if !googleSubscriptionClientCredentialsValid(clientID, clientSecret) {
		return current, googleSubscriptionClientCredentialsRequired()
	}
	if a.Client == nil {
		return current, NewHTTPError(503, "google_subscription_client_unavailable", "Google subscription HTTP client is unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken}, "client_id": {clientID}, "client_secret": {clientSecret}}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, firstNonEmpty(a.TokenURL, googleSubscriptionTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return current, NewHTTPError(500, "google_subscription_token_url_invalid", "Google subscription token endpoint is invalid")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := subscriptionClient(a.Client).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return current, ctx.Err()
		}
		if guarded := providerEgressFailure(err); guarded != nil {
			return current, guarded
		}
		return current, NewHTTPError(502, "google_subscription_refresh_failed", "Google subscription credential refresh failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if response.StatusCode == http.StatusUnauthorized || (response.StatusCode == http.StatusBadRequest && failure.Error == "invalid_grant") {
			return current, subscriptionCredentialError(NewHTTPError(409, "provider_resource_reauthorization_required", "Google subscription authorization expired. Import renewed credentials."))
		}
		return current, NewHTTPError(502, "google_subscription_refresh_failed", "Google subscription credential refresh failed")
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &token) != nil || strings.TrimSpace(token.AccessToken) == "" || token.ExpiresIn <= 0 || token.ExpiresIn > 31*86400 {
		return current, NewHTTPError(502, "google_subscription_token_invalid", "Google subscription token response is invalid")
	}
	current.AccessToken = token.AccessToken
	current.RefreshToken = firstNonEmpty(token.RefreshToken, current.RefreshToken)
	current.IDToken = firstNonEmpty(token.IDToken, current.IDToken)
	current.ClientID = clientID
	current.TokenType = firstNonEmpty(token.TokenType, "Bearer")
	current.Scopes = firstNonEmpty(token.Scope, current.Scopes)
	current.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	return current, nil
}

type googleSubscriptionProject struct {
	ID       string
	Tier     string
	PaidTier string
}
type googleSubscriptionProjectID string

func (p *googleSubscriptionProjectID) UnmarshalJSON(data []byte) error {
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		*p = googleSubscriptionProjectID(plain)
		return nil
	}
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	*p = googleSubscriptionProjectID(object.ID)
	return nil
}

type googleSubscriptionTier struct {
	ID                  string `json:"id"`
	IsDefault           bool   `json:"isDefault"`
	UserProjectRequired bool   `json:"userDefinedCloudaicompanionProject"`
}

func (a *GoogleSubscriptionAdapter) project(ctx context.Context, p Provider, creds ProviderResourceCredentials) (googleSubscriptionProject, error) {
	key := a.accountCacheKey(p, creds, creds.OrganizationID)
	if a.accountState != nil {
		a.accountState.mu.Lock()
		cached, ok := a.accountState.projects[key]
		a.accountState.mu.Unlock()
		if ok && time.Since(cached.at) < time.Hour {
			return cached.value, nil
		}
	}
	project, err := a.loadProject(ctx, p, creds)
	if err == nil && a.accountState != nil {
		a.accountState.mu.Lock()
		if a.accountState.projects == nil || len(a.accountState.projects) >= 256 {
			a.accountState.projects = make(map[[32]byte]googleSubscriptionCachedProject)
		}
		a.accountState.projects[key] = googleSubscriptionCachedProject{value: project, at: time.Now()}
		a.accountState.mu.Unlock()
	}
	return project, err
}

func (a *GoogleSubscriptionAdapter) loadProject(ctx context.Context, p Provider, creds ProviderResourceCredentials) (googleSubscriptionProject, error) {
	wanted := strings.TrimSpace(creds.OrganizationID)
	base := firstNonEmpty(a.LoadURL, googleSubscriptionProdURL)
	// A configured upstream applies to bootstrap too; production defaults keep
	// Antigravity's production bootstrap separate from its daily inference host.
	if p.BaseURL != "" && strings.TrimRight(p.BaseURL, "/") != a.baseURL() {
		base = a.inferenceBase(p)
	}
	if a.LoadURL != "" {
		base = a.LoadURL
	}
	metadata := map[string]any{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	if a.antigravity() {
		metadata = map[string]any{"ideType": "ANTIGRAVITY"}
	}
	loadRequest := map[string]any{"metadata": metadata}
	if !a.antigravity() && wanted != "" {
		metadata["duetProject"] = wanted
		loadRequest["cloudaicompanionProject"] = wanted
	}
	var loaded struct {
		Project      googleSubscriptionProjectID `json:"cloudaicompanionProject"`
		CurrentTier  *googleSubscriptionTier     `json:"currentTier"`
		PaidTier     *googleSubscriptionTier     `json:"paidTier"`
		AllowedTiers []googleSubscriptionTier    `json:"allowedTiers"`
	}
	if err := a.call(ctx, base, "loadCodeAssist", creds, loadRequest, &loaded); err != nil {
		if a.antigravity() && wanted != "" && ctx.Err() == nil {
			return googleSubscriptionProject{ID: wanted}, nil
		}
		return googleSubscriptionProject{}, err
	}
	project := googleSubscriptionProject{ID: string(loaded.Project)}
	if loaded.CurrentTier != nil {
		project.Tier = loaded.CurrentTier.ID
	}
	if loaded.PaidTier != nil {
		project.PaidTier = loaded.PaidTier.ID
	}
	if a.antigravity() {
		project.ID = firstNonEmpty(wanted, project.ID)
		if project.ID != "" {
			return project, nil
		}
	} else if loaded.CurrentTier != nil {
		project.ID = firstNonEmpty(project.ID, wanted)
		if project.ID == "" {
			return project, googleSubscriptionProjectRequired()
		}
		return project, nil
	}
	tier := googleSubscriptionTier{ID: "legacy-tier", UserProjectRequired: true}
	for i, candidate := range loaded.AllowedTiers {
		if i == 0 || candidate.IsDefault {
			tier = candidate
		}
		if candidate.IsDefault {
			break
		}
	}
	if !a.antigravity() && tier.UserProjectRequired && wanted == "" {
		return project, googleSubscriptionProjectRequired()
	}
	request := map[string]any{"tierId": tier.ID, "metadata": metadata}
	if !a.antigravity() && tier.ID != "free-tier" {
		request["cloudaicompanionProject"] = wanted
	}
	onboardBase := base
	if a.antigravity() {
		if tier.ID == "" || tier.ID == "legacy-tier" {
			tier.ID = "free-tier"
		}
		request = map[string]any{"tier_id": tier.ID, "metadata": map[string]any{"ide_type": "ANTIGRAVITY", "ide_name": "antigravity", "ide_version": "2.9.1"}}
		onboardBase = a.inferenceBase(p)
	}
	poll := a.OnboardPoll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for attempt := 0; attempt < 5; attempt++ {
		var operation struct {
			Done     bool `json:"done"`
			Response struct {
				Project googleSubscriptionProjectID `json:"cloudaicompanionProject"`
			} `json:"response"`
			Error json.RawMessage `json:"error"`
		}
		if err := a.call(ctx, onboardBase, "onboardUser", creds, request, &operation); err != nil {
			return project, err
		}
		if len(operation.Error) > 0 && string(operation.Error) != "null" {
			return project, NewHTTPError(502, "google_subscription_onboarding_failed", "Google subscription account setup failed")
		}
		if operation.Done {
			project.ID = string(operation.Response.Project)
			if !a.antigravity() {
				project.ID = firstNonEmpty(project.ID, wanted)
			}
			project.Tier = tier.ID
			if project.ID == "" {
				return project, googleSubscriptionProjectRequired()
			}
			return project, nil
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return project, ctx.Err()
		case <-timer.C:
		}
	}
	return project, NewHTTPError(503, "google_subscription_onboarding_pending", "Google subscription account setup is still pending. Try again later.")
}

func googleSubscriptionProjectRequired() error {
	return NewHTTPError(400, "google_subscription_project_required", "Google Code Assist requires a Cloud project. Set the account organization ID to its Google Cloud project ID.")
}
