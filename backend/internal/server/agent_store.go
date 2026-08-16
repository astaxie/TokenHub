package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentStore interface {
	SaveAgent(agent Agent, card *a2a.AgentCard, instance AgentInstance, skills []AgentSkill, createdBy string) (AgentWithDetails, error)
	ListAgents() ([]AgentWithDetails, error)
	GetAgentBySlug(slug string) (AgentWithDetails, bool, error)
	GetAgentByID(id string) (AgentWithDetails, bool, error)
	DeleteAgent(id string) error
	ListAgentRevisions(agentID string) ([]AgentRevision, error)
	RestoreAgentRevision(agentID string, revisionID string, createdBy string) (AgentWithDetails, error)
	SaveAgentAccessBinding(binding AgentAccessBinding) (AgentAccessBinding, error)
	ListAgentAccessBindings(agentID string) ([]AgentAccessBinding, error)
	DeleteAgentAccessBinding(id string) error
	SaveAgentAccessGroup(group AgentAccessGroup, members []AgentAccessGroupMember) (AgentAccessGroup, error)
	ListAgentAccessGroups() ([]AgentAccessGroup, error)
	ListAgentAccessGroupMembers() ([]AgentAccessGroupMember, error)
	ReserveAgentInstance(agentID string, expiresAt time.Time) (AgentInstance, string, error)
	ReserveAgentInstanceByID(instanceID string, expiresAt time.Time) (AgentInstance, string, error)
	ReleaseAgentInstance(leaseID string) error
	GetAgentInstance(id string) (AgentInstance, bool, error)
	SetAgentInstanceHealth(id string, healthy bool, cooldownUntil *time.Time) error
	CreateAgentTask(task AgentTask) (AgentTask, error)
	UpdateAgentTask(task AgentTask, eventType string, payload any) (AgentTask, error)
	GetAgentTask(id string) (AgentTask, bool, error)
	ListAgentTasks(agentID string, projectID string, apiKeyID string, endUserID string, contextID string, state string, offset int, limit int) ([]AgentTask, int64, error)
	CreateAgentExecution(execution AgentExecution, edge AgentExecutionEdge) (AgentExecution, error)
	AdmitAgentExecutionEdge(edge AgentExecutionEdge) error
	FinishAgentExecutionEdge(id string, status string) error
	ConsumeAgentExecutionBudget(executionID string, kind string, tokens int64, costUSD float64) error
	RecordAgentUsage(record AgentUsageRecord) error
	CompleteAgentModel(record AgentUsageRecord) error
	AdmitAgentMCP(record AgentUsageRecord) error
	CompleteAgentMCP(record AgentUsageRecord) error
	FinishAgentExecution(id string, status string) error
	ListAgentExecutions(limit int) ([]AgentExecution, error)
	GetAgentExecutionDetails(id string) (AgentExecutionDetails, bool, error)
}

func (s *GormStore) SaveAgent(agent Agent, card *a2a.AgentCard, instance AgentInstance, skills []AgentSkill, createdBy string) (AgentWithDetails, error) {
	cardJSON, err := json.Marshal(card)
	if err != nil {
		return AgentWithDetails{}, NewHTTPError(400, "invalid_agent_card", "Agent Card is invalid")
	}
	if agent.ID == "" {
		agent.ID = NewID("agt")
	}
	if agent.Status == "" {
		agent.Status = StatusActive
	}
	if agent.Source == "" {
		agent.Source = agentSourceAdmin
	}
	agent.CardJSON = string(cardJSON)

	var result AgentWithDetails
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var current Agent
		find := tx.Where("slug = ?", agent.Slug).First(&current)
		if find.Error == nil {
			if current.Source == agentSourceConfig && agent.Source != agentSourceConfig {
				return NewHTTPError(409, "config_managed_agent", "Config-managed agents are read-only")
			}
			agent.ID = current.ID
			if err := tx.Model(&current).Updates(map[string]any{
				"name": agent.Name, "description": agent.Description, "version": agent.Version,
				"status": agent.Status, "source": agent.Source, "source_hash": agent.SourceHash,
				"card_json": agent.CardJSON,
			}).Error; err != nil {
				return err
			}
		} else if errors.Is(find.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(&agent).Error; err != nil {
				return err
			}
		} else {
			return find.Error
		}
		if agent.Source == agentSourceConfig {
			if err := tx.Model(&AgentInstance{}).Where("agent_id = ?", agent.ID).Updates(map[string]any{
				"status": StatusDisabled, "healthy": false,
			}).Error; err != nil {
				return err
			}
		}

		if instance.URL != "" {
			if instance.ID == "" {
				instance.ID = NewID("ains")
			}
			instance.AgentID = agent.ID
			if instance.ProtocolBinding == "" {
				instance.ProtocolBinding = string(a2a.TransportProtocolJSONRPC)
			}
			if instance.ProtocolVersion == "" {
				instance.ProtocolVersion = string(a2a.Version)
			}
			if instance.Status == "" {
				instance.Status = StatusActive
			}
			if instance.Weight <= 0 {
				instance.Weight = 1
			}
			if instance.Headers != nil {
				rawHeaders, marshalErr := json.Marshal(instance.Headers)
				if marshalErr != nil {
					return marshalErr
				}
				instance.HeadersCiphertext, err = s.encryptSecretStrict(string(rawHeaders))
				if err != nil {
					return NewHTTPError(500, "agent_credential_encryption_failed", "Agent credentials could not be encrypted")
				}
			}
			var existing AgentInstance
			lookup := tx.Where("agent_id = ? AND url = ?", agent.ID, instance.URL).First(&existing)
			if lookup.Error == nil {
				instance.ID = existing.ID
				if instance.HeadersCiphertext == "" {
					instance.HeadersCiphertext = existing.HeadersCiphertext
				}
				if err := tx.Model(&existing).Updates(instance).Error; err != nil {
					return err
				}
			} else if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
				if err := tx.Create(&instance).Error; err != nil {
					return err
				}
			} else {
				return lookup.Error
			}
		}

		if err := tx.Where("agent_id = ?", agent.ID).Delete(&AgentSkill{}).Error; err != nil {
			return err
		}
		for index := range skills {
			skills[index].AgentID = agent.ID
		}
		if len(skills) > 0 {
			if err := tx.Create(&skills).Error; err != nil {
				return err
			}
		}

		instanceJSON, err := snapshotAgentInstances(tx, agent.ID)
		if err != nil {
			return err
		}
		var revisionCount int64
		if err := tx.Model(&AgentRevision{}).Where("agent_id = ?", agent.ID).Count(&revisionCount).Error; err != nil {
			return err
		}
		revision := AgentRevision{
			ID: NewID("arev"), AgentID: agent.ID, Revision: revisionCount + 1,
			Source: agent.Source, CardJSON: agent.CardJSON, InstanceJSON: instanceJSON, CreatedBy: createdBy,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return AgentWithDetails{}, err
	}
	result, _, err = s.GetAgentByID(agent.ID)
	return result, err
}

