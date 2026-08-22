package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) CreateResource(kind string, resource AdminResource) AdminResource {
	resource, _ = s.CreateResourceChecked(kind, resource)
	return resource
}

func (s *GormStore) CreateResourceChecked(kind string, resource AdminResource) (AdminResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createResourceLocked(kind, resource, true)
}

func (s *GormStore) CreateRoutingPolicy(resource AdminResource) (AdminResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resource, err := s.createResourceLocked(routingPolicyResourceKind, resource, false)
	if err != nil {
		return AdminResource{}, writeConflict(err, "routing_policy_binding_conflict", "A routing policy is already bound to this scope")
	}
	return resource, nil
}

func (s *GormStore) createResourceLocked(kind string, resource AdminResource, upsert bool) (AdminResource, error) {
	now := time.Now().UTC()
	if resource.ID == "" {
		resource.ID = NewID(resourcePrefix(kind))
	}
	if resource.Status == "" {
		resource.Status = StatusActive
	}
	if resource.Fields == nil {
		resource.Fields = map[string]any{}
	}
	resource.Kind = kind
	resource.RoutingPolicyBindingKey = nil
	if kind == routingPolicyResourceKind {
		resource.RoutingPolicyBindingKey = routingPolicyBindingKey(resource.Fields)
	}
	if resource.CreatedAt.IsZero() {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	query := s.db
	if upsert {
		query = query.Clauses(clause.OnConflict{UpdateAll: true})
	}
	return resource, query.Create(&resource).Error
}

func (s *GormStore) ListResources(kind string) []AdminResource {
	items, _ := s.ListResourcesChecked(kind)
	return items
}

// ListResourcesChecked preserves any context already attached to s.db (for
// example, the startup lease context) while making query failures observable.
func (s *GormStore) ListResourcesChecked(kind string) ([]AdminResource, error) {
	var items []AdminResource
	err := s.db.Where("kind = ?", kind).Order("created_at asc").Find(&items).Error
	return items, err
}

func (s *GormStore) ListResourcesContext(ctx context.Context, kind string) ([]AdminResource, error) {
	var items []AdminResource
	err := s.db.WithContext(ctx).Where("kind = ?", kind).Order("created_at asc").Find(&items).Error
	return items, err
}

func (s *GormStore) UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource AdminResource
	if err := s.db.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return AdminResource{}, notFound(err, "resource_not_found", "Resource not found")
	}
	if patch.Name != "" {
		resource.Name = patch.Name
	}
	resource.Description = patch.Description
	if patch.Status != "" {
		resource.Status = patch.Status
	}
	if patch.Fields != nil {
		resource.Fields = patch.Fields
	}
	resource.RoutingPolicyBindingKey = nil
	if kind == routingPolicyResourceKind {
		resource.RoutingPolicyBindingKey = routingPolicyBindingKey(resource.Fields)
	}
	resource.UpdatedAt = time.Now().UTC()
	err := s.db.Save(&resource).Error
	if kind == routingPolicyResourceKind {
		err = writeConflict(err, "routing_policy_binding_conflict", "A routing policy is already bound to this scope")
	}
	return resource, err
}

func (s *GormStore) DeleteResource(kind string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource AdminResource
	if err := s.db.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return notFound(err, "resource_not_found", "Resource not found")
	}
	return s.db.Delete(&resource).Error
}

func (s *GormStore) DeleteTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		team, err := lockAdminResourceForMutation(tx, "teams", id)
		if err != nil {
			return err
		}
		var projectLinkCount int64
		if err := tx.Model(&ProjectTeam{}).Where("team_id = ?", id).Count(&projectLinkCount).Error; err != nil {
			return err
		}
		var primaryProjectCount int64
		if err := tx.Model(&Project{}).Where("team_id = ?", id).Count(&primaryProjectCount).Error; err != nil {
			return err
		}
		if projectLinkCount > 0 || primaryProjectCount > 0 {
			return NewHTTPError(http.StatusConflict, "team_has_projects", "Team is linked to one or more projects; unlink or transfer those projects first")
		}
		var users []AdminUser
		if err := tx.Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			if userHasTeam(user, id) {
				return NewHTTPError(http.StatusConflict, "team_has_users", "Team still has users; reassign them before deleting the team")
			}
		}
		return tx.Delete(&team).Error
	})
}

