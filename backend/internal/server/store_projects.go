package server

import (
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"tokenhub/backend/internal/guardrails"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) CreateProject(project Project) Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, _ = s.createProject(project, false)
	return project
}

func (s *GormStore) CreateProjectChecked(project Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createProject(project, true)
}

func (s *GormStore) createProject(project Project, requireActiveTeam bool) (Project, error) {
	mode, allowed, err := normalizeModelAccess(project.ModelAccessMode, project.AllowedModels)
	if err != nil {
		return Project{}, err
	}
	project.ModelAccessMode, project.AllowedModels = mode, allowed
	now := time.Now().UTC()
	if project.ID == "" {
		project.ID = NewID("prj")
	}
	if project.Status == "" {
		project.Status = StatusActive
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	project.Teams = nil
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := validateConfiguredModels(tx, project.AllowedModels); err != nil {
			return err
		}
		if strings.TrimSpace(project.TeamID) != "" {
			team, err := lockTeamForMutation(tx, project.TeamID)
			if err != nil {
				return err
			}
			if requireActiveTeam && team.Status != "" && team.Status != StatusActive {
				return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
			}
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&project).Error; err != nil {
			return err
		}
		if strings.TrimSpace(project.TeamID) == "" {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ProjectTeam{
			ProjectID: project.ID,
			TeamID:    strings.TrimSpace(project.TeamID),
			Role:      "team_leader",
			CreatedAt: now,
			UpdatedAt: now,
		}).Error
	})
	if err != nil {
		return Project{}, err
	}
	_ = s.loadProjectTeams(&project)
	return project, nil
}

func (s *GormStore) ListProjects() []Project {
	var items []Project
	_ = s.db.Order("created_at asc").Find(&items).Error
	for index := range items {
		hydrateProject(&items[index])
	}
	_ = s.loadProjectTeamsFor(items)
	return items
}

func (s *GormStore) UpdateProject(id string, patch Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var project Project
	nextTeamID := strings.TrimSpace(patch.TeamID)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "project", id); err != nil {
			return err
		}
		var nextTeam AdminResource
		if nextTeamID != "" {
			var err error
			nextTeam, err = lockTeamForMutation(tx, nextTeamID)
			if err != nil {
				return err
			}
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, "id = ?", id).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		if nextTeamID != strings.TrimSpace(project.TeamID) && nextTeam.Status != "" && nextTeam.Status != StatusActive {
			return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
		}
		if patch.Name != "" {
			project.Name = patch.Name
		}
		project.TeamID = nextTeamID
		project.OwnerUserID = patch.OwnerUserID
		project.CostCenter = patch.CostCenter
		if patch.Status != "" {
			project.Status = patch.Status
		}
		if patch.ModelAccessMode != "" || patch.AllowedModels != nil {
			mode, allowed, err := normalizeModelAccess(patch.ModelAccessMode, patch.AllowedModels)
			if err != nil {
				return err
			}
			if err := validateConfiguredModels(tx, allowed); err != nil {
				return err
			}
			project.ModelAccessMode, project.AllowedModels = mode, allowed
		}
		project.DefaultQuotaRef = patch.DefaultQuotaRef
		project.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&project).Error; err != nil {
			return err
		}
		if project.TeamID == "" {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ProjectTeam{
			ProjectID: project.ID,
			TeamID:    project.TeamID,
			Role:      "team_leader",
			CreatedAt: project.UpdatedAt,
			UpdatedAt: project.UpdatedAt,
		}).Error
	})
	if err != nil {
		return Project{}, err
	}
	hydrateProject(&project)
	_ = s.loadProjectTeams(&project)
	return project, nil
}