func (s *GormStore) ListAgents() ([]AgentWithDetails, error) {
	var agents []Agent
	if err := s.db.Order("slug ASC").Find(&agents).Error; err != nil {
		return nil, err
	}
	result := make([]AgentWithDetails, 0, len(agents))
	for _, agent := range agents {
		detail, found, err := s.GetAgentByID(agent.ID)
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, detail)
		}
	}
	return result, nil
}

func (s *GormStore) GetAgentBySlug(slug string) (AgentWithDetails, bool, error) {
	var agent Agent
	err := s.db.Where("slug = ?", strings.TrimSpace(slug)).First(&agent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentWithDetails{}, false, nil
	}
	if err != nil {
		return AgentWithDetails{}, false, err
	}
	return s.loadAgentDetails(agent)
}

func (s *GormStore) GetAgentByID(id string) (AgentWithDetails, bool, error) {
	var agent Agent
	err := s.db.First(&agent, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentWithDetails{}, false, nil
	}
	if err != nil {
		return AgentWithDetails{}, false, err
	}
	return s.loadAgentDetails(agent)
}

func (s *GormStore) loadAgentDetails(agent Agent) (AgentWithDetails, bool, error) {
	var card a2a.AgentCard
	if err := json.Unmarshal([]byte(agent.CardJSON), &card); err != nil {
		return AgentWithDetails{}, false, err
	}
	var instances []AgentInstance
	if err := s.db.Where("agent_id = ?", agent.ID).Order("priority DESC, id ASC").Find(&instances).Error; err != nil {
		return AgentWithDetails{}, false, err
	}
	var skills []AgentSkill
	if err := s.db.Where("agent_id = ?", agent.ID).Order("skill_id ASC").Find(&skills).Error; err != nil {
		return AgentWithDetails{}, false, err
	}
	return AgentWithDetails{Agent: agent, Card: &card, Instances: instances, Skills: skills}, true, nil
}

func (s *GormStore) DeleteAgent(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var agent Agent
		if err := tx.First(&agent, "id = ?", id).Error; err != nil {
			return err
		}
		if agent.Source == agentSourceConfig {
			return NewHTTPError(409, "config_managed_agent", "Config-managed agents are read-only")
		}
		if err := tx.Where("instance_id IN (?)", tx.Model(&AgentInstance{}).Select("id").Where("agent_id = ?", id)).Delete(&AgentInstanceLease{}).Error; err != nil {
			return err
		}
		for _, model := range []any{&AgentSkill{}, &AgentInstance{}, &AgentAccessBinding{}} {
			if err := tx.Where("agent_id = ?", id).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&agent).Error
	})
}

func (s *GormStore) ListAgentRevisions(agentID string) ([]AgentRevision, error) {
	var revisions []AgentRevision
	err := s.db.Where("agent_id = ?", agentID).Order("revision DESC").Find(&revisions).Error
	return revisions, err
}