func (s *GormStore) RunMonitor(id string) (MonitorRunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var monitor AdminResource
	if err := s.db.First(&monitor, "kind = ? AND id = ?", "monitors", id).Error; err != nil {
		return MonitorRunResult{}, notFound(err, "monitor_not_found", "Monitor not found")
	}
	started := time.Now().UTC()
	result := MonitorRunResult{
		MonitorID: monitor.ID,
		CheckedAt: started,
		Status:    "ok",
	}
	fields := cloneFields(monitor.Fields)
	targetType := strings.ToLower(strings.TrimSpace(stringField(fields, "target_type")))
	if targetType == "" {
		targetType = inferMonitorTargetType(fields)
	}
	result.TargetType = targetType
	switch targetType {
	case "provider":
		providerID := strings.TrimSpace(firstStringField(fields, "provider_id", "provider"))
		result.ProviderID = providerID
		result.TargetID = providerID
		var provider Provider
		if err := s.db.First(&provider, "id = ?", providerID).Error; err != nil {
			result.Status = "failed"
			result.Message = "Provider 不存在"
		} else {
			healthy := provider.Status == StatusActive
			result.Status = okFailed(healthy)
			result.Message = monitorProviderMessage(provider, healthy)
			now := time.Now().UTC()
			_ = s.db.Model(&Provider{}).Where("id = ?", provider.ID).Update("healthy", healthy).Error
			if !healthy {
				_ = s.db.Model(&ProviderResource{}).Where("provider_id = ?", provider.ID).Updates(map[string]any{"healthy": false, "last_checked_at": now, "updated_at": now}).Error
			}
		}
	case "resource", "provider_resource":
		resourceID := strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource"))
		result.ResourceID = resourceID
		result.TargetID = resourceID
		var resource ProviderResource
		if err := s.db.First(&resource, "id = ?", resourceID).Error; err != nil {
			result.Status = "failed"
			result.Message = "Provider 资源实例不存在"
		} else {
			healthy := resource.Status == StatusActive
			result.ProviderID = resource.ProviderID
			result.Status = okFailed(healthy)
			result.Message = monitorResourceMessage(resource, healthy)
			now := time.Now().UTC()
			updates := map[string]any{"healthy": healthy, "last_checked_at": now, "updated_at": now}
			if healthy {
				updates["failure_count"] = 0
				updates["cooldown_until"] = nil
			}
			_ = s.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Updates(updates).Error
		}
	case "model":
		modelName := strings.TrimSpace(firstStringField(fields, "model", "model_name"))
		result.ModelName = modelName
		result.TargetID = modelName
		var routes int64
		if modelName == "" {
			result.Status = "failed"
			result.Message = "模型名为空"
		} else if err := s.db.Model(&ModelRoute{}).Where("model_name = ? AND status = ?", modelName, StatusActive).Count(&routes).Error; err != nil {
			return MonitorRunResult{}, err
		} else if routes == 0 {
			result.Status = "failed"
			result.Message = "没有可用模型路由"
		} else {
			result.Status = "ok"
			result.Message = fmt.Sprintf("模型路由可用，候选路由 %d 条", routes)
		}
	default:
		result.Status = "failed"
		result.Message = "不支持的监控目标"
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if result.Message == "" {
		result.Message = "监控执行完成"
	}
	fields["target_type"] = result.TargetType
	fields["last_status"] = result.Status
	fields["last_result"] = result.Status
	fields["last_message"] = result.Message
	fields["last_checked_at"] = result.CheckedAt.Format(time.RFC3339)
	fields["latency_ms"] = result.LatencyMS
	fields["provider_id"] = result.ProviderID
	fields["provider_resource_id"] = result.ResourceID
	fields["model"] = result.ModelName
	monitor.Fields = fields
	monitor.UpdatedAt = time.Now().UTC()
	if err := s.db.Save(&monitor).Error; err != nil {
		return MonitorRunResult{}, err
	}
	if result.Status != "ok" {
		alert := AlertEvent{
			ID:         NewID("alt"),
			ScopeType:  "monitor",
			ScopeID:    monitor.ID,
			Severity:   "warning",
			Code:       "monitor_check_failed",
			Message:    result.Message,
			ResourceID: result.TargetID,
			CreatedAt:  time.Now().UTC(),
		}
		if err := s.db.Create(&alert).Error; err != nil {
			return MonitorRunResult{}, err
		}
		result.AlertID = alert.ID
	}
	return result, nil
}

func (s *GormStore) CreateApprovalRequest(request ApprovalRequest) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.ID == "" {
		request.ID = NewID("apr")
	}
	if request.Status == "" {
		request.Status = "pending"
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now().UTC()
	}
	_ = s.db.Create(&request).Error
	return request
}