func (s *GormStore) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "project", id); err != nil {
			return err
		}
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, "id = ?", id).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		var guardrailBindingCount int64
		if err := tx.Model(&guardrails.Binding{}).Where("scope_type = ? AND scope_id = ?", guardrails.ScopeProject, id).Count(&guardrailBindingCount).Error; err != nil {
			return err
		}
		if guardrailBindingCount > 0 {
			return NewHTTPError(http.StatusConflict, "project_guardrail_binding_conflict", "Remove the project from content security policies before deleting it")
		}
		var keys []APIKey
		if err := tx.Where("project_id = ?", id).Find(&keys).Error; err != nil {
			return err
		}
		keyIDs := make([]string, 0, len(keys))
		for _, key := range keys {
			keyIDs = append(keyIDs, key.ID)
		}
		if len(keyIDs) > 0 {
			if err := tx.Where("scope_type = ? AND scope_id IN ?", "api_key", keyIDs).Delete(&InFlightLease{}).Error; err != nil {
				return err
			}
			if err := tx.Where("key_id IN ?", keyIDs).Delete(&QuotaBucket{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", keyIDs).Delete(&APIKey{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("project_id = ?", id).Delete(&ProjectTeam{}).Error; err != nil {
			return err
		}
		return tx.Delete(&project).Error
	})
}

func (s *GormStore) GetProject(id string) (Project, bool) {
	var project Project
	if err := s.db.First(&project, "id = ?", id).Error; err != nil {
		return Project{}, false
	}
	hydrateProject(&project)
	_ = s.loadProjectTeams(&project)
	return project, true
}

func (s *GormStore) loadProjectTeams(project *Project) error {
	if project == nil || strings.TrimSpace(project.ID) == "" {
		return nil
	}
	var links []ProjectTeam
	if err := s.db.Where("project_id = ?", project.ID).Order("created_at asc, team_id asc").Find(&links).Error; err != nil {
		return err
	}
	for index := range links {
		links[index].IsPrimary = links[index].TeamID == project.TeamID
	}
	project.Teams = links
	return nil
}

func (s *GormStore) loadProjectTeamsFor(projects []Project) error {
	if len(projects) == 0 {
		return nil
	}
	projectIDs := make([]string, 0, len(projects))
	projectIndex := make(map[string]int, len(projects))
	for index := range projects {
		projects[index].Teams = nil
		projectIDs = append(projectIDs, projects[index].ID)
		projectIndex[projects[index].ID] = index
	}
	var links []ProjectTeam
	if err := s.db.Where("project_id IN ?", projectIDs).Order("created_at asc, team_id asc").Find(&links).Error; err != nil {
		return err
	}
	for _, link := range links {
		index, ok := projectIndex[link.ProjectID]
		if !ok {
			continue
		}
		link.IsPrimary = link.TeamID == projects[index].TeamID
		projects[index].Teams = append(projects[index].Teams, link)
	}
	return nil
}

func (s *GormStore) ListProjectTeams(projectID string, offset int, limit int) ([]ProjectTeam, int64, error) {
	var project Project
	if err := s.db.First(&project, "id = ?", projectID).Error; err != nil {
		return nil, 0, notFound(err, "project_not_found", "Project not found")
	}
	query := s.db.Model(&ProjectTeam{}).Where("project_id = ?", projectID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var links []ProjectTeam
	if err := query.Order("created_at asc, team_id asc").Offset(offset).Limit(limit).Find(&links).Error; err != nil {
		return nil, 0, err
	}
	for index := range links {
		links[index].IsPrimary = links[index].TeamID == project.TeamID
	}
	return links, total, nil
}

func (s *GormStore) AddProjectTeam(link ProjectTeam) (ProjectTeam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	link.ProjectID = strings.TrimSpace(link.ProjectID)
	link.TeamID = strings.TrimSpace(link.TeamID)
	now := time.Now().UTC()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	link.UpdatedAt = now
	var project Project
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockActiveTeamForMutation(tx, link.TeamID); err != nil {
			return err
		}
		if err := tx.First(&project, "id = ?", link.ProjectID).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		if err := tx.Create(&link).Error; err != nil {
			return writeConflict(err, "project_team_conflict", "Team is already linked to this project")
		}
		return nil
	})
	if err != nil {
		return ProjectTeam{}, err
	}
	link.IsPrimary = link.TeamID == project.TeamID
	return link, nil
}