func (s *GormStore) RestoreAgentRevision(agentID string, revisionID string, createdBy string) (AgentWithDetails, error) {
	var revision AgentRevision
	if err := s.db.Where("id = ? AND agent_id = ?", revisionID, agentID).First(&revision).Error; err != nil {
		return AgentWithDetails{}, err
	}
	current, found, err := s.GetAgentByID(agentID)
	if err != nil || !found {
		return AgentWithDetails{}, err
	}
	if current.Source == agentSourceConfig {
		return AgentWithDetails{}, NewHTTPError(409, "config_managed_agent", "Config-managed agents are read-only")
	}
	var card a2a.AgentCard
	if err := json.Unmarshal([]byte(revision.CardJSON), &card); err != nil {
		return AgentWithDetails{}, err
	}
	current.Name, current.Description, current.Version = card.Name, card.Description, card.Version
	var snapshots []agentInstanceSnapshot
	if revision.InstanceJSON != "" {
		if err := json.Unmarshal([]byte(revision.InstanceJSON), &snapshots); err != nil {
			return AgentWithDetails{}, err
		}
	}
	restored, err := s.SaveAgent(current.Agent, &card, AgentInstance{}, agentSkillsFromCard(current.ID, &card), createdBy)
	if err != nil {
		return AgentWithDetails{}, err
	}
	if revision.InstanceJSON == "" {
		return restored, nil
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&AgentInstance{}).Where("agent_id = ?", current.ID).Updates(map[string]any{
			"status": StatusDisabled, "healthy": false,
		}).Error; err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			instance := snapshot.instance(current.ID)
			var existing AgentInstance
			lookup := tx.First(&existing, "id = ? AND agent_id = ?", instance.ID, current.ID)
			if lookup.Error == nil {
				instance.CreatedAt = existing.CreatedAt
				instance.ActiveRequests = existing.ActiveRequests
				if err := tx.Save(&instance).Error; err != nil {
					return err
				}
				continue
			}
			if !errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
				return lookup.Error
			}
			if err := tx.Create(&instance).Error; err != nil {
				return err
			}
		}
		var latest AgentRevision
		if err := tx.Where("agent_id = ?", restored.ID).Order("revision DESC").First(&latest).Error; err != nil {
			return err
		}
		return tx.Model(&latest).Update("instance_json", revision.InstanceJSON).Error
	})
	if err != nil {
		return AgentWithDetails{}, err
	}
	restored, _, err = s.GetAgentByID(current.ID)
	return restored, err
}

type agentInstanceSnapshot struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	URL                   string     `json:"url"`
	ProtocolBinding       string     `json:"protocol_binding"`
	ProtocolVersion       string     `json:"protocol_version"`
	Status                string     `json:"status"`
	Healthy               bool       `json:"healthy"`
	Priority              int        `json:"priority"`
	Weight                int        `json:"weight"`
	MaxConcurrency        int64      `json:"max_concurrency"`
	FixedCostUSD          float64    `json:"fixed_cost_usd"`
	AllowedForwardHeaders []string   `json:"allowed_forward_headers,omitempty"`
	HeadersCiphertext     string     `json:"headers_ciphertext,omitempty"`
	FailureCount          int        `json:"failure_count"`
	CooldownUntil         *time.Time `json:"cooldown_until,omitempty"`
}

func snapshotAgentInstances(tx *gorm.DB, agentID string) (string, error) {
	var instances []AgentInstance
	if err := tx.Where("agent_id = ?", agentID).Order("priority DESC, id ASC").Find(&instances).Error; err != nil {
		return "", err
	}
	snapshots := make([]agentInstanceSnapshot, 0, len(instances))
	for _, instance := range instances {
		snapshots = append(snapshots, agentInstanceSnapshot{
			ID: instance.ID, Name: instance.Name, URL: instance.URL,
			ProtocolBinding: instance.ProtocolBinding, ProtocolVersion: instance.ProtocolVersion,
			Status: instance.Status, Healthy: instance.Healthy, Priority: instance.Priority, Weight: instance.Weight,
			MaxConcurrency: instance.MaxConcurrency, FixedCostUSD: instance.FixedCostUSD,
			AllowedForwardHeaders: instance.AllowedForwardHeaders, HeadersCiphertext: instance.HeadersCiphertext,
			FailureCount: instance.FailureCount, CooldownUntil: instance.CooldownUntil,
		})
	}
	data, err := json.Marshal(snapshots)
	return string(data), err
}

func (snapshot agentInstanceSnapshot) instance(agentID string) AgentInstance {
	return AgentInstance{
		ID: snapshot.ID, AgentID: agentID, Name: snapshot.Name, URL: snapshot.URL,
		ProtocolBinding: snapshot.ProtocolBinding, ProtocolVersion: snapshot.ProtocolVersion,
		Status: snapshot.Status, Healthy: snapshot.Healthy, Priority: snapshot.Priority, Weight: snapshot.Weight,
		MaxConcurrency: snapshot.MaxConcurrency, FixedCostUSD: snapshot.FixedCostUSD,
		AllowedForwardHeaders: snapshot.AllowedForwardHeaders, HeadersCiphertext: snapshot.HeadersCiphertext,
		FailureCount: snapshot.FailureCount, CooldownUntil: snapshot.CooldownUntil,
	}
}

