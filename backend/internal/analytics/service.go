package analytics

import (
	"context"
	"strings"
	"time"
)

// Service coordinates analytics use cases without exposing transport or
// database concerns to callers.
type Service struct {
	credentials CredentialStore
	tokenCosts  TokenCostStore
	now         func() time.Time
}

func NewService(credentials CredentialStore, tokenCosts TokenCostStore) *Service {
	return &Service{
		credentials: credentials,
		tokenCosts:  tokenCosts,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateCredential(ctx context.Context, credential Credential, rawSecret string) (Credential, string, error) {
	credential.Name = strings.TrimSpace(credential.Name)
	credential.ScopeType = strings.ToLower(strings.TrimSpace(credential.ScopeType))
	credential.ProjectID = strings.TrimSpace(credential.ProjectID)
	if err := ValidateCredential(credential, s.now()); err != nil {
		return Credential{}, "", err
	}
	if credential.ScopeType == ScopeOrganization {
		credential.ProjectID = ""
	}
	return s.credentials.CreateCredential(ctx, credential, rawSecret)
}

func (s *Service) ListCredentials(ctx context.Context) ([]Credential, error) {
	return s.credentials.ListCredentials(ctx)
}

func (s *Service) RevokeCredential(ctx context.Context, id string) (Credential, error) {
	return s.credentials.RevokeCredential(ctx, strings.TrimSpace(id))
}

func (s *Service) ValidateCredential(ctx context.Context, rawSecret string) (Credential, error) {
	return s.credentials.ValidateCredential(ctx, strings.TrimSpace(rawSecret))
}

func (s *Service) QueryTokenCostPage(ctx context.Context, query Query) (Page, error) {
	if err := ValidateQuery(query); err != nil {
		return Page{}, err
	}
	return s.tokenCosts.QueryTokenCostPage(ctx, query)
}