func (s *GormStore) UpdateProjectTeam(projectID string, teamID string, role string) (ProjectTeam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var link ProjectTeam
	if err := s.db.First(&link, "project_id = ? AND team_id = ?", projectID, teamID).Error; err != nil {
		return ProjectTeam{}, notFound(err, "project_team_not_found", "Project team link not found")
	}
	link.Role = role
	link.UpdatedAt = time.Now().UTC()
	if err := s.db.Save(&link).Error; err != nil {
		return ProjectTeam{}, err
	}
	var project Project
	_ = s.db.First(&project, "id = ?", projectID).Error
	link.IsPrimary = link.TeamID == project.TeamID
	return link, nil
}

func (s *GormStore) RemoveProjectTeam(projectID string, teamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		project, err := lockProjectForTeamMutation(tx, projectID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(project.TeamID) == strings.TrimSpace(teamID) {
			return NewHTTPError(http.StatusConflict, "project_primary_team", "The primary team cannot be removed; assign another primary team first")
		}
		var count int64
		if err := tx.Model(&ProjectTeam{}).Where("project_id = ?", projectID).Count(&count).Error; err != nil {
			return err
		}
		if count <= 1 {
			return NewHTTPError(http.StatusConflict, "project_last_team", "The last project team cannot be removed")
		}
		result := tx.Where("project_id = ? AND team_id = ?", projectID, teamID).Delete(&ProjectTeam{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return NewHTTPError(http.StatusNotFound, "project_team_not_found", "Project team link not found")
		}
		return nil
	})
}

func lockProjectForTeamMutation(tx *gorm.DB, projectID string) (Project, error) {
	result := tx.Model(&Project{}).Where("id = ?", projectID).UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return Project{}, result.Error
	}
	if result.RowsAffected == 0 {
		return Project{}, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
	}
	var project Project
	if err := tx.First(&project, "id = ?", projectID).Error; err != nil {
		return Project{}, notFound(err, "project_not_found", "Project not found")
	}
	return project, nil
}

func lockAdminResourceForMutation(tx *gorm.DB, kind string, id string) (AdminResource, error) {
	result := tx.Model(&AdminResource{}).Where("kind = ? AND id = ?", kind, id).UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return AdminResource{}, result.Error
	}
	if result.RowsAffected == 0 {
		return AdminResource{}, NewHTTPError(http.StatusNotFound, "resource_not_found", "Resource not found")
	}
	var resource AdminResource
	if err := tx.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return AdminResource{}, notFound(err, "resource_not_found", "Resource not found")
	}
	return resource, nil
}

func lockActiveTeamForMutation(tx *gorm.DB, teamID string) error {
	team, err := lockTeamForMutation(tx, teamID)
	if err != nil {
		return err
	}
	if team.Status != "" && team.Status != StatusActive {
		return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
	}
	return nil
}

func lockTeamForMutation(tx *gorm.DB, teamID string) (AdminResource, error) {
	team, err := lockAdminResourceForMutation(tx, "teams", strings.TrimSpace(teamID))
	if err != nil {
		if AsHTTPError(err).Status == http.StatusNotFound {
			return AdminResource{}, NewHTTPError(http.StatusNotFound, "team_not_found", "Team not found")
		}
		return AdminResource{}, err
	}
	return team, nil
}

func lockUserTeamsForMutation(tx *gorm.DB, primaryTeamID string, teamIDs []string) error {
	ids := normalizedTeamIDs(primaryTeamID, teamIDs)
	sort.Strings(ids)
	for _, teamID := range ids {
		team, err := lockTeamForMutation(tx, teamID)
		if err != nil {
			return err
		}
		if team.Status != "" && team.Status != StatusActive {
			return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a user")
		}
	}
	return nil
}