func (s *GormStore) SaveAgentAccessBinding(binding AgentAccessBinding) (AgentAccessBinding, error) {
	if binding.ID == "" {
		binding.ID = NewID("aab")
	}
	if binding.Status == "" {
		binding.Status = StatusActive
	}
	binding.Effect = strings.ToLower(strings.TrimSpace(binding.Effect))
	if binding.Effect != agentBindingAllow && binding.Effect != agentBindingDeny {
		return AgentAccessBinding{}, NewHTTPError(400, "invalid_agent_access_effect", "effect must be allow or deny")
	}
	err := s.db.Save(&binding).Error
	return binding, err
}

func (s *GormStore) ListAgentAccessBindings(agentID string) ([]AgentAccessBinding, error) {
	var bindings []AgentAccessBinding
	db := s.db.Order("created_at ASC")
	if agentID != "" {
		db = db.Where("agent_id = ?", agentID)
	}
	err := db.Find(&bindings).Error
	return bindings, err
}

func (s *GormStore) DeleteAgentAccessBinding(id string) error {
	return s.db.Delete(&AgentAccessBinding{}, "id = ?", id).Error
}

func (s *GormStore) SaveAgentAccessGroup(group AgentAccessGroup, members []AgentAccessGroupMember) (AgentAccessGroup, error) {
	if group.ID == "" {
		group.ID = NewID("aag")
	}
	if group.Status == "" {
		group.Status = StatusActive
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&group).Error; err != nil {
			return err
		}
		if err := tx.Where("group_id = ?", group.ID).Delete(&AgentAccessGroupMember{}).Error; err != nil {
			return err
		}
		for index := range members {
			members[index].GroupID = group.ID
		}
		if len(members) > 0 {
			return tx.Create(&members).Error
		}
		return nil
	})
	return group, err
}

func (s *GormStore) ListAgentAccessGroups() ([]AgentAccessGroup, error) {
	var groups []AgentAccessGroup
	err := s.db.Order("name ASC").Find(&groups).Error
	return groups, err
}

func (s *GormStore) ListAgentAccessGroupMembers() ([]AgentAccessGroupMember, error) {
	var members []AgentAccessGroupMember
	err := s.db.Find(&members).Error
	return members, err
}

func (s *GormStore) ReserveAgentInstance(agentID string, expiresAt time.Time) (AgentInstance, string, error) {
	now := time.Now().UTC()
	expiresAt = normalizeAgentLeaseExpiry(now, expiresAt)
	var candidates []AgentInstance
	err := s.db.Where("agent_id = ? AND status = ? AND (healthy = ? OR cooldown_until <= ?)", agentID, StatusActive, true, now).
		Order("priority DESC, healthy DESC, failure_count ASC, updated_at ASC, id ASC").Find(&candidates).Error
	if err != nil {
		return AgentInstance{}, "", err
	}
	if len(candidates) == 0 {
		return AgentInstance{}, "", NewHTTPError(503, "agent_unavailable", "No healthy Agent instance is available")
	}
	for start := 0; start < len(candidates); {
		priority := candidates[start].Priority
		end := start
		for end < len(candidates) && candidates[end].Priority == priority {
			end++
		}
		tier := append([]AgentInstance(nil), candidates[start:end]...)
		for len(tier) > 0 {
			index := weightedAgentInstanceIndex(tier)
			candidate := tier[index]
			tier = append(tier[:index], tier[index+1:]...)
			leaseID, reserved, reserveErr := s.tryReserveAgentInstance(candidate.ID, agentID, now, expiresAt, true)
			if reserveErr != nil {
				return AgentInstance{}, "", reserveErr
			}
			if reserved {
				instance, found, hydrateErr := s.GetAgentInstance(candidate.ID)
				if hydrateErr != nil || !found {
					_ = s.ReleaseAgentInstance(leaseID)
					if hydrateErr != nil {
						return AgentInstance{}, "", hydrateErr
					}
					return AgentInstance{}, "", NewHTTPError(503, "agent_unavailable", "Agent instance is unavailable")
				}
				return instance, leaseID, nil
			}
		}
		start = end
	}
	return AgentInstance{}, "", NewHTTPError(503, "agent_concurrency_exhausted", "All Agent instances reached their concurrency limit")
}

func (s *GormStore) ReserveAgentInstanceByID(instanceID string, expiresAt time.Time) (AgentInstance, string, error) {
	now := time.Now().UTC()
	expiresAt = normalizeAgentLeaseExpiry(now, expiresAt)
	leaseID, reserved, err := s.tryReserveAgentInstance(instanceID, "", now, expiresAt, false)
	if err != nil {
		return AgentInstance{}, "", err
	}
	if !reserved {
		return AgentInstance{}, "", NewHTTPError(503, "agent_concurrency_exhausted", "Agent instance reached its concurrency limit")
	}
	instance, found, err := s.GetAgentInstance(instanceID)
	if err != nil || !found {
		_ = s.ReleaseAgentInstance(leaseID)
		if err != nil {
			return AgentInstance{}, "", err
		}
		return AgentInstance{}, "", NewHTTPError(503, "agent_unavailable", "Agent instance is unavailable")
	}
	return instance, leaseID, nil
}

