package server

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAdminResources(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/resources/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	kind := parts[0]
	if kind == openAIAccountQuotaResetOperationKind {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if kind == "monitors" {
				s.ensureDefaultMonitors()
			}
			if kind == "alert-rules" {
				s.ensureDefaultAlertRules()
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": s.filterResourcesForUser(user, kind, s.store.ListResources(kind))})
		case http.MethodPost:
			if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot create teams"))
				return
			}
			var req AdminResource
			if err := s.decodeJSON(w, r, &req); err != nil {
				writeError(w, r, err)
				return
			}
			if req.Name == "" {
				writeError(w, r, NewHTTPError(400, "invalid_resource", "name is required"))
				return
			}
			if err := s.validateScopedResourceMutation(user, kind, "", req); err != nil {
				writeError(w, r, err)
				return
			}
			if approval, required := s.adminResourceApproval(user, kind, "", req); required {
				s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
				writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
				return
			}
			var resource AdminResource
			if kind == routingPolicyResourceKind {
				var err error
				resource, err = s.store.CreateRoutingPolicy(req)
				if err != nil {
					writeError(w, r, err)
					return
				}
			} else {
				resource = s.store.CreateResource(kind, req)
			}
			s.recordAdminAudit(r, user, "create", kind, resource.ID, "", resource)
			writeJSON(w, http.StatusCreated, resource)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if kind == "invoices" && len(parts) == 3 && parts[1] != "" {
		s.handleAdminInvoiceAction(w, r, user, parts[1], parts[2])
		return
	}
	if kind == "monitors" && len(parts) == 3 && parts[1] != "" && parts[2] == "run" {
		s.handleAdminMonitorRun(w, r, user, parts[1])
		return
	}
	if len(parts) != 2 || parts[1] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" && parts[1] != user.TeamID {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only update own team"))
			return
		}
		var req AdminResource
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		if err := s.validateScopedResourceMutation(user, kind, parts[1], req); err != nil {
			writeError(w, r, err)
			return
		}
		if approval, required := s.adminResourceApproval(user, kind, parts[1], req); required {
			s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
			writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
			return
		}
		resource, err := s.store.UpdateResource(kind, parts[1], req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", kind, parts[1], "", resource)
		writeJSON(w, http.StatusOK, resource)
	case http.MethodDelete:
		if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot delete teams"))
			return
		}
		if kind == "teams" {
			if err := s.store.DeleteTeam(parts[1]); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", kind, parts[1], "", nil)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := s.store.DeleteResource(kind, parts[1]); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", kind, parts[1], "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProjectQuotaIncrease(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		writeError(w, r, NewHTTPError(404, "project_not_found", "Project not found"))
		return
	}
	if !s.canManageProject(user, project) {
		writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
		return
	}
	var req AdminResource
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("%s 项目额度提升", project.Name)
	}
	if req.Description == "" {
		req.Description = "项目空间发起的额度提升申请"
	}
	if req.Status == "" {
		req.Status = StatusActive
	}
	fields := req.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	fields["scope"] = "project"
	fields["scope_id"] = project.ID
	req.Fields = fields
	if err := validateQuotaPolicyMinuteLimits(req.Fields); err != nil {
		writeError(w, r, err)
		return
	}
	resourceID := ""
	if quota, ok := s.projectQuotaPolicy(project); ok {
		resourceID = quota.ID
	}
	payload := map[string]any{
		"kind":             "quota-policies",
		"resource_id":      resourceID,
		"project_id":       project.ID,
		"name":             req.Name,
		"description":      req.Description,
		"status":           req.Status,
		"fields":           req.Fields,
		"requested_action": "quota_increase",
	}
	flowID := ""
	if flow, ok := s.matchApprovalFlow("quota_increase", payload); ok {
		flowID = flow.ID
	}
	approval := s.createApprovalRequest(user, flowID, "quota_increase", "quota-policies", resourceID, payload)
	s.recordAdminAudit(r, user, "request_approval", "quota-policies", approval.ID, "", approval)
	writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
}