func (s *GormStore) CreateAPIKey(projectID string, key APIKey, rawSecret string) (APIKey, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.First(&Project{}, "id = ?", projectID).Error; err != nil {
		return APIKey{}, "", notFound(err, "project_not_found", "Project not found")
	}
	if err := validateAPIKeyMinuteLimits(key.RateLimitRPM, key.TokenLimitTPM); err != nil {
		return APIKey{}, "", err
	}
	mode, allowed, err := normalizeModelAccess(key.ModelAccessMode, key.Allowed)
	if err != nil {
		return APIKey{}, "", err
	}
	if strings.TrimSpace(key.ModelAccessMode) != "" {
		if err := validateConfiguredModels(s.db, allowed); err != nil {
			return APIKey{}, "", err
		}
	}
	key.ModelAccessMode, key.Allowed = mode, allowed
	if rawSecret == "" {
		rawSecret = s.generateAPIKeySecret()
	}
	prefix, suffix := PrefixSuffix(rawSecret)
	now := time.Now().UTC()
	if key.ID == "" {
		key.ID = NewID("key")
	}
	if key.Status == "" {
		key.Status = StatusActive
	}
	if key.Group == "" {
		key.Group = "default"
	}
	key.ProjectID = projectID
	key.KeyHash = HashSecret(rawSecret)
	key.KeyPrefix = prefix
	key.KeySuffix = suffix
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	key.AllowedModels = AllowedModelSet(key.Allowed)
	if err := s.db.Create(&key).Error; err != nil {
		return APIKey{}, "", writeConflict(err, "api_key_conflict", "API key already exists")
	}
	return publicKey(key), rawSecret, nil
}

func (s *GormStore) generateAPIKeySecret() string {
	prefix, randomLength := s.apiKeyGenerationConfig()
	return GenerateAPIKeyWithOptions(prefix, randomLength)
}

func (s *GormStore) apiKeyGenerationConfig() (string, int) {
	var settings []AdminResource
	_ = s.db.Where("kind = ? AND status = ?", "settings", StatusActive).Order("created_at asc").Find(&settings).Error
	var fields map[string]any
	for _, item := range settings {
		if item.ID == "cfg_gateway" {
			fields = item.Fields
			break
		}
	}
	if fields == nil && len(settings) > 0 {
		fields = settings[0].Fields
	}
	prefix := stringField(fields, "api_key_prefix")
	randomLength := DefaultAPIKeyRandomLength
	if value := strings.TrimSpace(stringField(fields, "api_key_random_length")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			randomLength = parsed
		}
	}
	return NormalizeAPIKeyPrefix(prefix), NormalizeAPIKeyRandomLength(randomLength)
}

func (s *GormStore) ListProjectKeys(projectID string) []APIKey {
	var items []APIKey
	_ = s.db.Where("project_id = ?", projectID).Order("created_at asc").Find(&items).Error
	return publicKeys(items)
}

func (s *GormStore) ListAPIKeys() []APIKey {
	var items []APIKey
	_ = s.db.Order("created_at asc").Find(&items).Error
	return publicKeys(items)
}

func (s *GormStore) GetAPIKey(id string) (APIKey, bool) {
	var key APIKey
	if err := s.db.First(&key, "id = ?", id).Error; err != nil {
		return APIKey{}, false
	}
	hydrateAPIKey(&key)
	return publicKey(key), true
}