func (s *GormStore) tryReserveAgentInstance(instanceID string, agentID string, now time.Time, expiresAt time.Time, requireHealthy bool) (string, bool, error) {
	leaseID := NewID("alease")
	reserved := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := reconcileExpiredAgentInstanceLeases(tx, instanceID, now); err != nil {
			return err
		}
		query := tx.Model(&AgentInstance{}).Where(
			"id = ? AND status = ? AND (max_concurrency <= 0 OR active_requests < max_concurrency)",
			instanceID, StatusActive,
		)
		if agentID != "" {
			query = query.Where("agent_id = ?", agentID)
		}
		if requireHealthy {
			query = query.Where("healthy = ? OR cooldown_until <= ?", true, now)
		}
		result := query.UpdateColumn("active_requests", gorm.Expr("active_requests + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		lease := AgentInstanceLease{ID: leaseID, InstanceID: instanceID, ExpiresAt: expiresAt}
		if err := tx.Create(&lease).Error; err != nil {
			return err
		}
		reserved = true
		return nil
	})
	return leaseID, reserved, err
}

func (s *GormStore) ReleaseAgentInstance(leaseID string) error {
	if strings.TrimSpace(leaseID) == "" {
		return nil
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var lease AgentInstanceLease
		if err := tx.First(&lease, "id = ?", leaseID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		result := tx.Delete(&AgentInstanceLease{}, "id = ?", lease.ID)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return decrementAgentInstanceRequests(tx, lease.InstanceID)
	})
}

func reconcileExpiredAgentInstanceLeases(tx *gorm.DB, instanceID string, now time.Time) error {
	var leases []AgentInstanceLease
	if err := tx.Where("instance_id = ?", instanceID).Find(&leases).Error; err != nil {
		return err
	}
	for _, lease := range leases {
		if lease.ExpiresAt.After(now) {
			continue
		}
		result := tx.Delete(&AgentInstanceLease{}, "id = ?", lease.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if err := decrementAgentInstanceRequests(tx, lease.InstanceID); err != nil {
				return err
			}
		}
	}
	return nil
}

func decrementAgentInstanceRequests(tx *gorm.DB, instanceID string) error {
	return tx.Model(&AgentInstance{}).Where("id = ?", instanceID).UpdateColumn(
		"active_requests", gorm.Expr("CASE WHEN active_requests > 0 THEN active_requests - 1 ELSE 0 END"),
	).Error
}

func normalizeAgentLeaseExpiry(now time.Time, expiresAt time.Time) time.Time {
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return now.Add(15 * time.Minute)
	}
	return expiresAt.UTC()
}

func weightedAgentInstanceIndex(instances []AgentInstance) int {
	totalWeight := 0
	for _, instance := range instances {
		totalWeight += max(1, instance.Weight)
	}
	choice := rand.IntN(totalWeight)
	for index, instance := range instances {
		choice -= max(1, instance.Weight)
		if choice < 0 {
			return index
		}
	}
	return len(instances) - 1
}

func (s *GormStore) GetAgentInstance(id string) (AgentInstance, bool, error) {
	var instance AgentInstance
	err := s.db.First(&instance, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentInstance{}, false, nil
	}
	if err != nil {
		return AgentInstance{}, false, err
	}
	instance, err = s.hydrateAgentInstance(instance)
	return instance, err == nil, err
}

func (s *GormStore) hydrateAgentInstance(instance AgentInstance) (AgentInstance, error) {
	instance.Headers = map[string]string{}
	if instance.HeadersCiphertext == "" {
		return instance, nil
	}
	plain := s.decryptSecret(instance.HeadersCiphertext)
	if plain == "" || json.Unmarshal([]byte(plain), &instance.Headers) != nil {
		return AgentInstance{}, NewHTTPError(500, "agent_credentials_unavailable", "Agent credentials could not be decrypted")
	}
	return instance, nil
}

func (s *GormStore) SetAgentInstanceHealth(id string, healthy bool, cooldownUntil *time.Time) error {
	updates := map[string]any{"healthy": healthy, "cooldown_until": cooldownUntil}
	if healthy {
		updates["failure_count"] = 0
	} else {
		updates["failure_count"] = gorm.Expr("failure_count + 1")
	}
	return s.db.Model(&AgentInstance{}).Where("id = ?", id).Updates(updates).Error
}

func (s *GormStore) CreateAgentTask(task AgentTask) (AgentTask, error) {
	if task.ID == "" {
		task.ID = NewID("atask")
	}
	err := s.db.Create(&task).Error
	return task, err
}