func (s *GormStore) ListApprovalRequests() []ApprovalRequest {
	var items []ApprovalRequest
	_ = s.db.Order("created_at desc").Limit(500).Find(&items).Error
	return items
}

func (s *GormStore) GetApprovalRequest(id string) (ApprovalRequest, error) {
	var item ApprovalRequest
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return ApprovalRequest{}, notFound(err, "approval_not_found", "Approval request not found")
	}
	return item, nil
}

func (s *GormStore) UpdateApprovalRequestStatus(id string, status string, decidedBy string, reason string) (ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var item ApprovalRequest
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return ApprovalRequest{}, notFound(err, "approval_not_found", "Approval request not found")
	}
	if item.Status != "pending" {
		return ApprovalRequest{}, NewHTTPError(http.StatusConflict, "approval_already_decided", "Approval request has already been decided")
	}
	now := time.Now().UTC()
	item.Status = status
	item.Reason = reason
	item.DecidedAt = &now
	item.DecidedBy = decidedBy
	if err := s.db.Save(&item).Error; err != nil {
		return ApprovalRequest{}, err
	}
	return item, nil
}

func (s *GormStore) CreateAdminUser(user AdminUser, password string) (AdminUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var created AdminUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTeamsForMutation(tx, user.TeamID, user.TeamIDs); err != nil {
			return err
		}
		var err error
		created, err = createAdminUser(tx, user, password)
		return err
	})
	return created, err
}

func (s *GormStore) ListAdminUsers() []AdminUser {
	var items []AdminUser
	_ = s.db.Order("created_at asc").Find(&items).Error
	for i := range items {
		items[i] = publicAdminUser(items[i])
	}
	return items
}

func (s *GormStore) UpdateAdminUser(id string, patch AdminUser, password string) (AdminUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var updated AdminUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTeamsForMutation(tx, patch.TeamID, patch.TeamIDs); err != nil {
			return err
		}
		var err error
		updated, err = updateAdminUser(tx, id, patch, password)
		return err
	})
	return updated, err
}

