package server

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) authorizeAdminUser(w http.ResponseWriter, r *http.Request) (AdminUser, bool) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, r, NewHTTPError(401, "invalid_admin_token", "Invalid admin token"))
		return AdminUser{}, false
	}
	if token == strings.TrimSpace(s.config.AdminToken) {
		users := s.store.ListAdminUsers()
		if len(users) > 0 {
			return users[0], true
		}
		return AdminUser{ID: "dev_admin", Username: "dev_admin", Name: "开发管理员", Email: "admin@tokenhub.local", Role: "admin", Status: StatusActive}, true
	}
	user, ok := s.store.ValidateAdminSession(token)
	if !ok {
		writeError(w, r, NewHTTPError(401, "invalid_admin_token", "Invalid admin token"))
		return AdminUser{}, false
	}
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, resource string, method string) (AdminUser, bool) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return AdminUser{}, false
	}
	if !canAdmin(user.Role, resource, method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return AdminUser{}, false
	}
	return user, true
}

func canAdmin(role string, resource string, method string) bool {
	role = normalizeAdminRole(role)
	if role == "" {
		role = "user"
	}
	resource = strings.ToLower(strings.TrimSpace(resource))
	write := method != http.MethodGet
	switch role {
	case "admin", "system_admin":
		return true
	case "security_admin":
		if resource == "backup" {
			return false
		}
		if write {
			return resource == "alert" || resource == "security" || resource == "audit" || resource == "admin_audit" || resource == "approval"
		}
		return resource == "overview" || resource == "usage" || resource == "audit" || resource == "admin_audit" || resource == "alert" || resource == "security" || resource == "approval"
	case "team_leader":
		if resource == "backup" {
			return false
		}
		if write {
			return resource == "identity" || resource == "project" || resource == "api_key" || resource == "approval" || resource == "playground" || resource == "quota"
		}
		return resource == "overview" || resource == "project" || resource == "api_key" || resource == "usage" || resource == "audit" || resource == "identity" || resource == "approval" || resource == "quota"
	case "user":
		if write {
			return resource == "api_key" || resource == "playground"
		}
		return resource == "overview" || resource == "project" || resource == "api_key" || resource == "usage" || resource == "audit" || resource == "model" || resource == "playground"
	default:
		return !write && resource == "overview"
	}
}

func adminRoleMatches(actual string, required string) bool {
	actual = normalizeAdminRole(actual)
	required = normalizeAdminRole(required)
	if actual == "admin" || actual == "system_admin" {
		return true
	}
	return actual == required
}

func normalizeAdminRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "security":
		return "security_admin"
	case "project_admin", "teamlead":
		return "team_leader"
	case "viewer", "readonly", "read_only", "member":
		return "user"
	default:
		return role
	}
}

func isPlatformAdminRole(role string) bool {
	role = normalizeAdminRole(role)
	return role == "admin" || role == "system_admin"
}

func (s *Server) canViewGlobalOperations(user AdminUser) bool {
	role := normalizeAdminRole(user.Role)
	return isPlatformAdminRole(role) || role == "security_admin"
}

func (s *Server) filterProjectsForUser(user AdminUser, projects []Project) []Project {
	if s.canViewGlobalOperations(user) {
		return projects
	}
	memberships := s.store.ListResources("project-members")
	activeTeams := s.activeTeamIDSet()
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		if projectAccessRole(user, project, memberships, activeTeams) != "" {
			out = append(out, project)
		}
	}
	return out
}

func (s *Server) canAccessProject(user AdminUser, project Project) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	return projectAccessRole(user, project, s.store.ListResources("project-members"), s.activeTeamIDSet()) != ""
}

func (s *Server) canManageProject(user AdminUser, project Project) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	return projectAccessRoleRank(projectAccessRole(user, project, s.store.ListResources("project-members"), s.activeTeamIDSet())) >= projectAccessRoleRank("maintainer")
}

func (s *Server) activeTeamIDSet() map[string]bool {
	activeTeams := map[string]bool{}
	for _, team := range s.store.ListResources("teams") {
		if team.Status == "" || team.Status == StatusActive {
			activeTeams[team.ID] = true
		}
	}
	return activeTeams
}

func projectAccessRole(user AdminUser, project Project, memberships []AdminResource, activeTeams map[string]bool) string {
	if strings.TrimSpace(project.OwnerUserID) == user.ID {
		return "owner"
	}
	role := ""
	for _, member := range memberships {
		if !projectMemberMatches(member, project.ID, user.ID) {
			continue
		}
		role = higherProjectAccessRole(role, normalizeProjectAccessRole(memberRole(member)))
	}
	for _, link := range project.Teams {
		if !activeTeams[link.TeamID] || !userHasTeam(user, link.TeamID) {
			continue
		}
		candidate := normalizeProjectAccessRole(link.Role)
		if candidate == "team_leader" {
			if normalizeAdminRole(user.Role) != "team_leader" {
				continue
			}
			candidate = "maintainer"
		}
		role = higherProjectAccessRole(role, candidate)
	}
	return role
}

func normalizeProjectAccessRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "owner", "maintainer", "developer", "viewer", "team_leader":
		return role
	default:
		return ""
	}
}

func validProjectTeamRole(role string) bool {
	switch normalizeProjectAccessRole(role) {
	case "maintainer", "developer", "viewer":
		return true
	default:
		return false
	}
}

func projectAccessRoleRank(role string) int {
	switch normalizeProjectAccessRole(role) {
	case "owner":
		return 4
	case "maintainer":
		return 3
	case "developer":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func higherProjectAccessRole(left string, right string) string {
	if projectAccessRoleRank(right) > projectAccessRoleRank(left) {
		return normalizeProjectAccessRole(right)
	}
	return normalizeProjectAccessRole(left)
}

func (s *Server) projectQuotaPolicy(project Project) (AdminResource, bool) {
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		if quota, err := s.findResource("quota-policies", project.DefaultQuotaRef); err == nil {
			return quota, true
		}
	}
	for _, quota := range s.store.ListResources("quota-policies") {
		scope := strings.ToLower(strings.TrimSpace(stringField(quota.Fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(quota.Fields, "scope_type")))
		}
		scopeID := strings.TrimSpace(stringField(quota.Fields, "scope_id"))
		if scope == "project" && scopeID == project.ID {
			return quota, true
		}
	}
	return AdminResource{}, false
}

func (s *Server) linkProjectQuotaPolicy(quota AdminResource, payload map[string]any) error {
	projectID := strings.TrimSpace(stringFromPayload(payload, "project_id"))
	if projectID == "" {
		fields := fieldsFromPayload(payload["fields"])
		scope := strings.ToLower(strings.TrimSpace(stringField(fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(fields, "scope_type")))
		}
		if scope == "project" {
			projectID = strings.TrimSpace(stringField(fields, "scope_id"))
		}
	}
	if projectID == "" {
		return nil
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		return NewHTTPError(404, "project_not_found", "Project not found")
	}
	if project.DefaultQuotaRef == quota.ID {
		return nil
	}
	project.DefaultQuotaRef = quota.ID
	_, err := s.store.UpdateProject(project.ID, Project{
		Name:            project.Name,
		TeamID:          project.TeamID,
		OwnerUserID:     project.OwnerUserID,
		CostCenter:      project.CostCenter,
		Status:          project.Status,
		DefaultQuotaRef: quota.ID,
	})
	return err
}

func (s *Server) usageSummaryForUser(user AdminUser) map[string]any {
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	logs := s.filterRequestLogsForUser(user, s.store.ListRequestLogs())
	var input, cachedInput, cacheWrite, output, reasoningOutput, total int64
	var cost float64
	errorsCount := 0
	for _, record := range records {
		input += record.InputTokens
		cachedInput += record.CachedInputTokens
		cacheWrite += record.CacheWriteTokens
		output += record.OutputTokens
		reasoningOutput += record.ReasoningTokens
		total += record.TotalTokens
		cost += record.CostUSD
	}
	for _, log := range logs {
		if isPlaygroundRequestLog(log) {
			continue
		}
		if log.StatusCode >= 400 {
			errorsCount++
		}
	}
	return map[string]any{
		"request_count":            billableRequestLogCount(logs),
		"usage_record_count":       len(records),
		"input_tokens":             input,
		"cached_input_tokens":      cachedInput,
		"cache_write_input_tokens": cacheWrite,
		"output_tokens":            output,
		"reasoning_output_tokens":  reasoningOutput,
		"total_tokens":             total,
		"estimated_cost_usd":       cost,
		"errors":                   errorsCount,
	}
}

func (s *Server) usageBreakdownForUser(user AdminUser) map[string]any {
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	breakdown := s.usageBreakdownFromRecords(records)
	breakdown["members"] = s.aggregateUsageByMember(user, records)
	return breakdown
}

func (s *Server) usageBreakdownFromRecords(records []UsageRecord) map[string]any {
	return map[string]any{
		"projects":  aggregateUsage(records, func(record UsageRecord) string { return record.ProjectID }),
		"models":    aggregateUsage(records, func(record UsageRecord) string { return record.ModelName }),
		"providers": aggregateUsage(records, func(record UsageRecord) string { return record.ProviderID }),
		"provider_resources": aggregateUsage(records, func(record UsageRecord) string {
			return record.ProviderResourceID
		}),
		"cost_centers": aggregateUsage(records, func(record UsageRecord) string {
			project, ok := s.store.GetProject(record.ProjectID)
			if !ok {
				return "unknown"
			}
			return s.costCenterForProject(project)
		}),
	}
}

func (s *Server) aggregateUsageByMember(user AdminUser, records []UsageRecord) []map[string]any {
	keysByID := map[string]APIKey{}
	for _, key := range s.store.ListAPIKeys() {
		keysByID[key.ID] = key
	}
	projectsByID := map[string]Project{}
	for _, project := range s.store.ListProjects() {
		projectsByID[project.ID] = project
	}
	usersByID := map[string]AdminUser{}
	for _, item := range s.store.ListAdminUsers() {
		usersByID[item.ID] = item
	}

	type bucket struct {
		Key               string
		Requests          int64
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		TotalTokens       int64
		CostUSD           float64
		UsedKeyIDs        map[string]bool
	}
	buckets := map[string]*bucket{}
	ownedKeyCount := map[string]int{}
	for _, key := range keysByID {
		if key.Status == StatusRevoked {
			continue
		}
		ownerUserID := usageAttributionUserID(key, projectsByID[key.ProjectID])
		if canAttributeUsageToMember(user, usersByID, ownerUserID) {
			ownedKeyCount[ownerUserID]++
		}
	}
	for _, record := range records {
		memberID := strings.TrimSpace(record.AttributedUserID)
		if !canAttributeUsageToMember(user, usersByID, memberID) {
			memberID = ""
		}
		if memberID == "" {
			if key, ok := keysByID[record.APIKeyID]; ok {
				candidate := usageAttributionUserID(key, projectsByID[key.ProjectID])
				if canAttributeUsageToMember(user, usersByID, candidate) {
					memberID = candidate
				}
			}
		}
		if memberID == "" {
			if project, ok := projectsByID[record.ProjectID]; ok && canAttributeUsageToMember(user, usersByID, project.OwnerUserID) {
				memberID = project.OwnerUserID
			}
		}
		if memberID == "" {
			memberID = "unknown"
		}
		item, ok := buckets[memberID]
		if !ok {
			item = &bucket{Key: memberID, UsedKeyIDs: map[string]bool{}}
			buckets[memberID] = item
		}
		item.Requests++
		item.InputTokens += record.InputTokens
		item.CachedInputTokens += record.CachedInputTokens
		item.OutputTokens += record.OutputTokens
		item.TotalTokens += record.TotalTokens
		item.CostUSD += record.CostUSD
		if record.APIKeyID != "" {
			item.UsedKeyIDs[record.APIKeyID] = true
		}
	}
	items := make([]bucket, 0, len(buckets))
	for _, item := range buckets {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TotalTokens != items[j].TotalTokens {
			return items[i].TotalTokens > items[j].TotalTokens
		}
		if items[i].Requests != items[j].Requests {
			return items[i].Requests > items[j].Requests
		}
		return items[i].CostUSD > items[j].CostUSD
	})
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id":                  item.Key,
			"request_count":       item.Requests,
			"input_tokens":        item.InputTokens,
			"cached_input_tokens": item.CachedInputTokens,
			"output_tokens":       item.OutputTokens,
			"total_tokens":        item.TotalTokens,
			"estimated_cost_usd":  item.CostUSD,
			"owned_key_count":     ownedKeyCount[item.Key],
			"used_key_count":      len(item.UsedKeyIDs),
		})
	}
	return result
}