func (s *GormStore) UpdateAgentTask(task AgentTask, eventType string, payload any) (AgentTask, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var current AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", task.ID).Error; err != nil {
			return err
		}
		current.State = task.State
		current.ContextID = task.ContextID
		current.ExecutionStepID = task.ExecutionStepID
		current.SnapshotJSON = task.SnapshotJSON
		current.LastEventSeq++
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		if eventType != "" {
			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			event := AgentTaskEvent{TaskID: current.ID, Sequence: current.LastEventSeq, EventType: eventType, PayloadJSON: string(data)}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		}
		task = current
		return nil
	})
	return task, err
}

func (s *GormStore) GetAgentTask(id string) (AgentTask, bool, error) {
	var task AgentTask
	err := s.db.First(&task, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTask{}, false, nil
	}
	return task, err == nil, err
}

func (s *GormStore) ListAgentTasks(agentID string, projectID string, apiKeyID string, endUserID string, contextID string, state string, offset int, limit int) ([]AgentTask, int64, error) {
	db := s.db.Model(&AgentTask{}).Where(
		"agent_id = ? AND project_id = ? AND api_key_id = ? AND end_user_id = ?",
		agentID, projectID, apiKeyID, endUserID,
	)
	if contextID != "" {
		db = db.Where("context_id = ?", contextID)
	}
	if state != "" {
		db = db.Where("state = ?", state)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tasks []AgentTask
	err := db.Order("updated_at DESC, id ASC").Offset(offset).Limit(limit).Find(&tasks).Error
	return tasks, total, err
}

func (s *GormStore) CreateAgentExecution(execution AgentExecution, edge AgentExecutionEdge) (AgentExecution, error) {
	if execution.ID == "" {
		execution.ID = NewID("aexec")
	}
	if edge.ID == "" {
		edge.ID = NewID("astep")
	}
	edge.ExecutionID = execution.ID
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(context.Background()).Create(&execution).Error; err != nil {
			return err
		}
		return tx.Create(&edge).Error
	})
	return execution, err
}

func (s *GormStore) AdmitAgentExecutionEdge(edge AgentExecutionEdge) error {
	if edge.ID == "" {
		edge.ID = NewID("astep")
	}
	var budgetErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var execution AgentExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&execution, "id = ?", edge.ExecutionID).Error; err != nil {
			return err
		}
		if execution.Status != "running" {
			return NewHTTPError(409, "agent_execution_not_running", "Agent execution is no longer running")
		}
		markBudgetExceeded := func(err error) error {
			budgetErr = err
			now := time.Now().UTC()
			return tx.Model(&execution).Updates(map[string]any{"status": "budget_exceeded", "completed_at": &now}).Error
		}
		if execution.Deadline != nil && !execution.Deadline.After(time.Now().UTC()) {
			return markBudgetExceeded(NewHTTPError(429, "agent_runtime_budget_exceeded", "Agent execution runtime budget was exceeded"))
		}
		if execution.AgentHops+1 > execution.MaxAgentHops {
			return markBudgetExceeded(NewHTTPError(429, "agent_hop_budget_exceeded", "Agent hop budget was exceeded"))
		}
		var active int64
		if err := tx.Model(&AgentExecutionEdge{}).Where("execution_id = ? AND status = ?", edge.ExecutionID, "running").Count(&active).Error; err != nil {
			return err
		}
		if active >= execution.MaxConcurrency {
			return markBudgetExceeded(NewHTTPError(429, "agent_concurrency_budget_exceeded", "Agent execution concurrency budget was exceeded"))
		}
		if err := tx.Model(&execution).Update("agent_hops", gorm.Expr("agent_hops + 1")).Error; err != nil {
			return err
		}
		return tx.Create(&edge).Error
	})
	if err != nil {
		return err
	}
	return budgetErr
}

func (s *GormStore) FinishAgentExecutionEdge(id string, status string) error {
	return s.db.Model(&AgentExecutionEdge{}).Where("id = ? AND status = ?", id, "running").Updates(map[string]any{"status": status}).Error
}

