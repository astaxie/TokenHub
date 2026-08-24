package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type nullableInt64Patch struct {
	Set   bool
	Value *int64
}

func (value *nullableInt64Patch) UnmarshalJSON(data []byte) error {
	value.Set = true
	if string(data) == "null" {
		value.Value = nil
		return nil
	}
	var decoded int64
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "overview", r.Method)
	if !ok {
		return
	}
	providers := []Provider{}
	providerResources := []ProviderResource{}
	alerts := []AlertEvent{}
	if s.canViewGlobalOperations(user) {
		providers = s.store.ListProviders()
		providerResources = s.store.ListProviderResources()
		alerts = s.store.ListAlerts()
	}
	models := s.accessibleModelsForAdminUser(user)
	routes := []ModelRoute{}
	if s.canViewGlobalOperations(user) {
		routes = s.store.ListRoutes()
	}
	activeRoutes := 0
	for _, route := range routes {
		if route.Status == StatusActive {
			activeRoutes++
		}
	}
	summary, err := s.usageSummaryForUser(r.Context(), user)
	if err != nil {
		writeError(w, r, err)
		return
	}
	summary["api_key_count"] = len(s.filterAPIKeysForUser(user, s.store.ListAPIKeys()))
	summary["route_count"] = len(routes)
	summary["active_route_count"] = activeRoutes
	summary["user_count"] = len(s.filterAdminUsersForUser(user, s.store.ListAdminUsers()))
	writeJSON(w, http.StatusOK, map[string]any{
		"summary":            summary,
		"projects":           s.filterProjectsForUser(user, s.store.ListProjects()),
		"providers":          providers,
		"provider_resources": providerResources,
		"models":             models,
		"alerts":             alerts,
	})
}

func (s *Server) handleAdminProjectsGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "project", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterProjectsForUser(user, s.store.ListProjects())})
}

func (s *Server) handleAdminProjectsPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "project", r.Method)
	if !ok {
		return
	}
	var req Project
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if normalizeAdminRole(user.Role) == "team_leader" {
		if strings.TrimSpace(user.TeamID) == "" {
			writeError(w, r, NewHTTPError(403, "team_required", "Team leader must belong to a team"))
			return
		}
		req.TeamID = user.TeamID
		if strings.TrimSpace(req.OwnerUserID) == "" {
			req.OwnerUserID = user.ID
		}
	}
	project, err := s.store.CreateProjectChecked(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "project", project.ID, "", project)
	if link, found := projectTeamByID(project.Teams, project.TeamID); found {
		s.recordAdminAudit(r, user, "create", "project_team", projectTeamAuditID(project.ID, link.TeamID), nil, link)
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleAdminProjectNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/projects/"), "/")
	projectID := parts[0]
	if projectID == "" {
		writeError(w, r, NewHTTPError(400, "project_required", "Project ID is required"))
		return
	}
	permission := "project"
	if len(parts) == 2 && parts[1] == "keys" {
		permission = "api_key"
	}
	if len(parts) == 2 && parts[1] == "quota-increase" {
		permission = "approval"
	}
	if !canAdmin(user.Role, permission, r.Method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return
	}
	if len(parts) >= 2 && parts[1] == "teams" {
		s.handleAdminProjectTeams(w, r, user, projectID, parts)
		return
	}
	if len(parts) == 2 && parts[1] == "quota-increase" {
		s.handleAdminProjectQuotaIncrease(w, r, user, projectID)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			s.serveAdminProjectPatch(w, r, user, projectID)
		case http.MethodDelete:
			s.serveAdminProjectDelete(w, r, user, projectID)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if len(parts) != 2 || parts[1] != "keys" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.serveAdminProjectKeysGet(w, r, user, projectID)
	case http.MethodPost:
		s.serveAdminProjectKeysPost(w, r, user, projectID)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProjectPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectItemRoute(w, r, "project", s.serveAdminProjectPatch)
}

func (s *Server) handleAdminProjectDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectItemRoute(w, r, "project", s.serveAdminProjectDelete)
}

func (s *Server) handleAdminProjectKeysGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectItemRoute(w, r, "api_key", s.serveAdminProjectKeysGet)
}

func (s *Server) handleAdminProjectKeysPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectItemRoute(w, r, "api_key", s.serveAdminProjectKeysPost)
}