func canAttributeUsageToMember(user AdminUser, usersByID map[string]AdminUser, memberID string) bool {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return false
	}
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) || role == "security_admin" {
		return true
	}
	if role == "team_leader" {
		member, ok := usersByID[memberID]
		if !ok || !userHasTeam(member, user.TeamID) {
			return false
		}
		memberRole := normalizeAdminRole(member.Role)
		return memberRole == "user" || memberRole == "team_leader"
	}
	return memberID == user.ID
}

func (s *Server) usageTimeseriesForUser(user AdminUser, days int) []map[string]any {
	if days <= 0 {
		days = 31
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().UTC()
	series := make([]map[string]any, 0, days)
	indexByDay := map[string]int{}
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		indexByDay[day] = len(series)
		series = append(series, map[string]any{
			"date":                day,
			"request_count":       int64(0),
			"input_tokens":        int64(0),
			"cached_input_tokens": int64(0),
			"output_tokens":       int64(0),
			"total_tokens":        int64(0),
			"estimated_cost_usd":  float64(0),
		})
	}
	for _, record := range s.filterUsageRecordsForUser(user, s.store.ListUsageRecords()) {
		if record.CreatedAt.Before(now.AddDate(0, 0, -days+1)) {
			continue
		}
		day := record.CreatedAt.UTC().Format("2006-01-02")
		idx, ok := indexByDay[day]
		if !ok {
			continue
		}
		series[idx]["request_count"] = series[idx]["request_count"].(int64) + 1
		series[idx]["input_tokens"] = series[idx]["input_tokens"].(int64) + record.InputTokens
		series[idx]["cached_input_tokens"] = series[idx]["cached_input_tokens"].(int64) + record.CachedInputTokens
		series[idx]["output_tokens"] = series[idx]["output_tokens"].(int64) + record.OutputTokens
		series[idx]["total_tokens"] = series[idx]["total_tokens"].(int64) + record.TotalTokens
		series[idx]["estimated_cost_usd"] = series[idx]["estimated_cost_usd"].(float64) + record.CostUSD
	}
	return series
}