func (s *GormStore) ConsumeAgentExecutionBudget(executionID string, kind string, tokens int64, costUSD float64) error {
	var budgetErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var execution AgentExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&execution, "id = ?", executionID).Error; err != nil {
			return err
		}
		if execution.Status != "running" {
			return NewHTTPError(409, "agent_execution_not_running", "Agent execution is no longer running")
		}
		markBudgetExceeded := func(err error) error {
			budgetErr = err
			now := time.Now().UTC()
			return tx.Model(&execution).Updates(map[string]any{"status": "budget_exceeded", "completed_at": &now}).Error
		}
		if execution.Deadline != nil && !execution.Deadline.After(time.Now().UTC()) {
			return markBudgetExceeded(NewHTTPError(429, "agent_runtime_budget_exceeded", "Agent execution runtime budget was exceeded"))
		}
		updates := map[string]any{}
		switch kind {
		case "model":
			if execution.ModelCalls+1 > execution.MaxModelCalls {
				return markBudgetExceeded(NewHTTPError(429, "agent_model_call_budget_exceeded", "Agent model call budget was exceeded"))
			}
			updates["model_calls"] = gorm.Expr("model_calls + 1")
		case "mcp":
			if execution.MCPCalls+1 > execution.MaxMCPCalls {
				return markBudgetExceeded(NewHTTPError(429, "agent_mcp_call_budget_exceeded", "Agent MCP call budget was exceeded"))
			}
			updates["mcp_calls"] = gorm.Expr("mcp_calls + 1")
		case "usage":
			nextTokens := execution.Tokens + tokens
			nextCost := execution.CostUSD + costUSD
			updates["tokens"] = nextTokens
			updates["cost_usd"] = nextCost
			if nextTokens > execution.MaxTokens {
				budgetErr = NewHTTPError(429, "agent_token_budget_exceeded", "Agent token budget was exceeded")
			}
			if nextCost > execution.MaxCostUSD && budgetErr == nil {
				budgetErr = NewHTTPError(429, "agent_cost_budget_exceeded", "Agent cost budget was exceeded")
			}
			if budgetErr != nil {
				now := time.Now().UTC()
				updates["status"] = "budget_exceeded"
				updates["completed_at"] = &now
			}
			return tx.Model(&execution).Updates(updates).Error
		case "reserve_cost":
			if execution.CostUSD+costUSD > execution.MaxCostUSD {
				return markBudgetExceeded(NewHTTPError(429, "agent_cost_budget_exceeded", "Agent cost budget was exceeded"))
			}
			updates["cost_usd"] = gorm.Expr("cost_usd + ?", costUSD)
		default:
			return NewHTTPError(400, "invalid_agent_budget_kind", "Agent budget kind is invalid")
		}
		if execution.Tokens+tokens > execution.MaxTokens {
			return markBudgetExceeded(NewHTTPError(429, "agent_token_budget_exceeded", "Agent token budget was exceeded"))
		}
		updates["tokens"] = gorm.Expr("tokens + ?", tokens)
		if kind != "reserve_cost" {
			updates["cost_usd"] = gorm.Expr("cost_usd + ?", costUSD)
		}
		return tx.Model(&execution).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	return budgetErr
}