func (s *Server) handleAdminProjectItemRoute(w http.ResponseWriter, r *http.Request, permission string, handler func(http.ResponseWriter, *http.Request, AdminUser, string)) {
	projectID := r.PathValue("project_id")
	if projectID == "" || strings.Contains(projectID, "/") {
		s.handleAdminProjectNested(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, permission, r.Method)
	if !ok {
		return
	}
	handler(w, r, user, projectID)
}

func (s *Server) adminProjectMethodNotAllowed(permission string, allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("project_id")
		if projectID == "" || strings.Contains(projectID, "/") {
			s.handleAdminProjectNested(w, r)
			return
		}
		if _, ok := s.requireAdmin(w, r, permission, r.Method); !ok {
			return
		}
		reject(w, r)
	}
}

func (s *Server) serveAdminProjectPatch(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	beforeProject, _ := s.store.GetProject(projectID)
	var req Project
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if normalizeAdminRole(user.Role) == "team_leader" {
		existing, err := s.findProject(projectID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !s.canManageProject(user, existing) {
			writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
			return
		}
		req.TeamID = existing.TeamID
		if strings.TrimSpace(req.OwnerUserID) == "" {
			req.OwnerUserID = existing.OwnerUserID
		}
	}
	project, err := s.store.UpdateProject(projectID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "project", project.ID, beforeProject, project)
	if project.TeamID != "" {
		if _, existed := projectTeamByID(beforeProject.Teams, project.TeamID); !existed {
			if link, found := projectTeamByID(project.Teams, project.TeamID); found {
				s.recordAdminAudit(r, user, "create", "project_team", projectTeamAuditID(project.ID, link.TeamID), nil, link)
			}
		}
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) serveAdminProjectDelete(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	beforeProject, _ := s.store.GetProject(projectID)
	if normalizeAdminRole(user.Role) == "team_leader" {
		existing, err := s.findProject(projectID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if !s.canManageProject(user, existing) {
			writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
			return
		}
	}
	if err := s.store.DeleteProject(projectID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "project", projectID, "", nil)
	for _, link := range beforeProject.Teams {
		s.recordAdminAudit(r, user, "delete", "project_team", projectTeamAuditID(projectID, link.TeamID), link, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveAdminProjectKeysGet(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	if !s.canUseProjectForAPIKey(user, projectID) {
		writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAPIKeysForUser(user, s.store.ListProjectKeys(projectID))})
}

func (s *Server) serveAdminProjectKeysPost(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	if !s.canUseProjectForAPIKey(user, projectID) {
		writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
		return
	}
	s.handleAdminAPIKeyCreate(w, r, user, projectID)
}

func (s *Server) handleAdminProjectTeamsGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectTeamRoute(w, r, false, s.serveAdminProjectTeamsGet)
}

func (s *Server) handleAdminProjectTeamsPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectTeamRoute(w, r, true, s.serveAdminProjectTeamsPost)
}

func (s *Server) handleAdminProjectTeamPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectTeamItemRoute(w, r, s.serveAdminProjectTeamPatch)
}

func (s *Server) handleAdminProjectTeamDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProjectTeamItemRoute(w, r, s.serveAdminProjectTeamDelete)
}

func (s *Server) handleAdminProjectTeamRoute(w http.ResponseWriter, r *http.Request, manage bool, handler func(http.ResponseWriter, *http.Request, AdminUser, Project)) {
	projectID := r.PathValue("project_id")
	if projectID == "" || strings.Contains(projectID, "/") {
		s.handleAdminProjectNested(w, r)
		return
	}
	user, ok := s.requireAdmin(w, r, "project", r.Method)
	if !ok {
		return
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
		return
	}
	if manage && !s.canManageProject(user, project) {
		writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project management permission is required"))
		return
	}
	if !manage && !s.canAccessProject(user, project) {
		writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project is not available for this user"))
		return
	}
	handler(w, r, user, project)
}

func (s *Server) handleAdminProjectTeamItemRoute(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request, AdminUser, Project, string)) {
	teamID := r.PathValue("team_id")
	if teamID == "" || strings.Contains(teamID, "/") {
		s.handleAdminProjectNested(w, r)
		return
	}
	teamID = strings.TrimSpace(teamID)
	s.handleAdminProjectTeamRoute(w, r, true, func(w http.ResponseWriter, r *http.Request, user AdminUser, project Project) {
		handler(w, r, user, project, teamID)
	})
}

func (s *Server) adminProjectTeamsMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("project_id")
		if projectID == "" || strings.Contains(projectID, "/") {
			s.handleAdminProjectNested(w, r)
			return
		}
		user, ok := s.requireAdmin(w, r, "project", r.Method)
		if !ok {
			return
		}
		project, ok := s.store.GetProject(projectID)
		if !ok {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
			return
		}
		if !s.canManageProject(user, project) {
			writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project management permission is required"))
			return
		}
		reject(w, r)
	}
}