func (s *Server) ensureDefaultMonitors() {
	existing := s.store.ListResources("monitors")
	existingIDs := map[string]bool{}
	existingTargets := map[string]bool{}
	createdIDs := map[string]bool{}
	for _, item := range existing {
		existingIDs[item.ID] = true
		if key := monitorTargetKey(item.Fields); key != "" {
			existingTargets[key] = true
		}
	}
	for _, item := range s.defaultMonitorResources(existingIDs, existingTargets) {
		created := s.store.CreateResource("monitors", item)
		_, _ = s.store.RunMonitor(created.ID)
		existingIDs[created.ID] = true
		createdIDs[created.ID] = true
		if key := monitorTargetKey(created.Fields); key != "" {
			existingTargets[key] = true
		}
	}
	s.runDueMonitors(createdIDs)
}

func (s *Server) defaultMonitorResources(existingIDs map[string]bool, existingTargets map[string]bool) []AdminResource {
	now := time.Now().UTC()
	items := []AdminResource{}
	add := func(targetKey string, id string, name string, description string, fields map[string]any) {
		if targetKey == "" || existingTargets[targetKey] || existingIDs[id] {
			return
		}
		fields["managed_by"] = "tokenhub_auto"
		fields["auto_key"] = targetKey
		fields["interval_seconds"] = defaultFloatField(fields, "interval_seconds", 60)
		items = append(items, AdminResource{
			ID:          id,
			Name:        name,
			Description: description,
			Status:      StatusActive,
			Fields:      fields,
			CreatedAt:   now,
		})
	}
	for _, provider := range s.store.ListProviders() {
		add(
			"provider:"+provider.ID,
			autoMonitorID("provider", provider.ID),
			fmt.Sprintf("%s Provider Connectivity", provider.Name),
			"System default check for whether the Provider is enabled and can participate in routing.",
			map[string]any{
				"target_type": "provider",
				"provider_id": provider.ID,
			},
		)
	}
	for _, resource := range s.store.ListProviderResources() {
		add(
			"resource:"+resource.ID,
			autoMonitorID("resource", resource.ID),
			fmt.Sprintf("%s Resource Health", resource.Name),
			"System default check for Provider resource availability.",
			map[string]any{
				"target_type":          "resource",
				"provider_id":          resource.ProviderID,
				"provider_resource_id": resource.ID,
			},
		)
	}
	seenModels := map[string]bool{}
	for _, route := range s.store.ListRoutes() {
		modelName := strings.TrimSpace(route.ModelName)
		if modelName == "" || route.Status != StatusActive || seenModels[modelName] {
			continue
		}
		seenModels[modelName] = true
		add(
			"model:"+modelName,
			autoMonitorID("model", modelName),
			fmt.Sprintf("%s Model Route Heartbeat", modelName),
			"System default check for whether the model API has an enabled route.",
			map[string]any{
				"target_type": "model",
				"model":       modelName,
			},
		)
	}
	return items
}

func autoMonitorID(kind string, target string) string {
	return fmt.Sprintf("mon_auto_%s_%d", kind, stableHashInt(target, 91))
}

func monitorTargetKey(fields map[string]any) string {
	targetType := strings.ToLower(strings.TrimSpace(stringField(fields, "target_type")))
	if targetType == "" {
		targetType = inferMonitorTargetType(fields)
	}
	switch targetType {
	case "provider":
		if providerID := strings.TrimSpace(firstStringField(fields, "provider_id", "provider")); providerID != "" {
			return "provider:" + providerID
		}
	case "resource", "provider_resource":
		if resourceID := strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource")); resourceID != "" {
			return "resource:" + resourceID
		}
	case "model":
		if modelName := strings.TrimSpace(firstStringField(fields, "model", "model_name")); modelName != "" {
			return "model:" + modelName
		}
	}
	return ""
}