func (s *GormStore) UpdateAPIKey(id string, patch APIKey) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key APIKey
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "api_key", id); err != nil {
			return err
		}
		if err := tx.First(&key, "id = ?", id).Error; err != nil {
			return notFound(err, "api_key_not_found", "API key not found")
		}
		if err := validateAPIKeyMinuteLimits(patch.RateLimitRPM, patch.TokenLimitTPM); err != nil {
			return err
		}
		hydrateAPIKey(&key)
		if patch.Name != "" {
			key.Name = patch.Name
		}
		if patch.Group != "" {
			key.Group = patch.Group
		}
		if patch.OwnerUserID != "" {
			key.OwnerUserID = patch.OwnerUserID
		}
		if patch.Status != "" {
			key.Status = patch.Status
		}
		if patch.Allowed != nil {
			mode, allowed, err := normalizeModelAccess(patch.ModelAccessMode, patch.Allowed)
			if err != nil {
				return err
			}
			if strings.TrimSpace(patch.ModelAccessMode) != "" {
				if err := validateConfiguredModels(tx, allowed); err != nil {
					return err
				}
			}
			key.ModelAccessMode, key.Allowed = mode, allowed
			key.AllowedModels = AllowedModelSet(allowed)
		} else if patch.ModelAccessMode != "" {
			mode, allowed, err := normalizeModelAccess(patch.ModelAccessMode, key.Allowed)
			if err != nil {
				return err
			}
			key.ModelAccessMode, key.Allowed = mode, allowed
			key.AllowedModels = AllowedModelSet(allowed)
		}
		if patch.IPAllowlist != nil {
			key.IPAllowlist = patch.IPAllowlist
		}
		if patch.LimitsSet || patch.Limits != (QuotaLimits{}) {
			key.Limits = patch.Limits
		}
		if patch.RateLimitSet || patch.RateLimitRPM != nil {
			key.RateLimitRPM = patch.RateLimitRPM
		}
		if patch.TokenLimitSet || patch.TokenLimitTPM != nil {
			key.TokenLimitTPM = patch.TokenLimitTPM
		}
		if patch.ExpiresAt != nil {
			key.ExpiresAt = patch.ExpiresAt
		}
		return tx.Omit("LastUsedAt").Save(&key).Error
	})
	if err != nil {
		return APIKey{}, err
	}
	return publicKey(key), nil
}

func validateConfiguredModels(db *gorm.DB, modelNames []string) error {
	for _, modelName := range modelNames {
		var count int64
		if err := db.Model(&Model{}).Where("name = ? AND status = ?", modelName, StatusActive).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return NewHTTPError(http.StatusNotFound, "model_not_found", "Model not found")
		}
	}
	return nil
}

func validateAPIKeyMinuteLimits(rpm *int64, tpm *int64) error {
	if (rpm != nil && *rpm < 0) || (tpm != nil && *tpm < 0) {
		return NewHTTPError(http.StatusBadRequest, "invalid_api_key_rate_limit", "API key RPM and TPM limits must be zero or greater")
	}
	return nil
}

func (s *GormStore) RotateAPIKey(id string, graceUntil *time.Time) (APIKey, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var oldKey APIKey
	var newKey APIKey
	newSecret := s.generateAPIKeySecret()
	prefix, suffix := PrefixSuffix(newSecret)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "api_key", id); err != nil {
			return err
		}
		if err := tx.First(&oldKey, "id = ?", id).Error; err != nil {
			return notFound(err, "api_key_not_found", "API key not found")
		}
		hydrateAPIKey(&oldKey)
		now := time.Now().UTC()
		newKey = oldKey
		newKey.ID = NewID("key")
		newKey.KeyHash = HashSecret(newSecret)
		newKey.KeyPrefix = prefix
		newKey.KeySuffix = suffix
		newKey.RotatedFromID = oldKey.ID
		newKey.GraceUntil = nil
		newKey.CreatedAt = now
		newKey.LastUsedAt = nil
		newKey.Status = StatusActive
		if newKey.Metadata == nil {
			newKey.Metadata = map[string]string{}
		}
		newKey.Metadata["rotated_from"] = oldKey.ID

		if graceUntil != nil {
			oldKey.GraceUntil = graceUntil
			oldKey.Status = StatusActive
		} else {
			oldKey.Status = StatusRevoked
			oldKey.GraceUntil = &now
		}
		if err := tx.Omit("LastUsedAt").Save(&oldKey).Error; err != nil {
			return err
		}
		if err := tx.Create(&newKey).Error; err != nil {
			return err
		}
		return cloneRotatedAPIKeyRoutingPolicy(tx, oldKey.ID, newKey.ID, now)
	})
	if err != nil {
		return APIKey{}, "", err
	}
	return publicKey(newKey), newSecret, nil
}