func updateAdminUser(db *gorm.DB, id string, patch AdminUser, password string) (AdminUser, error) {
	var user AdminUser
	if err := db.First(&user, "id = ?", id).Error; err != nil {
		return AdminUser{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	wasActivePlatformAdmin := activePlatformAdmin(user)
	if patch.Username != "" {
		var count int64
		if err := db.Model(&AdminUser{}).Where("id <> ? AND username = ?", id, patch.Username).Count(&count).Error; err != nil {
			return AdminUser{}, err
		}
		if count > 0 {
			return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Username already exists")
		}
		user.Username = patch.Username
	}
	if patch.Name != "" {
		user.Name = patch.Name
	}
	if patch.Email != "" {
		var count int64
		if err := db.Model(&AdminUser{}).Where("id <> ? AND email = ?", id, patch.Email).Count(&count).Error; err != nil {
			return AdminUser{}, err
		}
		if count > 0 {
			return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Email already exists")
		}
		user.Email = patch.Email
	}
	if patch.Role != "" {
		user.Role = patch.Role
	}
	user.TeamID = patch.TeamID
	user.TeamIDs = normalizedTeamIDs(patch.TeamID, patch.TeamIDs)
	if patch.Status != "" {
		user.Status = patch.Status
	}
	if password != "" {
		passwordHash, err := hashPassword(password)
		if err != nil {
			return AdminUser{}, err
		}
		user.PasswordHash = passwordHash
	}
	if wasActivePlatformAdmin && !activePlatformAdmin(user) {
		if err := ensureAnotherActivePlatformAdmin(db, user.ID); err != nil {
			return AdminUser{}, err
		}
	}
	user.UpdatedAt = time.Now().UTC()
	if err := db.Save(&user).Error; err != nil {
		return AdminUser{}, err
	}
	if password != "" {
		if err := deleteInitialAdminPassword(db, user.ID); err != nil {
			return AdminUser{}, err
		}
	}
	return publicAdminUser(user), nil
}

func (s *GormStore) DeleteAdminUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var user AdminUser
		if err := tx.First(&user, "id = ?", id).Error; err != nil {
			return notFound(err, "admin_user_not_found", "Admin user not found")
		}
		if activePlatformAdmin(user) {
			if err := ensureAnotherActivePlatformAdmin(tx, user.ID); err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", id).Delete(&AdminSession{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&AdminPasswordResetToken{}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	})
}

func activePlatformAdmin(user AdminUser) bool {
	role := strings.ToLower(strings.TrimSpace(user.Role))
	return user.Status == StatusActive && (role == "admin" || role == "system_admin")
}

func ensureAnotherActivePlatformAdmin(db *gorm.DB, excludedUserID string) error {
	var count int64
	if err := db.Model(&AdminUser{}).
		Where("id <> ? AND status = ? AND lower(role) IN ?", excludedUserID, StatusActive, []string{"admin", "system_admin"}).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return NewHTTPError(400, "last_admin_user", "Cannot remove, disable, or demote the last active platform administrator")
	}
	return nil
}

func usageAttributionUserID(key APIKey, project Project) string {
	if ownerUserID := strings.TrimSpace(key.OwnerUserID); ownerUserID != "" {
		return ownerUserID
	}
	if key.Metadata != nil {
		if creatorUserID := strings.TrimSpace(key.Metadata["created_by"]); creatorUserID != "" {
			return creatorUserID
		}
	}
	return strings.TrimSpace(project.OwnerUserID)
}

func (s *GormStore) CreateAdminPasswordResetToken(userID string, createdBy string, ttl time.Duration) (string, AdminPasswordResetToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var user AdminUser
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return "", AdminPasswordResetToken{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	now := time.Now().UTC()
	plainToken := NewID("rst") + NewID("tok")
	item := AdminPasswordResetToken{
		ID:        NewID("rtk"),
		UserID:    userID,
		TokenHash: HashSecret(plainToken),
		ExpiresAt: now.Add(ttl),
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	if err := s.db.Create(&item).Error; err != nil {
		return "", AdminPasswordResetToken{}, err
	}
	return plainToken, item, nil
}

func (s *GormStore) ResetAdminUserPassword(token string, password string) (AdminUser, error) {
	return s.resetAdminUserPassword(token, password, hashPassword)
}

func (s *GormStore) resetAdminUserPassword(token string, password string, passwordHasher func(string) (string, error)) (AdminUser, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(password) == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_reset_request", "token and password are required")
	}
	tokenHash := HashSecret(token)
	var candidate AdminPasswordResetToken
	if err := s.db.Select("id").Take(&candidate, "token_hash = ? AND used_at IS NULL AND expires_at > ?", tokenHash, time.Now().UTC()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminUser{}, NewHTTPError(400, "invalid_reset_token", "Reset token is invalid or expired")
		}
		return AdminUser{}, err
	}
	passwordHash, err := passwordHasher(password)
	if err != nil {
		return AdminUser{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var user AdminUser
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		claimed := tx.Model(&AdminPasswordResetToken{}).
			Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", tokenHash, now).
			Update("used_at", now)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected != 1 {
			return NewHTTPError(400, "invalid_reset_token", "Reset token is invalid or expired")
		}
		var item AdminPasswordResetToken
		if err := tx.First(&item, "token_hash = ?", tokenHash).Error; err != nil {
			return err
		}
		if err := tx.First(&user, "id = ?", item.UserID).Error; err != nil {
			return notFound(err, "admin_user_not_found", "Admin user not found")
		}
		user.PasswordHash = passwordHash
		user.UpdatedAt = now
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&AdminSession{}).Error; err != nil {
			return err
		}
		return deleteInitialAdminPassword(tx, user.ID)
	}); err != nil {
		return AdminUser{}, err
	}
	return publicAdminUser(user), nil
}