func defaultFloatField(fields map[string]any, key string, fallback float64) float64 {
	if value := float64Field(fields, key); value > 0 {
		return value
	}
	return fallback
}

func (s *Server) runDueMonitors(skip map[string]bool) {
	now := time.Now().UTC()
	for _, item := range s.store.ListResources("monitors") {
		if skip[item.ID] || item.Status != StatusActive || !monitorRunDue(item, now) {
			continue
		}
		_, _ = s.store.RunMonitor(item.ID)
	}
}

func monitorRunDue(item AdminResource, now time.Time) bool {
	intervalSeconds := defaultFloatField(item.Fields, "interval_seconds", 60)
	if intervalSeconds < 1 {
		intervalSeconds = 60
	}
	lastCheckedText := strings.TrimSpace(stringField(item.Fields, "last_checked_at"))
	if lastCheckedText == "" {
		return true
	}
	lastChecked, err := time.Parse(time.RFC3339, lastCheckedText)
	if err != nil {
		return true
	}
	return now.Sub(lastChecked) >= time.Duration(intervalSeconds)*time.Second
}

func (s *Server) ensureDefaultAlertRules() {
	existing := s.store.ListResources("alert-rules")
	existingIDs := map[string]bool{}
	existingKeys := map[string]bool{}
	for _, item := range existing {
		existingIDs[item.ID] = true
		if key := alertRuleKey(item.Fields); key != "" {
			existingKeys[key] = true
		}
	}
	for _, item := range defaultAlertRuleResources(existingIDs, existingKeys) {
		created := s.store.CreateResource("alert-rules", item)
		existingIDs[created.ID] = true
		if key := alertRuleKey(created.Fields); key != "" {
			existingKeys[key] = true
		}
	}
}

func defaultAlertRuleResources(existingIDs map[string]bool, existingKeys map[string]bool) []AdminResource {
	now := time.Now().UTC()
	items := []AdminResource{}
	add := func(ruleKey string, id string, name string, description string, metric string, threshold string, severity string, scope string, eventCodes []string) {
		if ruleKey == "" || existingKeys[ruleKey] || existingIDs[id] {
			return
		}
		fields := map[string]any{
			"rule_key":    ruleKey,
			"metric":      metric,
			"threshold":   threshold,
			"severity":    severity,
			"scope":       scope,
			"channel":     "default",
			"event_codes": strings.Join(eventCodes, ","),
			"managed_by":  "tokenhub_auto",
		}
		items = append(items, AdminResource{
			ID:          id,
			Name:        name,
			Description: description,
			Status:      StatusActive,
			Fields:      fields,
			CreatedAt:   now,
		})
	}
	add(
		"provider_health_failed",
		"alr_default_provider_health",
		"Provider Unavailable Alert",
		"Triggered when Provider health checks fail or the Provider is disabled.",
		"provider_health",
		"failed",
		"critical",
		"provider",
		[]string{"monitor_check_failed"},
	)
	add(
		"provider_resource_health_failed",
		"alr_default_provider_resource_health",
		"Provider Resource Unavailable Alert",
		"Triggered when a resource check fails, the resource is disabled, or it enters cooldown.",
		"provider_resource_health",
		"failed",
		"warning",
		"provider_resource",
		[]string{"monitor_check_failed", "provider_resource_cooling_down"},
	)
	add(
		"request_quota_near_limit",
		"alr_default_quota_requests",
		"Request Quota Alert",
		"Triggered when request usage reaches the quota threshold or requests are rejected by quota.",
		"request_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"quota_exceeded"},
	)
	add(
		"token_quota_near_limit",
		"alr_default_quota_tokens",
		"Token Quota Alert",
		"Triggered when daily or monthly token usage reaches the quota threshold.",
		"token_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"daily_tokens_near_limit", "monthly_tokens_near_limit"},
	)
	add(
		"cost_quota_near_limit",
		"alr_default_quota_cost",
		"Cost Quota Alert",
		"Triggered when daily or monthly cost reaches the quota threshold.",
		"cost_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"daily_cost_near_limit", "monthly_cost_near_limit"},
	)
	return items
}