func (s *Server) filterUsageRecordsForUser(user AdminUser, records []UsageRecord) []UsageRecord {
	if s.canViewGlobalOperations(user) {
		return records
	}
	visibleProjects := s.visibleProjectIDSet(user)
	visibleKeys := s.visibleAPIKeyIDSet(user)
	usersByID := map[string]AdminUser{}
	for _, item := range s.store.ListAdminUsers() {
		usersByID[item.ID] = item
	}
	out := make([]UsageRecord, 0, len(records))
	for _, record := range records {
		if record.AttributedUserID != "" && canAttributeUsageToMember(user, usersByID, record.AttributedUserID) {
			out = append(out, record)
			continue
		}
		if normalizeAdminRole(user.Role) == "team_leader" && visibleProjects[record.ProjectID] {
			out = append(out, record)
			continue
		}
		if record.APIKeyID != "" && visibleKeys[record.APIKeyID] {
			out = append(out, record)
		}
	}
	return out
}

func (s *Server) filterRequestLogsForUser(user AdminUser, logs []RequestLog) []RequestLog {
	if s.canViewGlobalOperations(user) {
		return logs
	}
	visibleProjects := s.visibleProjectIDSet(user)
	visibleKeys := s.visibleAPIKeyIDSet(user)
	out := make([]RequestLog, 0, len(logs))
	for _, log := range logs {
		if canAccessRequestLogFromSets(user, log, visibleProjects, visibleKeys) {
			out = append(out, log)
		}
	}
	return out
}

func (s *Server) canAccessRequestLog(user AdminUser, log RequestLog) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	return canAccessRequestLogFromSets(user, log, s.visibleProjectIDSet(user), s.visibleAPIKeyIDSet(user))
}

func canAccessRequestLogFromSets(user AdminUser, log RequestLog, visibleProjects map[string]bool, visibleKeys map[string]bool) bool {
	if normalizeAdminRole(user.Role) == "team_leader" && visibleProjects[log.ProjectID] {
		return true
	}
	return log.APIKeyID != "" && visibleKeys[log.APIKeyID]
}

func (s *Server) visibleProjectIDSet(user AdminUser) map[string]bool {
	out := map[string]bool{}
	for _, project := range s.filterProjectsForUser(user, s.store.ListProjects()) {
		out[project.ID] = true
	}
	return out
}

func (s *Server) visibleAPIKeyIDSet(user AdminUser) map[string]bool {
	out := map[string]bool{}
	for _, key := range s.filterAPIKeysForUser(user, s.store.ListAPIKeys()) {
		out[key.ID] = true
	}
	return out
}

func (s *Server) costCenterForProject(project Project) string {
	if costCenter := strings.TrimSpace(project.CostCenter); costCenter != "" {
		return costCenter
	}
	if strings.TrimSpace(project.TeamID) != "" {
		for _, team := range s.store.ListResources("teams") {
			if team.ID == project.TeamID {
				if costCenter := strings.TrimSpace(stringField(team.Fields, "cost_center")); costCenter != "" {
					return costCenter
				}
			}
		}
	}
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		for _, quota := range s.store.ListResources("quota-policies") {
			if quota.ID == project.DefaultQuotaRef {
				if costCenter := strings.TrimSpace(stringField(quota.Fields, "cost_center")); costCenter != "" {
					return costCenter
				}
			}
		}
	}
	if strings.TrimSpace(project.TeamID) != "" {
		return project.TeamID
	}
	if strings.TrimSpace(project.ID) != "" {
		return "project:" + project.ID
	}
	return "unknown"
}

func (s *Server) filterAPIKeysForUser(user AdminUser, keys []APIKey) []APIKey {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return keys
	}
	projects := map[string]Project{}
	for _, project := range s.store.ListProjects() {
		projects[project.ID] = project
	}
	memberships := s.store.ListResources("project-members")
	activeTeams := s.activeTeamIDSet()
	out := make([]APIKey, 0, len(keys))
	for _, key := range keys {
		if canAccessAPIKeyWithProjects(user, key, projects, memberships, activeTeams) {
			out = append(out, key)
		}
	}
	return out
}

func (s *Server) accessibleModelsForAdminUser(user AdminUser) []Model {
	if s.canViewGlobalOperations(user) {
		return s.store.ListModels()
	}
	routed := s.activeRoutedModelNameSet()
	models := s.store.ListModels()
	out := make([]Model, 0, len(models))
	for _, model := range models {
		if model.Status != StatusActive {
			continue
		}
		if routed[model.Name] || routed[model.ID] {
			out = append(out, model)
		}
	}
	return out
}