func (s *Server) adminProjectTeamMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("project_id")
		teamID := r.PathValue("team_id")
		if projectID == "" || teamID == "" || strings.Contains(projectID, "/") || strings.Contains(teamID, "/") {
			s.handleAdminProjectNested(w, r)
			return
		}
		user, ok := s.requireAdmin(w, r, "project", r.Method)
		if !ok {
			return
		}
		project, ok := s.store.GetProject(projectID)
		if !ok {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
			return
		}
		if r.Method == http.MethodGet {
			writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project is not available for this user"))
			return
		}
		if !s.canManageProject(user, project) {
			writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project management permission is required"))
			return
		}
		jsonMethodNotAllowed(allowedMethods)(w, r)
	}
}

func (s *Server) serveAdminProjectTeamsGet(w http.ResponseWriter, r *http.Request, _ AdminUser, project Project) {
	limit := projectTeamPageValue(r.URL.Query().Get("limit"), 50, 1, 200)
	offset := projectTeamPageValue(r.URL.Query().Get("offset"), 0, 0, math.MaxInt)
	links, total, err := s.store.ListProjectTeams(project.ID, offset, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": links, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) serveAdminProjectTeamsPost(w http.ResponseWriter, r *http.Request, user AdminUser, project Project) {
	var req struct {
		TeamID string `json:"team_id"`
		Role   string `json:"role"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TeamID = strings.TrimSpace(req.TeamID)
	req.Role = normalizeProjectAccessRole(req.Role)
	if req.TeamID == "" || !validProjectTeamRole(req.Role) {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_project_team", "team_id and a viewer, developer, or maintainer role are required"))
		return
	}
	team, err := s.findResource("teams", req.TeamID)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "team_not_found", "Team not found"))
		return
	}
	if team.Status != "" && team.Status != StatusActive {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be linked to a project"))
		return
	}
	link, err := s.store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: req.TeamID, Role: req.Role})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "project_team", projectTeamAuditID(project.ID, req.TeamID), nil, link)
	writeJSON(w, http.StatusCreated, link)
}

func (s *Server) serveAdminProjectTeamPatch(w http.ResponseWriter, r *http.Request, user AdminUser, project Project, teamID string) {
	var req struct {
		Role string `json:"role"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Role = normalizeProjectAccessRole(req.Role)
	if teamID == "" || !validProjectTeamRole(req.Role) {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_project_team", "A viewer, developer, or maintainer role is required"))
		return
	}
	before, found := projectTeamByID(project.Teams, teamID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_team_not_found", "Project team link not found"))
		return
	}
	link, err := s.store.UpdateProjectTeam(project.ID, teamID, req.Role)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "project_team", projectTeamAuditID(project.ID, teamID), before, link)
	writeJSON(w, http.StatusOK, link)
}

func (s *Server) serveAdminProjectTeamDelete(w http.ResponseWriter, r *http.Request, user AdminUser, project Project, teamID string) {
	before, found := projectTeamByID(project.Teams, teamID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_team_not_found", "Project team link not found"))
		return
	}
	if err := s.store.RemoveProjectTeam(project.ID, teamID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "project_team", projectTeamAuditID(project.ID, teamID), before, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminProjectTeams(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string, parts []string) {
	project, ok := s.store.GetProject(projectID)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
		return
	}
	if r.Method == http.MethodGet {
		if len(parts) != 2 || !s.canAccessProject(user, project) {
			writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project is not available for this user"))
			return
		}
		s.serveAdminProjectTeamsGet(w, r, user, project)
		return
	}
	if !s.canManageProject(user, project) {
		writeError(w, r, NewHTTPError(http.StatusForbidden, "project_forbidden", "Project management permission is required"))
		return
	}

	switch {
	case len(parts) == 2 && r.Method == http.MethodPost:
		s.serveAdminProjectTeamsPost(w, r, user, project)
	case len(parts) == 3 && r.Method == http.MethodPatch:
		s.serveAdminProjectTeamPatch(w, r, user, project, strings.TrimSpace(parts[2]))
	case len(parts) == 3 && r.Method == http.MethodDelete:
		s.serveAdminProjectTeamDelete(w, r, user, project, strings.TrimSpace(parts[2]))
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func projectTeamPageValue(value string, fallback int, minimum int, maximum int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum {
		return fallback
	}
	if parsed > maximum {
		return maximum
	}
	return parsed
}

func projectTeamByID(links []ProjectTeam, teamID string) (ProjectTeam, bool) {
	for _, link := range links {
		if link.TeamID == teamID {
			return link, true
		}
	}
	return ProjectTeam{}, false
}

func projectTeamAuditID(projectID string, teamID string) string {
	return strings.TrimSpace(projectID) + ":" + strings.TrimSpace(teamID)
}

func (s *Server) handleAdminUsersGet(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAdminUsersForUser(actor, s.store.ListAdminUsers())})
}

func (s *Server) handleAdminUsersPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	var req struct {
		Username string   `json:"username"`
		Name     string   `json:"name"`
		Email    string   `json:"email"`
		Role     string   `json:"role"`
		TeamID   string   `json:"team_id"`
		TeamIDs  []string `json:"team_ids"`
		Status   string   `json:"status"`
		Password string   `json:"password"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if normalizeAdminRole(actor.Role) == "team_leader" {
		if req.TeamID != "" && req.TeamID != actor.TeamID {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage own team"))
			return
		}
		req.TeamID = actor.TeamID
		req.TeamIDs = []string{actor.TeamID}
		if normalizeAdminRole(req.Role) != "user" {
			writeError(w, r, NewHTTPError(403, "role_forbidden", "Team leader can only create ordinary users"))
			return
		}
	}
	user, err := s.store.CreateAdminUser(AdminUser{
		Username: req.Username,
		Name:     req.Name,
		Email:    req.Email,
		Role:     req.Role,
		TeamID:   req.TeamID,
		TeamIDs:  req.TeamIDs,
		Status:   req.Status,
	}, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, actor, "create", "admin_user", user.ID, "", user)
	writeJSON(w, http.StatusCreated, user)
}

type adminUserImportItem struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TeamID   string `json:"team_id"`
	Status   string `json:"status"`
}

func (s *Server) handleAdminUsersImportPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	var req struct {
		Source  string                `json:"source"`
		Format  string                `json:"format"`
		Content string                `json:"content"`
		Users   []adminUserImportItem `json:"users"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	users := req.Users
	if strings.TrimSpace(req.Content) != "" {
		parsed, err := parseAdminUserImportCSV(req.Content)
		if err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_import", err.Error()))
			return
		}
		users = append(users, parsed...)
	}
	if len(users) == 0 {
		writeError(w, r, NewHTTPError(400, "invalid_import", "no users to import"))
		return
	}
	mailChannel, err := s.resolvePasswordResetMailChannel()
	if err != nil {
		writeError(w, r, err)
		return
	}

	existing := s.store.ListAdminUsers()
	result := map[string]any{
		"source":            strings.TrimSpace(req.Source),
		"format":            strings.TrimSpace(req.Format),
		"created":           0,
		"updated":           0,
		"skipped":           0,
		"reset_emails_sent": 0,
		"errors":            []string{},
		"users":             []AdminUser{},
	}
	importedUsers := []AdminUser{}
	errors := []string{}
	created := 0
	updated := 0
	resetEmailsSent := 0
	skipped := 0

	for index, item := range users {
		normalized, err := normalizeAdminUserImportItem(actor, item)
		if err != nil {
			skipped++
			errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
			continue
		}
		if normalizeAdminRole(actor.Role) == "team_leader" {
			if normalized.TeamID != actor.TeamID {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: team leader can only import own team", index+1))
				continue
			}
			if normalizeAdminRole(normalized.Role) != "user" {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: team leader can only import ordinary users", index+1))
				continue
			}
		}

		if current, ok := findImportedAdminUser(existing, normalized); ok {
			if normalizeAdminRole(actor.Role) == "team_leader" && current.TeamID != actor.TeamID {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: existing user is outside current team", index+1))
				continue
			}
			user, err := s.store.UpdateAdminUser(current.ID, normalized, "")
			if err != nil {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
				continue
			}
			importedUsers = append(importedUsers, user)
			updated++
			if err := s.sendAdminPasswordResetEmail(r, mailChannel, user, actor.ID); err != nil {
				errors = append(errors, fmt.Sprintf("row %d: reset email failed: %s", index+1, err.Error()))
			} else {
				resetEmailsSent++
			}
			for i := range existing {
				if existing[i].ID == user.ID {
					existing[i] = user
					break
				}
			}
			continue
		}

		user, err := s.store.CreateAdminUser(normalized, NewID("sso"))
		if err != nil {
			skipped++
			errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
			continue
		}
		importedUsers = append(importedUsers, user)
		existing = append(existing, user)
		created++
		if err := s.sendAdminPasswordResetEmail(r, mailChannel, user, actor.ID); err != nil {
			errors = append(errors, fmt.Sprintf("row %d: reset email failed: %s", index+1, err.Error()))
		} else {
			resetEmailsSent++
		}
	}

	result["created"] = created
	result["updated"] = updated
	result["skipped"] = skipped
	result["reset_emails_sent"] = resetEmailsSent
	result["errors"] = errors
	result["users"] = importedUsers
	s.recordAdminAudit(r, actor, "import", "admin_user", "", "", result)
	writeJSON(w, http.StatusOK, result)
}

func parseAdminUserImportCSV(content string) ([]adminUserImportItem, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("csv must include at least one user row")
	}
	headers := map[string]int{}
	for index, header := range records[0] {
		headers[normalizeImportHeader(header)] = index
	}
	hasHeader := hasAdminUserImportHeader(headers)
	value := func(record []string, names ...string) string {
		for _, name := range names {
			if index, ok := headers[name]; ok && index < len(record) {
				return strings.TrimSpace(record[index])
			}
		}
		return ""
	}
	items := make([]adminUserImportItem, 0, len(records))
	start := 0
	if hasHeader {
		start = 1
	}
	for _, record := range records[start:] {
		if len(record) == 0 || strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}
		if hasHeader {
			items = append(items, adminUserImportItem{
				Username: value(record, "username"),
				Name:     value(record, "name"),
				Email:    value(record, "email"),
				Role:     value(record, "role"),
				TeamID:   value(record, "team_id", "team"),
				Status:   value(record, "status"),
			})
			continue
		}
		items = append(items, adminUserImportItemFromRecord(record))
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("csv must include at least one user row")
	}
	return items, nil
}