func cloneRotatedAPIKeyRoutingPolicy(tx *gorm.DB, oldKeyID string, newKeyID string, now time.Time) error {
	oldBindingKey := RoutingPolicyScopeAPIKey + ":" + oldKeyID
	var policy AdminResource
	if err := tx.First(&policy, "kind = ? AND routing_policy_binding_key = ?", routingPolicyResourceKind, oldBindingKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	policy.ID = NewID(resourcePrefix(routingPolicyResourceKind))
	policy.Fields = cloneFields(policy.Fields)
	policy.Fields["scope"] = RoutingPolicyScopeAPIKey
	policy.Fields["scope_id"] = newKeyID
	policy.RoutingPolicyBindingKey = routingPolicyBindingKey(policy.Fields)
	policy.CreatedAt = now
	policy.UpdatedAt = now
	return tx.Create(&policy).Error
}

func (s *GormStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "api_key", id); err != nil {
			return err
		}
		var key APIKey
		if err := tx.First(&key, "id = ?", id).Error; err != nil {
			return notFound(err, "api_key_not_found", "API key not found")
		}
		if err := tx.Where("key_id = ?", id).Delete(&QuotaBucket{}).Error; err != nil {
			return err
		}
		if err := tx.Where("scope_type = ? AND scope_id = ?", "api_key", id).Delete(&InFlightLease{}).Error; err != nil {
			return err
		}
		return tx.Delete(&key).Error
	})
}

func (s *GormStore) ValidateAPIKey(rawSecret string, clientIP string) (Project, APIKey, error) {
	var key APIKey
	var project Project
	err := s.withReadSnapshot(func(tx *gorm.DB) error {
		if err := tx.First(&key, "key_hash = ?", HashSecret(rawSecret)).Error; err != nil {
			return ErrInvalidAPIKey
		}
		hydrateAPIKey(&key)
		now := time.Now().UTC()
		if key.Status == StatusDisabled || key.Status == StatusRevoked {
			if !(key.Status == StatusRevoked && key.GraceUntil != nil && now.Before(*key.GraceUntil)) {
				return ErrAPIKeyDisabled
			}
		}
		if len(key.IPAllowlist) > 0 && !ipAllowed(clientIP, key.IPAllowlist) {
			return ErrAPIKeyDisabled
		}
		if key.ExpiresAt != nil && now.After(*key.ExpiresAt) {
			return ErrAPIKeyExpired
		}
		if err := tx.First(&project, "id = ?", key.ProjectID).Error; err != nil || project.Status != StatusActive {
			return ErrAPIKeyDisabled
		}
		return nil
	})
	if err != nil {
		return Project{}, APIKey{}, err
	}
	now := time.Now().UTC()
	// The returned copy always reports this request. Callers read it as "the key
	// was just used" and never write it back, so it stays exact even though the
	// persisted column is throttled and may trail it by up to one window.
	key.LastUsedAt = &now
	if err := s.lastUsed.mark(lastUsedAPIKeyKey(key.ID), func() error {
		return s.db.Model(&APIKey{}).Where("id = ?", key.ID).Update("last_used_at", now).Error
	}); err != nil {
		// last_used_at is display-only, so a failed write must not reject an
		// otherwise valid key. The failed-at state suppresses repeated attempts
		// and logs until the failure backoff expires.
		log.Printf("[tokenhub] failed to record api key last_used_at key=%s: %v", key.ID, err)
	}
	return project, publicKey(key), nil
}