func (s *GormStore) AuthenticateAdminUser(identity string, password string, ttl time.Duration) (AdminUser, AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	identity = strings.ToLower(strings.TrimSpace(identity))
	var user AdminUser
	if err := s.db.Where("LOWER(email) = ? OR LOWER(username) = ?", identity, identity).First(&user).Error; err != nil {
		return AdminUser{}, AdminSession{}, NewHTTPError(401, "invalid_credentials", "Invalid username or password")
	}
	if user.Status != StatusActive {
		return AdminUser{}, AdminSession{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
	}
	validPassword, needsPasswordUpgrade := verifyPassword(user.PasswordHash, password)
	if !validPassword {
		return AdminUser{}, AdminSession{}, NewHTTPError(401, "invalid_credentials", "Invalid username or password")
	}
	if needsPasswordUpgrade {
		upgradedHash, err := hashPasswordForUpgrade(password)
		if err != nil {
			return AdminUser{}, AdminSession{}, err
		}
		user.PasswordHash = upgradedHash
	}
	now := time.Now().UTC()
	session := AdminSession{
		Token:     GenerateAdminSessionToken(),
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		return deleteInitialAdminPassword(tx, user.ID)
	})
	if err != nil {
		return AdminUser{}, AdminSession{}, err
	}
	return publicAdminUser(user), session, nil
}

func (s *GormStore) CreateAdminSession(userID string, ttl time.Duration) (AdminUser, AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var user AdminUser
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return AdminUser{}, AdminSession{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	if user.Status != StatusActive {
		return AdminUser{}, AdminSession{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
	}
	now := time.Now().UTC()
	session := AdminSession{
		Token:     GenerateAdminSessionToken(),
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		return deleteInitialAdminPassword(tx, user.ID)
	})
	if err != nil {
		return AdminUser{}, AdminSession{}, err
	}
	return publicAdminUser(user), session, nil
}

func (s *GormStore) ValidateAdminSession(token string) (AdminUser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var session AdminSession
	if err := s.db.First(&session, "token = ?", token).Error; err != nil {
		return AdminUser{}, false
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		_ = s.db.Delete(&session).Error
		return AdminUser{}, false
	}
	var user AdminUser
	if err := s.db.First(&user, "id = ? AND status = ?", session.UserID, StatusActive).Error; err != nil {
		return AdminUser{}, false
	}
	return publicAdminUser(user), true
}

func (s *GormStore) RevokeAdminSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.db.Delete(&AdminSession{}, "token = ?", token).Error
}