func hasAdminUserImportHeader(headers map[string]int) bool {
	for _, name := range []string{"username", "name", "email", "role", "team_id", "team", "status"} {
		if _, ok := headers[name]; ok {
			return true
		}
	}
	return false
}

func adminUserImportItemFromRecord(record []string) adminUserImportItem {
	field := func(index int) string {
		if index >= 0 && index < len(record) {
			return strings.TrimSpace(record[index])
		}
		return ""
	}
	return adminUserImportItem{
		Username: field(0),
		Name:     field(1),
		Email:    field(2),
		Role:     field(3),
		TeamID:   field(4),
		Status:   field(5),
	}
}

func normalizeImportHeader(header string) string {
	header = strings.TrimSpace(strings.ToLower(header))
	switch header {
	case "用户名", "账号", "工号":
		return "username"
	case "姓名", "名称", "昵称":
		return "name"
	case "邮箱", "邮件":
		return "email"
	case "角色":
		return "role"
	case "团队", "团队id", "部门", "部门id":
		return "team_id"
	case "状态":
		return "status"
	default:
		return strings.ReplaceAll(header, "-", "_")
	}
}

func normalizeAdminUserImportItem(actor AdminUser, item adminUserImportItem) (AdminUser, error) {
	email := strings.TrimSpace(item.Email)
	username := strings.TrimSpace(item.Username)
	if email == "" {
		return AdminUser{}, fmt.Errorf("email is required")
	}
	if username == "" {
		username = email
	}
	role := normalizeAdminRole(item.Role)
	if role == "" {
		role = "user"
	}
	teamID := strings.TrimSpace(item.TeamID)
	if normalizeAdminRole(actor.Role) == "team_leader" {
		teamID = actor.TeamID
	}
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = StatusActive
	}
	return AdminUser{
		Username: username,
		Name:     strings.TrimSpace(item.Name),
		Email:    email,
		Role:     role,
		TeamID:   teamID,
		Status:   status,
	}, nil
}