func (s *Server) activeRoutedModelNameSet() map[string]bool {
	out := map[string]bool{}
	for _, route := range s.store.ListRoutes() {
		if route.Status != StatusActive {
			continue
		}
		if modelName := strings.TrimSpace(route.ModelName); modelName != "" {
			out[modelName] = true
		}
	}
	return out
}

func (s *Server) canManageAPIKey(user AdminUser, keyID string) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	for _, key := range s.store.ListAPIKeys() {
		if key.ID == keyID {
			return s.canAccessAPIKey(user, key)
		}
	}
	return false
}

func (s *Server) findAPIKey(keyID string) (APIKey, error) {
	for _, key := range s.store.ListAPIKeys() {
		if key.ID == keyID {
			return key, nil
		}
	}
	return APIKey{}, NewHTTPError(404, "api_key_not_found", "API key not found")
}

func (s *Server) resolveAPIKeyOwner(actor AdminUser, requestedUserID string) (string, error) {
	ownerUserID := strings.TrimSpace(requestedUserID)
	if ownerUserID == "" {
		return actor.ID, nil
	}
	if ownerUserID == actor.ID {
		return actor.ID, nil
	}
	owner, ok := s.findAdminUser(ownerUserID)
	if !ok {
		return "", NewHTTPError(404, "api_key_owner_not_found", "API key owner not found")
	}
	if owner.Status != "" && owner.Status != StatusActive {
		return "", NewHTTPError(400, "api_key_owner_inactive", "API key owner must be active")
	}
	role := normalizeAdminRole(actor.Role)
	if isPlatformAdminRole(role) {
		return owner.ID, nil
	}
	if role == "team_leader" && actor.TeamID != "" && userHasTeam(owner, actor.TeamID) {
		ownerRole := normalizeAdminRole(owner.Role)
		if ownerRole == "user" || ownerRole == "team_leader" {
			return owner.ID, nil
		}
	}
	return "", NewHTTPError(403, "api_key_owner_forbidden", "API key owner is not available for this user")
}

func (s *Server) canAccessAPIKey(user AdminUser, key APIKey) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	project, ok := s.store.GetProject(key.ProjectID)
	projects := map[string]Project{}
	if ok {
		projects[project.ID] = project
	}
	return canAccessAPIKeyWithProjects(user, key, projects, s.store.ListResources("project-members"), s.activeTeamIDSet())
}

func (s *Server) canUseProjectForAPIKey(user AdminUser, projectID string) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	project, ok := s.store.GetProject(projectID)
	if !ok || project.Status != "" && project.Status != StatusActive {
		return false
	}
	memberships := s.store.ListResources("project-members")
	if projectAccessRoleRank(projectAccessRole(user, project, memberships, s.activeTeamIDSet())) >= projectAccessRoleRank("developer") {
		return true
	}
	return projectMembersCanIssueKey(memberships, user, project.ID)
}

func canAccessAPIKeyWithProjects(user AdminUser, key APIKey, projects map[string]Project, memberships []AdminResource, activeTeams map[string]bool) bool {
	if strings.TrimSpace(key.OwnerUserID) != "" && key.OwnerUserID == user.ID {
		return true
	}
	if strings.TrimSpace(key.OwnerUserID) == "" && key.Metadata != nil && key.Metadata["created_by"] == user.ID {
		return true
	}
	project, ok := projects[key.ProjectID]
	if !ok {
		return false
	}
	return projectAccessRoleRank(projectAccessRole(user, project, memberships, activeTeams)) >= projectAccessRoleRank("maintainer")
}

func projectMembersCanIssueKey(memberships []AdminResource, user AdminUser, projectID string) bool {
	for _, member := range memberships {
		if !projectMemberMatches(member, projectID, user.ID) {
			continue
		}
		if projectMemberRoleCanIssueKey(memberRole(member)) || truthyField(member.Fields, "can_issue_keys") {
			return true
		}
	}
	return false
}

func projectMemberMatches(member AdminResource, projectID string, userID string) bool {
	return member.Status == StatusActive &&
		strings.TrimSpace(stringField(member.Fields, "project_id")) == projectID &&
		strings.TrimSpace(stringField(member.Fields, "user_id")) == userID
}

func memberRole(member AdminResource) string {
	return strings.ToLower(strings.TrimSpace(stringField(member.Fields, "role")))
}

func projectMemberRoleCanIssueKey(role string) bool {
	switch role {
	case "owner", "maintainer", "developer":
		return true
	default:
		return false
	}
}