func (s *GormStore) RecordAgentUsage(record AgentUsageRecord) error {
	if record.ID == "" {
		record.ID = NewID("ausg")
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
}

func (s *GormStore) CompleteAgentModel(record AgentUsageRecord) error {
	var budgetErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var execution AgentExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&execution, "id = ?", record.ExecutionID).Error; err != nil {
			return err
		}
		var existing AgentUsageRecord
		err := tx.Where("step_id = ?", record.StepID).First(&existing).Error
		if err == nil {
			if existing.ExecutionID != record.ExecutionID || existing.AgentID != record.AgentID {
				return NewHTTPError(409, "agent_model_step_conflict", "Model step_id is already used by another execution")
			}
			if existing.Tokens != record.Tokens || existing.CostUSD != record.CostUSD {
				return NewHTTPError(409, "agent_model_usage_conflict", "Completed model usage cannot be changed")
			}
			if existing.SourceType == "model_exceeded" {
				return NewHTTPError(429, "agent_execution_budget_exceeded", "Agent execution budget was exceeded")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		nextTokens := execution.Tokens + record.Tokens
		nextCost := execution.CostUSD + record.CostUSD
		updates := map[string]any{"tokens": nextTokens, "cost_usd": nextCost}
		record.SourceType = "model"
		if nextTokens > execution.MaxTokens {
			budgetErr = NewHTTPError(429, "agent_token_budget_exceeded", "Agent token budget was exceeded")
		}
		if nextCost > execution.MaxCostUSD && budgetErr == nil {
			budgetErr = NewHTTPError(429, "agent_cost_budget_exceeded", "Agent cost budget was exceeded")
		}
		if budgetErr != nil {
			now := time.Now().UTC()
			updates["status"] = "budget_exceeded"
			updates["completed_at"] = &now
			record.SourceType = "model_exceeded"
		}
		if record.ID == "" {
			record.ID = NewID("ausg")
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return tx.Model(&execution).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	return budgetErr
}

func (s *GormStore) AdmitAgentMCP(record AgentUsageRecord) error {
	if record.ID == "" {
		record.ID = NewID("ausg")
	}
	record.SourceType = "mcp_pending"
	record.Tokens = 0
	record.CostUSD = 0
	var budgetErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var execution AgentExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&execution, "id = ?", record.ExecutionID).Error; err != nil {
			return err
		}
		if execution.Status != "running" {
			return NewHTTPError(409, "agent_execution_not_running", "Agent execution is no longer running")
		}
		markBudgetExceeded := func(err error) error {
			budgetErr = err
			now := time.Now().UTC()
			return tx.Model(&execution).Updates(map[string]any{"status": "budget_exceeded", "completed_at": &now}).Error
		}
		var existing AgentUsageRecord
		err := tx.Where("step_id = ?", record.StepID).First(&existing).Error
		if err == nil {
			if existing.ExecutionID != record.ExecutionID || existing.AgentID != record.AgentID {
				return NewHTTPError(409, "agent_mcp_step_conflict", "MCP step_id is already used by another execution")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if execution.Deadline != nil && !execution.Deadline.After(time.Now().UTC()) {
			return markBudgetExceeded(NewHTTPError(429, "agent_runtime_budget_exceeded", "Agent execution runtime budget was exceeded"))
		}
		if execution.MCPCalls+1 > execution.MaxMCPCalls {
			return markBudgetExceeded(NewHTTPError(429, "agent_mcp_call_budget_exceeded", "Agent MCP call budget was exceeded"))
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return tx.Model(&execution).Update("mcp_calls", gorm.Expr("mcp_calls + 1")).Error
	})
	if err != nil {
		return err
	}
	return budgetErr
}

func (s *GormStore) CompleteAgentMCP(record AgentUsageRecord) error {
	var budgetErr error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var execution AgentExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&execution, "id = ?", record.ExecutionID).Error; err != nil {
			return err
		}
		var existing AgentUsageRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("step_id = ?", record.StepID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return NewHTTPError(409, "agent_mcp_not_admitted", "MCP step must be admitted before completion")
			}
			return err
		}
		if existing.ExecutionID != record.ExecutionID || existing.AgentID != record.AgentID {
			return NewHTTPError(409, "agent_mcp_step_conflict", "MCP step_id is already used by another execution")
		}
		if existing.SourceType == "mcp" || existing.SourceType == "mcp_exceeded" {
			if existing.Tokens != record.Tokens || existing.CostUSD != record.CostUSD || existing.TaskID != record.TaskID {
				return NewHTTPError(409, "agent_mcp_usage_conflict", "Completed MCP usage cannot be changed")
			}
			if existing.SourceType == "mcp_exceeded" {
				return NewHTTPError(429, "agent_execution_budget_exceeded", "Agent execution budget was exceeded")
			}
			return nil
		}
		if existing.SourceType != "mcp_pending" {
			return NewHTTPError(409, "agent_mcp_usage_conflict", "MCP usage state is invalid")
		}
		nextTokens := execution.Tokens + record.Tokens
		nextCost := execution.CostUSD + record.CostUSD
		updates := map[string]any{"tokens": nextTokens, "cost_usd": nextCost}
		sourceType := "mcp"
		if nextTokens > execution.MaxTokens {
			budgetErr = NewHTTPError(429, "agent_token_budget_exceeded", "Agent token budget was exceeded")
		}
		if nextCost > execution.MaxCostUSD && budgetErr == nil {
			budgetErr = NewHTTPError(429, "agent_cost_budget_exceeded", "Agent cost budget was exceeded")
		}
		if budgetErr != nil {
			now := time.Now().UTC()
			updates["status"] = "budget_exceeded"
			updates["completed_at"] = &now
			sourceType = "mcp_exceeded"
		}
		if err := tx.Model(&execution).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]any{
			"source_type": sourceType, "task_id": record.TaskID, "tokens": record.Tokens, "cost_usd": record.CostUSD,
		}).Error
	})
	if err != nil {
		return err
	}
	return budgetErr
}

func (s *GormStore) FinishAgentExecution(id string, status string) error {
	now := time.Now().UTC()
	return s.db.Model(&AgentExecution{}).Where("id = ? AND status = ?", id, "running").Updates(map[string]any{
		"status": status, "completed_at": &now,
	}).Error
}

func (s *GormStore) ListAgentExecutions(limit int) ([]AgentExecution, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var executions []AgentExecution
	err := s.db.Order("created_at DESC, id DESC").Limit(limit).Find(&executions).Error
	return executions, err
}

func (s *GormStore) GetAgentExecutionDetails(id string) (AgentExecutionDetails, bool, error) {
	var execution AgentExecution
	if err := s.db.First(&execution, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AgentExecutionDetails{}, false, nil
		}
		return AgentExecutionDetails{}, false, err
	}
	details := AgentExecutionDetails{AgentExecution: execution}
	if err := s.db.Where("execution_id = ?", id).Order("created_at ASC, id ASC").Find(&details.Edges).Error; err != nil {
		return AgentExecutionDetails{}, false, err
	}
	if err := s.db.Where("execution_id = ?", id).Order("created_at ASC, id ASC").Find(&details.Usage).Error; err != nil {
		return AgentExecutionDetails{}, false, err
	}
	if err := s.db.Where("execution_id = ?", id).Order("created_at ASC, id ASC").Find(&details.Tasks).Error; err != nil {
		return AgentExecutionDetails{}, false, err
	}
	return details, true, nil
}

func agentSkillsFromCard(agentID string, card *a2a.AgentCard) []AgentSkill {
	if card == nil {
		return nil
	}
	skills := make([]AgentSkill, 0, len(card.Skills))
	for _, skill := range card.Skills {
		skills = append(skills, AgentSkill{
			AgentID: agentID, SkillID: skill.ID, Name: skill.Name, Description: skill.Description,
			InputModes: skill.InputModes, OutputModes: skill.OutputModes, Examples: skill.Examples,
		})
	}
	return skills
}