func findImportedAdminUser(existing []AdminUser, user AdminUser) (AdminUser, bool) {
	email := strings.ToLower(strings.TrimSpace(user.Email))
	username := strings.ToLower(strings.TrimSpace(user.Username))
	for _, item := range existing {
		if email != "" && strings.ToLower(strings.TrimSpace(item.Email)) == email {
			return item, true
		}
		if username != "" && strings.ToLower(strings.TrimSpace(item.Username)) == username {
			return item, true
		}
	}
	return AdminUser{}, false
}

func (s *Server) handleAdminUserItem(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.URL.Path == "/api/admin/users/import" {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	userID := parts[0]
	if len(parts) == 2 {
		if parts[1] != "reset-password-email" {
			writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
			return
		}
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.handleAdminUserResetPasswordEmail(w, r, actor, userID)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.serveAdminUserPatch(w, r, actor, userID)
	case http.MethodDelete:
		s.serveAdminUserDelete(w, r, actor, userID)
	default:
		jsonMethodNotAllowed("PATCH, DELETE")(w, r)
	}
}

func (s *Server) handleAdminUserPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminUserRoute(w, r, s.serveAdminUserPatch)
}

func (s *Server) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminUserRoute(w, r, s.serveAdminUserDelete)
}

func (s *Server) handleAdminUserRoute(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request, AdminUser, string)) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if userID == "" || strings.Contains(userID, "/") {
		s.handleAdminUserItem(w, r)
		return
	}
	handler(w, r, actor, userID)
}

