package server

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) authenticate(r *http.Request) (Project, APIKey, error) {
	auth := r.Header.Get("authorization")
	if auth != "" {
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			return Project{}, APIKey{}, ErrInvalidAPIKey
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
		if strings.HasPrefix(token, "thd_") && s.config.A2AEnabled {
			return s.authenticateAgentDelegation(token)
		}
		return s.store.ValidateAPIKey(token, s.clientIP(r))
	}
	if apiKey := strings.TrimSpace(r.Header.Get("x-api-key")); apiKey != "" {
		return s.store.ValidateAPIKey(apiKey, s.clientIP(r))
	}
	// The official Google Gen AI SDK, and therefore Gemini CLI in API-key mode,
	// sends its credential in x-goog-api-key when GOOGLE_GEMINI_BASE_URL points
	// at a compatible gateway.
	if apiKey := strings.TrimSpace(r.Header.Get("x-goog-api-key")); apiKey != "" {
		return s.store.ValidateAPIKey(apiKey, s.clientIP(r))
	}
	return Project{}, APIKey{}, ErrInvalidAPIKey
}

func (s *Server) authenticateAgentDelegation(token string) (Project, APIKey, error) {
	project, key, claims, err := s.validateAgentDelegation(token)
	if err != nil {
		return Project{}, APIKey{}, err
	}
	if claims.ExecutionID != "" {
		if err := s.store.ConsumeAgentExecutionBudget(claims.ExecutionID, "model", 0, 0); err != nil {
			return Project{}, APIKey{}, err
		}
	}
	return project, key, nil
}

func (s *Server) validateAgentDelegation(token string) (Project, APIKey, agentDelegationClaims, error) {
	claims, err := s.parseAgentDelegation(token)
	if err != nil {
		return Project{}, APIKey{}, agentDelegationClaims{}, err
	}
	project, found := s.store.GetProject(claims.ProjectID)
	if !found || project.Status != StatusActive {
		return Project{}, APIKey{}, agentDelegationClaims{}, ErrInvalidAPIKey
	}
	for _, key := range s.store.ListAPIKeys() {
		if key.ID == claims.APIKeyID && key.ProjectID == project.ID && key.Status == StatusActive &&
			(key.ExpiresAt == nil || key.ExpiresAt.After(time.Now().UTC())) {
			if claims.ExecutionID != "" {
				details, found, err := s.store.GetAgentExecutionDetails(claims.ExecutionID)
				if err != nil || !found || details.ProjectID != project.ID || details.APIKeyID != key.ID {
					return Project{}, APIKey{}, agentDelegationClaims{}, ErrInvalidAPIKey
				}
				if details.Status != "running" {
					return Project{}, APIKey{}, agentDelegationClaims{}, NewHTTPError(409, "agent_execution_not_running", "Agent execution is no longer running")
				}
				if details.Deadline != nil && !details.Deadline.After(time.Now().UTC()) {
					_ = s.store.FinishAgentExecution(details.ID, "budget_exceeded")
					return Project{}, APIKey{}, agentDelegationClaims{}, NewHTTPError(429, "agent_runtime_budget_exceeded", "Agent execution runtime budget was exceeded")
				}
			}
			return project, key, claims, nil
		}
	}
	return Project{}, APIKey{}, agentDelegationClaims{}, ErrInvalidAPIKey
}