func truthyField(fields map[string]any, key string) bool {
	value := strings.ToLower(strings.TrimSpace(stringField(fields, key)))
	switch value {
	case "true", "1", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func (s *Server) filterAdminUsersForUser(user AdminUser, users []AdminUser) []AdminUser {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return users
	}
	if role != "team_leader" {
		return nil
	}
	out := make([]AdminUser, 0, len(users))
	for _, item := range users {
		if userHasTeam(item, user.TeamID) {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) filterApprovalRequestsForUser(user AdminUser, approvals []ApprovalRequest) []ApprovalRequest {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return approvals
	}
	teamUsers := map[string]bool{}
	projects := map[string]Project{}
	activeTeam := role == "team_leader" && s.activeTeamIDSet()[user.TeamID]
	if activeTeam {
		teamUsers[user.ID] = true
		for _, item := range s.store.ListAdminUsers() {
			if userHasTeam(item, user.TeamID) {
				teamUsers[item.ID] = true
			}
		}
		for _, project := range s.store.ListProjects() {
			projects[project.ID] = project
		}
	}
	out := make([]ApprovalRequest, 0, len(approvals))
	for _, item := range approvals {
		if projectID := approvalProjectID(item); projectID != "" {
			project, ok := projects[projectID]
			if activeTeam && ok && strings.TrimSpace(user.TeamID) != "" && user.TeamID == project.TeamID {
				out = append(out, item)
			}
			continue
		}
		if s.canViewGlobalOperations(user) || activeTeam && teamUsers[item.RequesterID] {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) canExportKind(user AdminUser, kind string) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	role := normalizeAdminRole(user.Role)
	switch role {
	case "team_leader":
		switch kind {
		case "requests", "usage", "cost-centers", "budgets", "chargebacks", "invoices", "approvals":
			return true
		default:
			return false
		}
	case "user":
		return kind == "requests" || kind == "usage"
	default:
		return false
	}
}

func (s *Server) findAdminUser(userID string) (AdminUser, bool) {
	for _, user := range s.store.ListAdminUsers() {
		if user.ID == userID {
			return user, true
		}
	}
	return AdminUser{}, false
}

func (s *Server) filterResourcesForUser(user AdminUser, kind string, resources []AdminResource) []AdminResource {
	role := normalizeAdminRole(user.Role)
	if s.canViewGlobalOperations(user) {
		return resources
	}
	if role == "user" && kind == "project-members" {
		out := make([]AdminResource, 0, len(resources))
		for _, item := range resources {
			if item.Status == StatusActive && strings.TrimSpace(stringField(item.Fields, "user_id")) == user.ID {
				out = append(out, item)
			}
		}
		return out
	}
	if role != "team_leader" {
		return nil
	}
	switch kind {
	case "project-members":
		return s.filterProjectMemberResourcesForTeamLeader(user, resources)
	case "quota-policies":
		return s.filterQuotaPoliciesForTeamLeader(user, resources)
	}
	out := make([]AdminResource, 0, len(resources))
	for _, item := range resources {
		if s.canAccessScopedResource(user, kind, item) {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) filterProjectMemberResourcesForTeamLeader(user AdminUser, resources []AdminResource) []AdminResource {
	projects := map[string]Project{}
	for _, project := range s.store.ListProjects() {
		projects[project.ID] = project
	}
	memberships := s.store.ListResources("project-members")
	activeTeams := s.activeTeamIDSet()
	users := map[string]AdminUser{}
	for _, item := range s.store.ListAdminUsers() {
		users[item.ID] = item
	}
	out := make([]AdminResource, 0, len(resources))
	for _, item := range resources {
		projectID := strings.TrimSpace(stringField(item.Fields, "project_id"))
		project, ok := projects[projectID]
		if !ok || projectAccessRole(user, project, memberships, activeTeams) == "" {
			continue
		}
		targetUser, ok := users[strings.TrimSpace(stringField(item.Fields, "user_id"))]
		if ok && userHasTeam(targetUser, user.TeamID) {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) filterQuotaPoliciesForTeamLeader(user AdminUser, resources []AdminResource) []AdminResource {
	visibleProjects := map[string]bool{}
	visibleDefaultQuotas := map[string]bool{}
	for _, project := range s.filterProjectsForUser(user, s.store.ListProjects()) {
		visibleProjects[project.ID] = true
		if quotaID := strings.TrimSpace(project.DefaultQuotaRef); quotaID != "" {
			visibleDefaultQuotas[quotaID] = true
		}
	}
	costCenters := s.teamCostCenterSet(user.TeamID)
	out := make([]AdminResource, 0, len(resources))
	for _, item := range resources {
		scope := strings.ToLower(strings.TrimSpace(firstStringField(item.Fields, "scope", "scope_type")))
		scopeID := strings.TrimSpace(stringField(item.Fields, "scope_id"))
		visible := false
		switch scope {
		case "project":
			visible = visibleProjects[scopeID]
		case "team":
			visible = scopeID == user.TeamID
		case "cost_center", "cost-center":
			visible = costCenters[normalizeScopeValue(scopeID)]
		default:
			visible = visibleDefaultQuotas[item.ID] || strings.TrimSpace(stringField(item.Fields, "team_id")) == user.TeamID || resourceMatchesCostCenterSet(item, costCenters)
		}
		if visible {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) canAccessScopedResource(user AdminUser, kind string, item AdminResource) bool {
	switch kind {
	case "teams":
		return item.ID == user.TeamID
	case "cost-centers":
		return s.resourceMatchesTeamCostCenter(user.TeamID, item)
	case "budgets":
		scope := strings.ToLower(strings.TrimSpace(stringField(item.Fields, "scope")))
		scopeID := strings.TrimSpace(stringField(item.Fields, "scope_id"))
		if scope == "team" {
			return scopeID == user.TeamID
		}
		if scope == "cost_center" || scope == "cost-center" {
			return s.teamCostCenterSet(user.TeamID)[normalizeScopeValue(scopeID)]
		}
		return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
	case "quota-policies":
		return s.canAccessQuotaPolicy(user, item)
	case "project-members":
		return s.canAccessProjectMemberResource(user, item)
	case "chargebacks", "invoices":
		return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
	default:
		return false
	}
}

func (s *Server) canAccessProjectMemberResource(user AdminUser, item AdminResource) bool {
	projectID := strings.TrimSpace(stringField(item.Fields, "project_id"))
	if projectID == "" {
		return false
	}
	project, ok := s.store.GetProject(projectID)
	if !ok || !s.canAccessProject(user, project) {
		return false
	}
	if normalizeAdminRole(user.Role) != "team_leader" {
		return true
	}
	targetUserID := strings.TrimSpace(stringField(item.Fields, "user_id"))
	targetUser, ok := s.findAdminUser(targetUserID)
	return ok && userHasTeam(targetUser, user.TeamID)
}

func (s *Server) canAccessQuotaPolicy(user AdminUser, item AdminResource) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	if normalizeAdminRole(user.Role) != "team_leader" {
		return false
	}
	scope := strings.ToLower(strings.TrimSpace(firstStringField(item.Fields, "scope", "scope_type")))
	scopeID := strings.TrimSpace(stringField(item.Fields, "scope_id"))
	switch scope {
	case "project":
		return s.visibleProjectIDSet(user)[scopeID]
	case "team":
		return scopeID == user.TeamID
	case "cost_center", "cost-center":
		return s.teamCostCenterSet(user.TeamID)[normalizeScopeValue(scopeID)]
	}
	for _, project := range s.filterProjectsForUser(user, s.store.ListProjects()) {
		if strings.TrimSpace(project.DefaultQuotaRef) == item.ID {
			return true
		}
	}
	return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
}

func (s *Server) validateScopedResourceMutation(user AdminUser, kind string, resourceID string, req AdminResource) error {
	if kind == "project-members" {
		return s.validateProjectMemberMutation(user, resourceID, req)
	}
	if normalizeAdminRole(user.Role) != "team_leader" || kind != "quota-policies" {
		return nil
	}
	if resourceID != "" {
		existing, err := s.findResource(kind, resourceID)
		if err != nil {
			return err
		}
		if !s.canManageQuotaPolicy(user, existing) {
			return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Quota policy is not available for this user")
		}
		if req.Fields == nil {
			return nil
		}
	}
	if !s.quotaPolicyReferencesManageableProject(user, req) {
		return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Quota policy must belong to a manageable project")
	}
	return nil
}

func (s *Server) validateProjectMemberMutation(user AdminUser, resourceID string, req AdminResource) error {
	var existing AdminResource
	var err error
	if resourceID != "" {
		existing, err = s.findResource("project-members", resourceID)
		if err != nil {
			return err
		}
		if normalizeAdminRole(user.Role) == "team_leader" && !s.canAccessProjectMemberResource(user, existing) {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Project member is not available for this user")
		}
	}
	fields := req.Fields
	if fields == nil {
		fields = existing.Fields
	}
	projectID := strings.TrimSpace(stringField(fields, "project_id"))
	userID := strings.TrimSpace(stringField(fields, "user_id"))
	role := strings.ToLower(strings.TrimSpace(stringField(fields, "role")))
	if projectID == "" || userID == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "project_id and user_id are required")
	}
	if role == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "role is required")
	}
	if !validProjectMemberRole(role) {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "role must be owner, maintainer, developer, or viewer")
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		return NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
	}
	targetUser, ok := s.findAdminUser(userID)
	if !ok {
		return NewHTTPError(http.StatusNotFound, "admin_user_not_found", "Admin user not found")
	}
	if normalizeAdminRole(user.Role) == "team_leader" {
		if !s.canManageProject(user, project) {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Team leader can only assign own team projects")
		}
		if !userHasTeam(targetUser, user.TeamID) || normalizeAdminRole(targetUser.Role) != "user" {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Team leader can only assign ordinary users in own team")
		}
	}
	for _, item := range s.store.ListResources("project-members") {
		if item.ID == resourceID {
			continue
		}
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			return NewHTTPError(http.StatusConflict, "project_member_conflict", "User is already assigned to this project")
		}
	}
	return nil
}

func validProjectMemberRole(role string) bool {
	switch role {
	case "owner", "maintainer", "developer", "viewer":
		return true
	default:
		return false
	}
}

func (s *Server) canManageQuotaPolicy(user AdminUser, item AdminResource) bool {
	projectID := projectScopedResourceProjectID(item)
	projects := s.store.ListProjects()
	memberships := s.store.ListResources("project-members")
	activeTeams := s.activeTeamIDSet()
	referencesProject := projectID != ""
	foundScopedProject := projectID == ""
	for _, project := range projects {
		matchesScope := project.ID == projectID
		matchesDefault := strings.TrimSpace(project.DefaultQuotaRef) == item.ID
		if !matchesScope && !matchesDefault {
			continue
		}
		referencesProject = true
		if matchesScope {
			foundScopedProject = true
		}
		if !s.canViewGlobalOperations(user) && projectAccessRoleRank(projectAccessRole(user, project, memberships, activeTeams)) < projectAccessRoleRank("maintainer") {
			return false
		}
	}
	if projectID != "" && !foundScopedProject {
		return false
	}
	if referencesProject {
		return true
	}
	return s.canAccessQuotaPolicy(user, item)
}

func (s *Server) quotaPolicyReferencesManageableProject(user AdminUser, item AdminResource) bool {
	projectID := projectScopedResourceProjectID(item)
	if projectID == "" {
		return false
	}
	project, ok := s.store.GetProject(projectID)
	return ok && s.canManageProject(user, project)
}

func projectScopedResourceProjectID(item AdminResource) string {
	scope := strings.ToLower(strings.TrimSpace(firstStringField(item.Fields, "scope", "scope_type")))
	if scope != "project" {
		return ""
	}
	return strings.TrimSpace(firstStringField(item.Fields, "scope_id", "project_id"))
}

func (s *Server) resourceMatchesTeamOrCostCenter(teamID string, item AdminResource) bool {
	if strings.TrimSpace(stringField(item.Fields, "team_id")) == teamID {
		return true
	}
	return s.resourceMatchesTeamCostCenter(teamID, item)
}

func (s *Server) resourceMatchesTeamCostCenter(teamID string, item AdminResource) bool {
	return resourceMatchesCostCenterSet(item, s.teamCostCenterSet(teamID))
}

func resourceMatchesCostCenterSet(item AdminResource, costCenters map[string]bool) bool {
	for _, value := range []string{
		item.ID,
		item.Name,
		stringField(item.Fields, "code"),
		stringField(item.Fields, "cost_center"),
		stringField(item.Fields, "scope_id"),
	} {
		if costCenters[normalizeScopeValue(value)] {
			return true
		}
	}
	return false
}

func (s *Server) teamCostCenterSet(teamID string) map[string]bool {
	out := map[string]bool{}
	teamCostCenter := ""
	for _, team := range s.store.ListResources("teams") {
		if team.ID == teamID {
			teamCostCenter = stringField(team.Fields, "cost_center")
			addScopeValue(out, teamCostCenter)
			break
		}
	}
	for _, project := range s.store.ListProjects() {
		if project.TeamID == teamID {
			addScopeValue(out, project.CostCenter)
			if strings.TrimSpace(project.CostCenter) != "" {
				continue
			}
			if strings.TrimSpace(teamCostCenter) != "" {
				addScopeValue(out, teamCostCenter)
			} else {
				addScopeValue(out, teamID)
			}
		}
	}
	return out
}

func addScopeValue(set map[string]bool, value string) {
	if normalized := normalizeScopeValue(value); normalized != "" {
		set[normalized] = true
	}
}

func normalizeScopeValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func adminResourcePermission(path string) string {
	if strings.Contains(path, "/quota-policies") {
		return "quota"
	}
	if strings.Contains(path, "/project-members") {
		return "project"
	}
	if strings.Contains(path, "/security-policies") {
		return "security"
	}
	if strings.Contains(path, "/identity-providers") {
		return "security"
	}
	if strings.Contains(path, "/alert-rules") {
		return "alert"
	}
	if strings.Contains(path, "/notification-channels") {
		return "alert"
	}
	if strings.Contains(path, "/cost-centers") || strings.Contains(path, "/budgets") ||
		strings.Contains(path, "/chargebacks") || strings.Contains(path, "/invoices") ||
		strings.Contains(path, "/approval-flows") || strings.Contains(path, "/reports") {
		return "usage"
	}
	if strings.Contains(path, "/teams") || strings.Contains(path, "/role-configs") {
		return "identity"
	}
	if strings.Contains(path, "/monitors") || strings.Contains(path, "/proxies") || strings.Contains(path, "/settings") {
		return "provider"
	}
	return "overview"
}