func (s *Server) adminUserMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireAdmin(w, r, "identity", r.Method); !ok {
			return
		}
		userID := r.PathValue("user_id")
		if userID == "" || strings.Contains(userID, "/") {
			s.handleAdminUserItem(w, r)
			return
		}
		reject(w, r)
	}
}

func (s *Server) serveAdminUserPatch(w http.ResponseWriter, r *http.Request, actor AdminUser, userID string) {
	var req struct {
		Username string   `json:"username"`
		Name     string   `json:"name"`
		Email    string   `json:"email"`
		Role     string   `json:"role"`
		TeamID   string   `json:"team_id"`
		TeamIDs  []string `json:"team_ids"`
		Status   string   `json:"status"`
		Password string   `json:"password"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if normalizeAdminRole(actor.Role) == "team_leader" {
		target, ok := s.findAdminUser(userID)
		if !ok || !userHasTeam(target, actor.TeamID) || normalizeAdminRole(target.Role) != "user" {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage ordinary users in own team"))
			return
		}
		if req.TeamID != "" && req.TeamID != actor.TeamID {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage own team"))
			return
		}
		req.TeamID = actor.TeamID
		req.TeamIDs = []string{actor.TeamID}
		if req.Role != "" && normalizeAdminRole(req.Role) != "user" {
			writeError(w, r, NewHTTPError(403, "role_forbidden", "Team leader cannot elevate user role"))
			return
		}
		req.Role = "user"
	}
	updatedUser, err := s.store.UpdateAdminUser(userID, AdminUser{
		Username: req.Username,
		Name:     req.Name,
		Email:    req.Email,
		Role:     req.Role,
		TeamID:   req.TeamID,
		TeamIDs:  req.TeamIDs,
		Status:   req.Status,
	}, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, actor, "update", "admin_user", userID, "", updatedUser)
	writeJSON(w, http.StatusOK, updatedUser)
}

func (s *Server) serveAdminUserDelete(w http.ResponseWriter, r *http.Request, actor AdminUser, userID string) {
	if actor.ID == userID {
		writeError(w, r, NewHTTPError(400, "cannot_delete_self", "You cannot delete your own account"))
		return
	}
	if normalizeAdminRole(actor.Role) == "team_leader" {
		target, ok := s.findAdminUser(userID)
		if !ok || !userHasTeam(target, actor.TeamID) || normalizeAdminRole(target.Role) != "user" {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only delete ordinary users in own team"))
			return
		}
	}
	if err := s.store.DeleteAdminUser(userID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, actor, "delete", "admin_user", userID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUserResetPasswordEmailPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if userID == "" || strings.Contains(userID, "/") {
		s.handleAdminUserItem(w, r)
		return
	}
	s.handleAdminUserResetPasswordEmail(w, r, actor, userID)
}

func (s *Server) handleAdminUserResetPasswordEmail(w http.ResponseWriter, r *http.Request, actor AdminUser, userID string) {
	target, ok := s.findAdminUser(userID)
	if !ok {
		writeError(w, r, NewHTTPError(404, "admin_user_not_found", "Admin user not found"))
		return
	}
	if normalizeAdminRole(actor.Role) == "team_leader" && (!userHasTeam(target, actor.TeamID) || normalizeAdminRole(target.Role) != "user") {
		writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage ordinary users in own team"))
		return
	}
	mailChannel, err := s.resolvePasswordResetMailChannel()
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.sendAdminPasswordResetEmail(r, mailChannel, target, actor.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, actor, "send_reset_password_email", "admin_user", userID, "", map[string]any{"email": target.Email})
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "user": target})
}

func (s *Server) handleAdminAPIKeysGet(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAPIKeysForUser(user, s.store.ListAPIKeys())})
}

func (s *Server) handleAdminAPIKeysPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	project, err := s.personalAPIKeyProject(user)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.handleAdminAPIKeyCreate(w, r, user, project.ID)
}

func (s *Server) personalAPIKeyProject(user AdminUser) (Project, error) {
	if normalizeAdminRole(user.Role) != "user" {
		return Project{}, NewHTTPError(400, "project_required", "Project ID is required")
	}
	for _, project := range s.store.ListProjects() {
		if project.ID == defaultProjectID || (project.Status != "" && project.Status != StatusActive) {
			continue
		}
		if s.canUseProjectForAPIKey(user, project.ID) {
			return project, nil
		}
	}
	project, ok := s.store.GetProject(defaultProjectID)
	if !ok || (project.Status != "" && project.Status != StatusActive) {
		return Project{}, NewHTTPError(409, "default_project_unavailable", "Default project is unavailable")
	}
	return project, nil
}

func (s *Server) handleAdminAPIKeyCreate(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	var req struct {
		Name            string      `json:"name"`
		Group           string      `json:"group"`
		OwnerUserID     string      `json:"owner_user_id"`
		AllowedModels   []string    `json:"allowed_models"`
		ModelAccessMode string      `json:"model_access_mode"`
		IPAllowlist     []string    `json:"ip_allowlist"`
		Limits          QuotaLimits `json:"limits"`
		RateLimitRPM    *int64      `json:"rate_limit_rpm"`
		TokenLimitTPM   *int64      `json:"token_limit_tpm"`
		ExpiresAt       *time.Time  `json:"expires_at"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	ownerUserID, err := s.resolveAPIKeyOwner(user, req.OwnerUserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload := map[string]any{
		"project_id":        projectID,
		"name":              req.Name,
		"group":             req.Group,
		"owner_user_id":     ownerUserID,
		"allowed_models":    req.AllowedModels,
		"model_access_mode": req.ModelAccessMode,
		"ip_allowlist":      req.IPAllowlist,
		"limits":            req.Limits,
		"rate_limit_rpm":    req.RateLimitRPM,
		"token_limit_tpm":   req.TokenLimitTPM,
		"expires_at":        req.ExpiresAt,
		"requested_action":  "api_key_create",
	}
	if approval, required := s.approvalRequired(user, "api_key_create", "api_key", "", payload); required {
		s.recordAdminAudit(r, user, "request_approval", "api_key", approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	key, secret, err := s.store.CreateAPIKey(projectID, APIKey{
		Name:            req.Name,
		Group:           req.Group,
		OwnerUserID:     ownerUserID,
		Allowed:         req.AllowedModels,
		ModelAccessMode: req.ModelAccessMode,
		IPAllowlist:     req.IPAllowlist,
		Limits:          req.Limits,
		RateLimitRPM:    req.RateLimitRPM,
		TokenLimitTPM:   req.TokenLimitTPM,
		ExpiresAt:       req.ExpiresAt,
		Status:          StatusActive,
		Metadata: map[string]string{
			"created_by":      user.ID,
			"created_by_role": normalizeAdminRole(user.Role),
		},
	}, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "api_key", key.ID, "", map[string]any{
		"project_id": key.ProjectID, "name": key.Name, "owner_user_id": key.OwnerUserID,
		"model_access_mode": key.ModelAccessMode, "allowed_models": key.Allowed,
		"rate_limit_rpm": key.RateLimitRPM, "token_limit_tpm": key.TokenLimitTPM,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                      key.ID,
		"api_key":                 secret,
		"name":                    key.Name,
		"project_id":              key.ProjectID,
		"owner_user_id":           key.OwnerUserID,
		"model_access_mode":       key.ModelAccessMode,
		"allowed_models":          key.Allowed,
		"rate_limit_rpm":          key.RateLimitRPM,
		"token_limit_tpm":         key.TokenLimitTPM,
		"plain_text_visible_once": true,
	})
}

func (s *Server) handleAdminAPIKeyItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/api-keys/"), "/"), "/")
	keyID := parts[0]
	if keyID == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if !s.canManageAPIKey(user, keyID) {
		writeError(w, r, NewHTTPError(403, "api_key_forbidden", "API key is not available for this user"))
		return
	}
	if len(parts) == 2 {
		if parts[1] != "rotate" {
			writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
			return
		}
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminAPIKeyRotate(w, r, user, keyID)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.serveAdminAPIKeyPatch(w, r, user, keyID)
	case http.MethodDelete:
		s.serveAdminAPIKeyDelete(w, r, user, keyID)
	default:
		jsonMethodNotAllowed("PATCH, DELETE")(w, r)
	}
}

func (s *Server) handleAdminAPIKeyPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminAPIKeyRoute(w, r, s.serveAdminAPIKeyPatch)
}

func (s *Server) handleAdminAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminAPIKeyRoute(w, r, s.serveAdminAPIKeyDelete)
}

func (s *Server) handleAdminAPIKeyRotatePost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminAPIKeyRoute(w, r, s.serveAdminAPIKeyRotate)
}

