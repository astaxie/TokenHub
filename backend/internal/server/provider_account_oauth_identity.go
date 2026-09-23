package server

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const providerResourceIdentityProfileOpenAIIDToken = "openai_account_id_token"

type openAIIDTokenClaims struct {
	Email      string            `json:"email"`
	OpenAIAuth *openAIAuthClaims `json:"https://api.openai.com/auth,omitempty"`
}

type openAIAuthClaims struct {
	ChatGPTAccountID string                    `json:"chatgpt_account_id"`
	ChatGPTUserID    string                    `json:"chatgpt_user_id"`
	ChatGPTPlanType  string                    `json:"chatgpt_plan_type"`
	UserID           string                    `json:"user_id"`
	Organizations    []openAIOrganizationClaim `json:"organizations"`
}

type openAIOrganizationClaim struct {
	ID        string `json:"id"`
	IsDefault bool   `json:"is_default"`
}

func (a *CodexSubscriptionAdapter) ProviderResourceCredentialIdentityProfiles() []providerResourceCredentialIdentityRegistration {
	if a == nil {
		return nil
	}
	return []providerResourceCredentialIdentityRegistration{{
		ProviderType: ProviderOpenAICodex,
		Profile:      providerResourceIdentityProfileOpenAIIDToken,
		Resolve:      applyOpenAIIDTokenClaims,
	}}
}

func applyOpenAIIDTokenClaims(creds ProviderResourceCredentials) ProviderResourceCredentials {
	claims := decodeOpenAIIDTokenClaims(creds.IDToken)
	if claims == nil {
		return creds
	}
	creds.Email = firstNonEmpty(creds.Email, claims.Email)
	if claims.OpenAIAuth != nil {
		creds.AccountID = firstNonEmpty(creds.AccountID, claims.OpenAIAuth.ChatGPTAccountID)
		creds.UserID = firstNonEmpty(creds.UserID, claims.OpenAIAuth.UserID, claims.OpenAIAuth.ChatGPTUserID)
		creds.PlanType = firstNonEmpty(creds.PlanType, claims.OpenAIAuth.ChatGPTPlanType)
		creds.OrganizationID = firstNonEmpty(creds.OrganizationID, defaultOpenAIOrganizationID(claims.OpenAIAuth.Organizations))
	}
	return creds
}

func decodeOpenAIIDTokenClaims(idToken string) *openAIIDTokenClaims {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	if padding := len(payload) % 4; padding != 0 {
		payload += strings.Repeat("=", 4-padding)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil
	}
	var claims openAIIDTokenClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	return &claims
}

func defaultOpenAIOrganizationID(organizations []openAIOrganizationClaim) string {
	if len(organizations) == 0 {
		return ""
	}
	for _, organization := range organizations {
		if organization.IsDefault && strings.TrimSpace(organization.ID) != "" {
			return strings.TrimSpace(organization.ID)
		}
	}
	return strings.TrimSpace(organizations[0].ID)
}
