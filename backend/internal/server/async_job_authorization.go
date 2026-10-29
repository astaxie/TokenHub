package server

import (
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// asyncJobScope comes from the durable job, never from a caller's project hint.
// A rebind changes future requests while existing jobs retain this scope.
type asyncJobScope struct {
	ProjectID, APIKeyID, AttributedUserID, Model, ClientIP string
}

func requestAPIKeySecret(r *http.Request) (string, error) {
	if auth := r.Header.Get("authorization"); auth != "" {
		if !strings.HasPrefix(auth, "Bearer ") {
			return "", ErrInvalidAPIKey
		}
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")), nil
	}
	for _, header := range []string{"x-api-key", "x-goog-api-key"} {
		if secret := strings.TrimSpace(r.Header.Get(header)); secret != "" {
			return secret, nil
		}
	}
	return "", ErrInvalidAPIKey
}

func (s *Server) authenticateAsyncJobKey(r *http.Request) (APIKey, error) {
	secret, err := requestAPIKeySecret(r)
	if err != nil {
		return APIKey{}, err
	}
	return s.store.AuthenticateAsyncJobAPIKey(secret, s.clientIP(r))
}

// This authenticates the secret and raw key lifecycle only. Job handlers must
// subsequently authorize the durable job's original project before returning data.
func (s *GormStore) AuthenticateAsyncJobAPIKey(rawSecret, clientIP string) (APIKey, error) {
	var key APIKey
	if err := s.db.First(&key, "key_hash = ?", HashSecret(rawSecret)).Error; err != nil {
		return APIKey{}, ErrInvalidAPIKey
	}
	if err := validateAsyncJobKeyIdentity(key, clientIP); err != nil {
		return APIKey{}, err
	}
	hydrateAPIKey(&key)
	return publicKey(key), nil
}

func validateAsyncJobKeyIdentity(key APIKey, clientIP string) error {
	now := time.Now().UTC()
	if key.ManagedBy == gatewayModelAccessKeyManagedBy && key.Status != StatusActive || key.Status == StatusDisabled || key.Status == StatusRevoked && (key.GraceUntil == nil || !now.Before(*key.GraceUntil)) {
		return ErrAPIKeyDisabled
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return ErrAPIKeyExpired
	}
	if len(key.IPAllowlist) > 0 && !ipAllowed(clientIP, key.IPAllowlist) {
		return ErrAPIKeyDisabled
	}
	return nil
}

func (s *GormStore) AuthorizeAsyncJob(projectID, keyID, attributedUserID, model, clientIP string) (Project, APIKey, error) {
	var project Project
	var key APIKey
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		if err := tx.First(&key, "id = ?", keyID).Error; err != nil {
			return ErrInvalidAPIKey
		}
		var err error
		project, key, err = authorizeAsyncJobScope(tx, key, asyncJobScope{projectID, keyID, attributedUserID, model, clientIP})
		return err
	})
	return project, publicKey(key), err
}

func authorizeAsyncJobScope(tx *gorm.DB, key APIKey, scope asyncJobScope) (Project, APIKey, error) {
	if key.ID != scope.APIKeyID {
		return Project{}, APIKey{}, ErrInvalidAPIKey
	}
	if key.ProjectID != scope.ProjectID {
		// Only integration-managed personal credentials support project changes.
		// The original projection must still belong to the credential's tenant.
		if key.ManagedBy != gatewayModelAccessKeyManagedBy || key.PrincipalType != "user" {
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
		key.ProjectID = scope.ProjectID
	}
	hydrateAPIKey(&key)
	if err := validateAsyncJobKeyIdentity(key, scope.ClientIP); err != nil {
		return Project{}, APIKey{}, err
	}
	if key.ManagedBy == gatewayModelAccessKeyManagedBy {
		if key.Status != StatusActive || validateGatewayModelAccessKeyEffectiveScope(tx, key) != nil {
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
		var projection GatewayProject
		if err := tx.First(&projection, "id = ?", scope.ProjectID).Error; err != nil {
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
		key.ProjectExternalID = projection.ExternalProjectID
	}
	var project Project
	if err := tx.First(&project, "id = ?", scope.ProjectID).Error; err != nil || project.Status != StatusActive {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	hydrateProject(&project)
	if usageAttributionUserID(key, project) != scope.AttributedUserID {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	if !modelAllowedByScopes(project, key, scope.Model) {
		return Project{}, APIKey{}, ErrModelNotAllowed
	}
	return project, key, nil
}

func (s *Server) asyncJobAccessible(key APIKey, projectID, keyID, attributedUserID, model, clientIP string) bool {
	if key.ID != keyID {
		return false
	}
	_, _, err := s.store.AuthorizeAsyncJob(projectID, keyID, attributedUserID, model, clientIP)
	return err == nil
}

func (s *Server) authorizeImageJobRead(r *http.Request, key APIKey, job ImageJob) error {
	// Preserve existing project-shared image reads. Cross-project recovery after
	// a rebind is restricted to the original key and its original authorization.
	if key.ProjectID == job.ProjectID {
		project, _, err := s.authenticate(r)
		if err != nil || project.ID == job.ProjectID {
			return err
		}
	}
	if s.asyncJobAccessible(key, job.ProjectID, job.APIKeyID, job.AttributedUserID, job.Model, s.clientIP(r)) {
		return nil
	}
	return NewHTTPError(http.StatusNotFound, "image_job_not_found", "Image job not found")
}

func (s *GormStore) ResponseJobModelAccessible(job ResponseJob, clientIP string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	accessible := false
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		var key APIKey
		if err := tx.First(&key, "id = ?", job.APIKeyID).Error; err != nil {
			return err
		}
		project, key, err := authorizeAsyncJobScope(tx, key, asyncJobScope{job.ProjectID, job.APIKeyID, job.AttributedUserID, job.Model, clientIP})
		if err != nil {
			return err
		}
		for _, model := range s.accessibleModelsInScope(tx, project, key) {
			if model.Name == job.Model || model.ID == job.Model {
				accessible = true
				break
			}
		}
		return nil
	})
	return err == nil && accessible
}