func (s *Server) handleAdminAPIKeyRoute(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request, AdminUser, string)) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	keyID := r.PathValue("key_id")
	if keyID == "" || strings.Contains(keyID, "/") {
		s.handleAdminAPIKeyItem(w, r)
		return
	}
	if !s.canManageAPIKey(user, keyID) {
		writeError(w, r, NewHTTPError(403, "api_key_forbidden", "API key is not available for this user"))
		return
	}
	handler(w, r, user, keyID)
}

func (s *Server) adminAPIKeyMethodNotAllowed(allowedMethods string) http.HandlerFunc {
	reject := jsonMethodNotAllowed(allowedMethods)
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireAdmin(w, r, "api_key", r.Method)
		if !ok {
			return
		}
		keyID := r.PathValue("key_id")
		if keyID == "" || strings.Contains(keyID, "/") {
			s.handleAdminAPIKeyItem(w, r)
			return
		}
		if !s.canManageAPIKey(user, keyID) {
			writeError(w, r, NewHTTPError(403, "api_key_forbidden", "API key is not available for this user"))
			return
		}
		reject(w, r)
	}
}

func (s *Server) serveAdminAPIKeyRotate(w http.ResponseWriter, r *http.Request, user AdminUser, keyID string) {
	var req struct {
		GraceUntil *time.Time `json:"grace_until"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	key, secret, err := s.store.RotateAPIKey(keyID, req.GraceUntil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "rotate", "api_key", keyID, "", map[string]any{"new_key_id": key.ID})
	for _, policyResource := range s.store.ListResources(routingPolicyResourceKind) {
		policy := scopedRoutingPolicy(policyResource)
		if policy.Scope == RoutingPolicyScopeAPIKey && policy.ScopeID == key.ID {
			s.recordAdminAudit(r, user, "rotate_bind", routingPolicyResourceKind, policy.ID,
				map[string]any{"rotated_from_key_id": keyID}, policyResource)
			break
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                      key.ID,
		"api_key":                 secret,
		"name":                    key.Name,
		"project_id":              key.ProjectID,
		"owner_user_id":           key.OwnerUserID,
		"rotated_from_id":         key.RotatedFromID,
		"model_access_mode":       key.ModelAccessMode,
		"allowed_models":          key.Allowed,
		"plain_text_visible_once": true,
	})
}

func (s *Server) serveAdminAPIKeyPatch(w http.ResponseWriter, r *http.Request, user AdminUser, keyID string) {
	var req struct {
		Name            string             `json:"name"`
		Group           string             `json:"group"`
		OwnerUserID     *string            `json:"owner_user_id"`
		AllowedModels   []string           `json:"allowed_models"`
		ModelAccessMode string             `json:"model_access_mode"`
		IPAllowlist     []string           `json:"ip_allowlist"`
		Limits          *QuotaLimits       `json:"limits"`
		RateLimitRPM    nullableInt64Patch `json:"rate_limit_rpm"`
		TokenLimitTPM   nullableInt64Patch `json:"token_limit_tpm"`
		Status          string             `json:"status"`
		ExpiresAt       *time.Time         `json:"expires_at"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	patch := APIKey{
		Name:            req.Name,
		Group:           req.Group,
		Allowed:         req.AllowedModels,
		ModelAccessMode: req.ModelAccessMode,
		IPAllowlist:     req.IPAllowlist,
		LimitsSet:       req.Limits != nil,
		RateLimitRPM:    req.RateLimitRPM.Value,
		RateLimitSet:    req.RateLimitRPM.Set,
		TokenLimitTPM:   req.TokenLimitTPM.Value,
		TokenLimitSet:   req.TokenLimitTPM.Set,
		Status:          req.Status,
		ExpiresAt:       req.ExpiresAt,
	}
	if req.Limits != nil {
		patch.Limits = *req.Limits
	}
	if req.OwnerUserID != nil {
		ownerUserID, err := s.resolveAPIKeyOwner(user, *req.OwnerUserID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		patch.OwnerUserID = ownerUserID
	}
	existing, err := s.findAPIKey(keyID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if approval, required := s.apiKeyUpdateApproval(user, existing, patch); required {
		s.recordAdminAudit(r, user, "request_approval", "api_key", approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	key, err := s.store.UpdateAPIKey(keyID, patch)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "api_key", keyID, existing, key)
	writeJSON(w, http.StatusOK, key)
}

func (s *Server) serveAdminAPIKeyDelete(w http.ResponseWriter, r *http.Request, user AdminUser, keyID string) {
	if err := s.store.DeleteAPIKey(keyID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "api_key", keyID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}
