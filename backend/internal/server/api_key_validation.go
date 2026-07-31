package server

import "time"

func (s *GormStore) ValidateAPIKey(rawSecret string, clientIP string) (Project, APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key APIKey
	if err := s.db.First(&key, "key_hash = ?", HashSecret(rawSecret)).Error; err != nil {
		return Project{}, APIKey{}, ErrInvalidAPIKey
	}
	hydrateAPIKey(&key)
	if key.Status == StatusDisabled || key.Status == StatusRevoked {
		if !(key.Status == StatusRevoked && key.GraceUntil != nil && time.Now().UTC().Before(*key.GraceUntil)) {
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
	}
	if key.ManagedBy == gatewayModelAccessKeyManagedBy {
		switch gatewayModelAccessKeyEffectiveStatus(s.db, key) {
		case gatewayModelAccessKeyStatusExpired:
			return Project{}, APIKey{}, ErrAPIKeyExpired
		case StatusActive:
		default:
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
	}
	if len(key.IPAllowlist) > 0 && !ipAllowed(clientIP, key.IPAllowlist) {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	if key.ExpiresAt != nil && time.Now().UTC().After(*key.ExpiresAt) {
		return Project{}, APIKey{}, ErrAPIKeyExpired
	}
	var project Project
	if err := s.db.First(&project, "id = ?", key.ProjectID).Error; err != nil || project.Status != StatusActive {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	now := time.Now().UTC()
	key.LastUsedAt = &now
	if err := s.db.Model(&key).Update("last_used_at", now).Error; err != nil {
		return Project{}, APIKey{}, err
	}
	return project, publicKey(key), nil
}