func alertRuleKey(fields map[string]any) string {
	if ruleKey := strings.TrimSpace(stringField(fields, "rule_key")); ruleKey != "" {
		return ruleKey
	}
	metric := strings.TrimSpace(stringField(fields, "metric"))
	if metric == "" {
		return ""
	}
	scope := strings.TrimSpace(stringField(fields, "scope"))
	threshold := strings.TrimSpace(stringField(fields, "threshold"))
	return "metric:" + metric + ":" + scope + ":" + threshold
}

func (s *Server) handleAdminMonitorRun(w http.ResponseWriter, r *http.Request, user AdminUser, monitorID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	result, err := s.store.RunMonitor(monitorID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "run", "monitor", monitorID, "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminSQLiteBackups(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListSQLiteBackups()})
	case http.MethodPost:
		var req struct {
			ExpireDays int `json:"expire_days"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := s.decodeJSON(w, r, &req); err != nil {
				writeError(w, r, err)
				return
			}
		}
		backup, err := s.store.CreateSQLiteBackup(user.ID, req.ExpireDays)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "sqlite_backup", backup.ID, "", backup)
		writeJSON(w, http.StatusCreated, backup)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminSQLiteBackupItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/sqlite/backups/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	backupID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			backup, err := s.store.GetSQLiteBackup(backupID)
			if err != nil {
				writeError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, backup)
		case http.MethodDelete:
			before, _ := s.store.GetSQLiteBackup(backupID)
			if err := s.store.DeleteSQLiteBackup(backupID); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "sqlite_backup", backupID, before, nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	switch parts[1] {
	case "download":
		if r.Method != http.MethodGet {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		backup, err := s.store.GetSQLiteBackup(backupID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if backup.Status != "ready" && backup.Status != "restored" {
			writeError(w, r, NewHTTPError(409, "backup_not_ready", "Backup is not ready to download"))
			return
		}
		if _, err := os.Stat(backup.FilePath); err != nil {
			writeError(w, r, NewHTTPError(404, "backup_file_missing", "Backup file is missing"))
			return
		}
		w.Header().Set("content-type", "application/vnd.sqlite3")
		w.Header().Set("content-disposition", `attachment; filename="`+backup.FileName+`"`)
		http.ServeFile(w, r, backup.FilePath)
		s.recordAdminAudit(r, user, "download", "sqlite_backup", backupID, "", map[string]any{"file_name": backup.FileName})
	case "restore":
		if r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		var req struct {
			Confirmation string `json:"confirmation"`
		}
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		if strings.TrimSpace(req.Confirmation) != "RESTORE "+backupID {
			writeError(w, r, NewHTTPError(400, "invalid_restore_confirmation", "Restore confirmation is invalid"))
			return
		}
		before, _ := s.store.GetSQLiteBackup(backupID)
		backup, err := s.store.RestoreSQLiteBackup(backupID, user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "restore", "sqlite_backup", backupID, before, backup)
		writeJSON(w, http.StatusOK, backup)
	default:
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
	}
}

func (s *Server) handleAdminInvoiceAction(w http.ResponseWriter, r *http.Request, user AdminUser, invoiceID string, action string) {
	if action != "confirm" && action != "reject" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		InvoiceNote  string `json:"invoice_note"`
		RejectReason string `json:"reject_reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	invoice, err := s.findResource("invoices", invoiceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload := invoiceDecisionPayload(invoice, action, req.InvoiceNote, req.RejectReason)
	if approval, required := s.approvalRequired(user, "invoice_"+action, "invoices", invoiceID, payload); required {
		s.recordAdminAudit(r, user, "request_approval", "invoices", approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	updated, err := s.applyInvoiceDecision(invoice, action, user, req.InvoiceNote, req.RejectReason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, action, "invoices", invoiceID, invoice, updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminUsageSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.usageSummaryForUser(user))
}

func (s *Server) handleAdminUsageBreakdown(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.usageBreakdownForUser(user))
}

func (s *Server) handleAdminUsageTimeseries(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.usageTimeseriesForUser(user, 31)})
}

func (s *Server) handleAdminGenerateBilling(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	var req struct {
		Period string `json:"period"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	result, err := s.store.GenerateBillingPeriod(req.Period)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "generate", "billing", stringifyCSV(result["period"]), "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminRequestDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	requestID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/audit/requests/"), "/")
	if requestID == "" || strings.Contains(requestID, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	detail, err := s.store.GetRequestDetail(requestID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	log, ok := detail["log"].(RequestLog)
	if !ok {
		writeError(w, r, NewHTTPError(500, "internal_error", "Request detail is missing request log"))
		return
	}
	if !s.canAccessRequestLog(user, log) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to access this request"))
		return
	}
	s.redactProviderCostsForUser(user, detail)
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleAdminAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "admin_audit", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAuditEvents()})
}

func (s *Server) handleAdminExport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	kind := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/export/"), "/")
	if kind == "" || strings.Contains(kind, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if !s.canExportKind(user, kind) {
		writeError(w, r, NewHTTPError(403, "export_forbidden", "Export is not available for this user"))
		return
	}
	periodFilter := normalizeExportPeriod(r.URL.Query().Get("period"))
	w.Header().Set("content-type", "text/csv; charset=utf-8")
	w.Header().Set("content-disposition", `attachment; filename="tokenhub-`+kind+`.csv"`)
	writer := csv.NewWriter(w)
	switch kind {
	case "requests":
		_ = writer.Write([]string{"created_at", "request_id", "project_id", "api_key_id", "model", "provider_id", "provider_resource_id", "status_code", "error_code", "latency_ms"})
		for _, item := range s.filterRequestLogsForUser(user, s.store.ListRequestLogs()) {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.RequestID,
				item.ProjectID,
				item.APIKeyID,
				item.ModelName,
				item.ProviderID,
				item.ProviderResourceID,
				strconv.Itoa(item.StatusCode),
				item.ErrorCode,
				strconv.FormatInt(item.LatencyMS, 10),
			})
		}
	case "usage":
		_ = writer.Write([]string{"dimension", "id", "request_count", "input_tokens", "cached_input_tokens", "output_tokens", "total_tokens", "estimated_cost_usd"})
		records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
		if periodFilter != "" {
			filtered := make([]UsageRecord, 0, len(records))
			start := periodStart(periodFilter)
			end := periodEnd(periodFilter)
			for _, record := range records {
				if !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
					filtered = append(filtered, record)
				}
			}
			records = filtered
		}
		breakdown := s.usageBreakdownFromRecords(records)
		for _, dimension := range []string{"projects", "models", "providers", "provider_resources", "cost_centers"} {
			rows, _ := breakdown[dimension].([]map[string]any)
			for _, row := range rows {
				_ = writer.Write([]string{
					dimension,
					stringifyCSV(row["id"]),
					stringifyCSV(row["request_count"]),
					stringifyCSV(row["input_tokens"]),
					stringifyCSV(row["cached_input_tokens"]),
					stringifyCSV(row["output_tokens"]),
					stringifyCSV(row["total_tokens"]),
					stringifyCSV(row["estimated_cost_usd"]),
				})
			}
		}
	case "cost-centers":
		s.writeResourceExport(writer, user, "cost-centers", "", []resourceExportColumn{
			{Header: "code", Field: "code"},
			{Header: "name", Source: "name"},
			{Header: "department", Field: "department"},
			{Header: "owner", Field: "owner"},
			{Header: "monthly_budget_usd", Field: "monthly_budget_usd"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "budgets":
		s.writeResourceExport(writer, user, "budgets", periodFilter, []resourceExportColumn{
			{Header: "name", Source: "name"},
			{Header: "scope", Field: "scope"},
			{Header: "scope_id", Field: "scope_id"},
			{Header: "period", Field: "period"},
			{Header: "period_ref", Field: "period_ref"},
			{Header: "amount_usd", Field: "amount_usd"},
			{Header: "warn_percent", Field: "warn_percent"},
			{Header: "used_usd", Field: "used_usd"},
			{Header: "remaining_usd", Field: "remaining_usd"},
			{Header: "usage_percent", Field: "usage_percent"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "chargebacks":
		s.writeResourceExport(writer, user, "chargebacks", periodFilter, []resourceExportColumn{
			{Header: "period", Field: "period"},
			{Header: "cost_center", Field: "cost_center"},
			{Header: "team_id", Field: "team_id"},
			{Header: "project_id", Field: "project_id"},
			{Header: "allocated_cost_usd", Field: "allocated_cost_usd"},
			{Header: "request_count", Field: "request_count"},
			{Header: "input_tokens", Field: "input_tokens"},
			{Header: "cached_input_tokens", Field: "cached_input_tokens"},
			{Header: "output_tokens", Field: "output_tokens"},
			{Header: "total_tokens", Field: "total_tokens"},
			{Header: "allocation_rule", Field: "allocation_rule"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "invoices":
		s.writeResourceExport(writer, user, "invoices", periodFilter, []resourceExportColumn{
			{Header: "period", Field: "period"},
			{Header: "cost_center", Field: "cost_center"},
			{Header: "amount_usd", Field: "amount_usd"},
			{Header: "invoice_note", Field: "invoice_note"},
			{Header: "confirmed_by", Field: "confirmed_by"},
			{Header: "confirmed_at", Field: "confirmed_at"},
			{Header: "reject_reason", Field: "reject_reason"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "approvals":
		_ = writer.Write([]string{"created_at", "id", "trigger", "resource_type", "resource_id", "requester", "status", "decided_by", "decided_at", "reason"})
		for _, item := range s.filterApprovalRequestsForUser(user, s.store.ListApprovalRequests()) {
			decidedAt := ""
			if item.DecidedAt != nil {
				decidedAt = item.DecidedAt.Format(time.RFC3339)
			}
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.ID,
				item.Trigger,
				item.ResourceType,
				item.ResourceID,
				item.Requester,
				item.Status,
				item.DecidedBy,
				decidedAt,
				item.Reason,
			})
		}
	case "audit-events":
		_ = writer.Write([]string{"created_at", "actor_user_id", "actor_name", "actor_role", "action", "resource_type", "resource_id", "status", "message", "ip"})
		for _, item := range s.store.ListAuditEvents() {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.ActorUserID,
				item.ActorName,
				item.ActorRole,
				item.Action,
				item.ResourceType,
				item.ResourceID,
				item.Status,
				item.Message,
				item.IP,
			})
		}
	case "alert-deliveries":
		_ = writer.Write([]string{"created_at", "alert_id", "channel_id", "channel", "target", "status", "status_code", "error"})
		for _, item := range s.store.ListAlertDeliveries() {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.AlertID,
				item.ChannelID,
				item.Channel,
				item.Target,
				item.Status,
				strconv.Itoa(item.StatusCode),
				item.Error,
			})
		}
	default:
		items := s.filterResourcesForUser(user, kind, s.store.ListResources(kind))
		_ = writer.Write([]string{"id", "kind", "name", "status", "description", "fields", "updated_at"})
		for _, item := range items {
			_ = writer.Write([]string{
				item.ID,
				item.Kind,
				item.Name,
				item.Status,
				item.Description,
				snapshotJSON(item.Fields),
				item.UpdatedAt.Format(time.RFC3339),
			})
		}
	}
	writer.Flush()
	s.recordAdminAudit(r, user, "export", kind, "", "", map[string]any{"format": "csv", "period": periodFilter})
}
