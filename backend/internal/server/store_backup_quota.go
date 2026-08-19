package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) CreateSQLiteBackup(createdBy string, expireDays int) (SQLiteBackupRecord, error) {
	if s.IsPostgreSQL() {
		return s.CreatePostgreSQLBackup(createdBy, expireDays)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	backupID := NewID("bak")
	fileName := backupID + ".sqlite3"
	filePath := filepath.Join(defaultString(s.backupDir, "data/backups"), fileName)
	record := SQLiteBackupRecord{
		ID:        backupID,
		Name:      "SQLite Backup " + now.Format("2006-01-02 15:04:05"),
		FileName:  fileName,
		FilePath:  filePath,
		Status:    "creating",
		Trigger:   "manual",
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	if expireDays > 0 {
		expiresAt := now.AddDate(0, 0, expireDays)
		record.ExpiresAt = &expiresAt
	}
	if err := s.db.Create(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	if err := s.copySQLiteDatabase(filePath, false); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	checksum, err := fileSHA256(filePath)
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	record.Status = "ready"
	record.SizeBytes = info.Size()
	record.ChecksumSHA256 = checksum
	record.Error = ""
	if err := s.db.Save(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	return record, nil
}

func (s *GormStore) ListSQLiteBackups() []SQLiteBackupRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	var records []SQLiteBackupRecord
	_ = s.db.Order("created_at desc").Find(&records).Error
	return records
}

func (s *GormStore) GetSQLiteBackup(id string) (SQLiteBackupRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getSQLiteBackupLocked(id)
}

func (s *GormStore) RestoreSQLiteBackup(id string, restoredBy string) (SQLiteBackupRecord, error) {
	if s.IsPostgreSQL() {
		return s.RestorePostgreSQLBackup(id, restoredBy)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.getSQLiteBackupLocked(id)
	if err != nil {
		return SQLiteBackupRecord{}, err
	}
	if record.Status != "ready" && record.Status != "restored" {
		return SQLiteBackupRecord{}, NewHTTPError(409, "backup_not_ready", "Backup is not ready to restore")
	}
	if _, err := os.Stat(record.FilePath); err != nil {
		return SQLiteBackupRecord{}, NewHTTPError(404, "backup_file_missing", "Backup file is missing")
	}
	if record.ChecksumSHA256 != "" {
		checksum, err := fileSHA256(record.FilePath)
		if err != nil {
			return SQLiteBackupRecord{}, err
		}
		if !strings.EqualFold(checksum, record.ChecksumSHA256) {
			return SQLiteBackupRecord{}, NewHTTPError(409, "backup_checksum_mismatch", "Backup checksum does not match")
		}
	}
	if err := s.copySQLiteDatabase(record.FilePath, true); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	now := time.Now().UTC()
	record.Status = "restored"
	record.RestoredBy = restoredBy
	record.RestoredAt = &now
	record.Error = ""
	if err := s.db.Save(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	return record, nil
}

func (s *GormStore) DeleteSQLiteBackup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.getSQLiteBackupLocked(id)
	if err != nil {
		return err
	}
	if record.FilePath != "" {
		if err := os.Remove(record.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.db.Delete(&SQLiteBackupRecord{}, "id = ?", id).Error; err != nil {
		return err
	}
	return nil
}

func (s *GormStore) getSQLiteBackupLocked(id string) (SQLiteBackupRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SQLiteBackupRecord{}, NewHTTPError(404, "backup_not_found", "Backup not found")
	}
	var record SQLiteBackupRecord
	if err := s.db.First(&record, "id = ?", id).Error; err != nil {
		return SQLiteBackupRecord{}, notFound(err, "backup_not_found", "Backup not found")
	}
	return record, nil
}

func (s *GormStore) copySQLiteDatabase(path string, restore bool) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	srcDB := sqlDB
	destDB := sqlDB
	var external *sql.DB
	if restore {
		external, err = sql.Open("sqlite3", path)
		srcDB = external
	} else {
		_ = os.Remove(path)
		external, err = sql.Open("sqlite3", path)
		destDB = external
	}
	if err != nil {
		return err
	}
	defer external.Close()
	destConn, err := destDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer destConn.Close()
	srcConn, err := srcDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer srcConn.Close()
	return withSQLiteConn(destConn, func(dest *sqlite3.SQLiteConn) error {
		return withSQLiteConn(srcConn, func(src *sqlite3.SQLiteConn) error {
			backup, err := dest.Backup("main", src, "main")
			if err != nil {
				return err
			}
			// Finish only releases the backup handle; the copy's success is decided
			// by Step above, and this runs in a defer where nothing could act on a
			// failure anyway.
			defer backup.Finish() //nolint:errcheck // release-only, result not actionable
			for {
				done, err := backup.Step(64)
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	})
}

func withSQLiteConn(conn *sql.Conn, fn func(*sqlite3.SQLiteConn) error) error {
	var err error
	rawErr := conn.Raw(func(driverConn any) error {
		sqliteConn, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("sqlite connection expected, got %T", driverConn)
		}
		err = fn(sqliteConn)
		return err
	})
	if rawErr != nil {
		return rawErr
	}
	return err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *GormStore) AccessibleModels(key APIKey) []Model {
	s.mu.Lock()
	defer s.mu.Unlock()

	var privateKey APIKey
	if err := s.db.First(&privateKey, "id = ?", key.ID).Error; err != nil {
		return nil
	}
	hydrateAPIKey(&privateKey)
	var project Project
	if err := s.db.First(&project, "id = ?", privateKey.ProjectID).Error; err != nil {
		return nil
	}
	hydrateProject(&project)
	var policyResources []AdminResource
	if err := s.db.Where("kind = ?", routingPolicyResourceKind).Order("created_at asc").Find(&policyResources).Error; err != nil {
		return nil
	}
	policy, err := effectiveScopedRoutingPolicy(policyResources, project.ID, privateKey.ID)
	if err != nil {
		return nil
	}
	var routes []ModelRoute
	if err := s.db.Where("status = ?", StatusActive).Find(&routes).Error; err != nil {
		return nil
	}
	var providerResources []ProviderResource
	if err := s.db.Where("status = ?", StatusActive).Find(&providerResources).Error; err != nil {
		return nil
	}
	resourcesByID := make(map[string]*ProviderResource, len(providerResources))
	resourcesByProvider := make(map[string][]*ProviderResource)
	for index := range providerResources {
		resource := &providerResources[index]
		resourcesByID[resource.ID] = resource
		resourcesByProvider[resource.ProviderID] = append(resourcesByProvider[resource.ProviderID], resource)
	}
	publishedModelNames := make([]string, 0, len(routes)+1)
	seenModelNames := map[string]bool{}
	for _, route := range routes {
		if route.ModelName == codexImageModelName || seenModelNames[route.ModelName] || !modelAllowedByScopes(project, privateKey, route.ModelName) {
			continue
		}
		selections := []RouteSelection{{Provider: Provider{ID: route.ProviderID}, ProviderModel: route.ProviderModel, Route: route}}
		if route.ProviderResourceID != "" {
			selections[0].Resource = resourcesByID[route.ProviderResourceID]
		} else if resources := resourcesByProvider[route.ProviderID]; len(resources) > 0 {
			selections = make([]RouteSelection, 0, len(resources))
			for _, resource := range resources {
				resourceRoute := route
				resourceRoute.ProviderResourceID = resource.ID
				selections = append(selections, RouteSelection{Provider: Provider{ID: route.ProviderID}, Resource: resource, ProviderModel: route.ProviderModel, Route: resourceRoute})
			}
		}
		call := CallContext{Project: project, Key: privateKey, Model: Model{Name: route.ModelName}}
		for _, selection := range selections {
			if len(routingPolicyCandidateReasons(call, selection, policy)) != 0 {
				continue
			}
			seenModelNames[route.ModelName] = true
			publishedModelNames = append(publishedModelNames, route.ModelName)
			break
		}
	}
	if modelAllowedByScopes(project, privateKey, codexImageModelName) && s.codexImageAllowedByPolicyLocked(project, privateKey, policy) {
		seenModelNames[codexImageModelName] = true
		publishedModelNames = append(publishedModelNames, codexImageModelName)
	}
	var models []Model
	if err := s.db.Where("status = ?", StatusActive).
		Where("name IN ?", publishedModelNames).
		Order("name asc").
		Find(&models).Error; err != nil {
		return nil
	}
	items := make([]Model, 0, len(models))
	items = append(items, models...)
	return items
}

func (s *GormStore) codexImageAllowedByPolicyLocked(project Project, key APIKey, policy *ScopedRoutingPolicy) bool {
	var routes []ModelRoute
	if err := s.db.Where("model_name = ? AND provider_model = ? AND status = ?", codexImageModelName, codexImageUpstreamModel, StatusActive).Find(&routes).Error; err != nil {
		return false
	}
	for _, configuredRoute := range routes {
		var provider Provider
		if err := s.db.Where("id = ? AND type = ? AND status = ? AND healthy = ?", configuredRoute.ProviderID, ProviderOpenAICodex, StatusActive, true).First(&provider).Error; err != nil {
			continue
		}
		var resources []ProviderResource
		query := s.db.Where("provider_id = ? AND status = ? AND healthy = ?", provider.ID, StatusActive, true)
		if configuredRoute.ProviderResourceID != "" {
			query = query.Where("id = ?", configuredRoute.ProviderResourceID)
		} else if strings.TrimSpace(configuredRoute.ResourceGroup) != "" {
			query = query.Where("\"group\" = ?", strings.TrimSpace(configuredRoute.ResourceGroup))
		}
		if err := query.Find(&resources).Error; err != nil {
			continue
		}
		for index := range resources {
			resource := &resources[index]
			if !isOpenAIAccountResource(resource.ResourceType) || !s.codexImageResourceAvailable(*resource) {
				continue
			}
			resourceRoute := configuredRoute
			resourceRoute.ProviderResourceID = resource.ID
			route := RouteSelection{
				Provider: provider, Resource: resource, ProviderModel: codexImageUpstreamModel,
				Route: resourceRoute,
			}
			call := CallContext{Project: project, Key: key, Model: Model{Name: codexImageModelName}}
			if len(routingPolicyCandidateReasons(call, route, policy)) == 0 {
				return true
			}
		}
	}
	return false
}

func (s *GormStore) codexImageResourceAvailable(resource ProviderResource) bool {
	switch strings.TrimSpace(resource.Options[codexImageCapabilityOption]) {
	case codexImageCapabilitySupported:
		return true
	case codexImageCapabilityUnsupported:
		checkedAt, err := time.Parse(time.RFC3339Nano, resource.Options[codexImageCapabilityCheckedAtOption])
		return err == nil && s.imageCapabilityRetry > 0 && !time.Now().Before(checkedAt.Add(s.imageCapabilityRetry))
	default:
		return false
	}
}

func (s *GormStore) quotaBucketForUpdate(tx *gorm.DB, keyID, scope, bucket string) (QuotaBucket, error) {
	seed := QuotaBucket{KeyID: keyID, Scope: scope, Bucket: bucket}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return QuotaBucket{}, err
	}
	query := tx
	if s.dbDriver == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var item QuotaBucket
	if err := query.First(&item, "key_id = ? AND scope = ? AND bucket = ?", keyID, scope, bucket).Error; err != nil {
		return QuotaBucket{}, err
	}
	return item, nil
}

func priceUsage(model Model, usage Usage) Usage {
	// Upstream-reported usage is untrusted: the provider parsers preserve the
	// sign of whatever the upstream sent, and a negative count would flow into
	// addUsage and shrink the day/month quota counters, letting a key keep
	// spending past its configured limits. Clamping PromptTokens first also
	// keeps the CachedInputTokens clamp below from going negative again.
	usage.PromptTokens = maxInt64(usage.PromptTokens, 0)
	usage.CompletionTokens = maxInt64(usage.CompletionTokens, 0)
	usage.TotalTokens = maxInt64(usage.TotalTokens, 0)
	if usage.TotalTokens == 0 {
		usage.TotalTokens = saturatingAddNonNegative(usage.PromptTokens, usage.CompletionTokens)
	}
	usage.CachedInputTokens = minInt64(maxInt64(usage.CachedInputTokens, 0), usage.PromptTokens)
	if usage.CostUSD == 0 {
		if model.Modality == "embedding" && model.EmbeddingPriceUSDPer1M > 0 {
			usage.CostUSD = float64(usage.TotalTokens) * model.EmbeddingPriceUSDPer1M / 1_000_000
		} else {
			uncachedInputTokens := usage.PromptTokens - usage.CachedInputTokens
			cacheReadPrice := effectiveCacheReadPriceUSDPer1M(model)
			usage.CostUSD = float64(uncachedInputTokens)*model.InputPriceUSDPer1M/1_000_000 +
				float64(usage.CachedInputTokens)*cacheReadPrice/1_000_000 +
				float64(usage.CompletionTokens)*model.OutputPriceUSDPer1M/1_000_000
		}
	}
	return usage
}

const (
	defaultCacheReadEstimateRatio  = 0.10
	deepSeekCacheReadEstimateRatio = 0.02
	deepSeekV4ProCacheReadRatio    = 1.0 / 120
)

func effectiveCacheReadPriceUSDPer1M(model Model) float64 {
	if model.Modality == "embedding" {
		return 0
	}
	if model.CacheReadPriceUSDPer1M > 0 {
		return model.CacheReadPriceUSDPer1M
	}
	for _, key := range []string{"cached_input_price_usd_per_1m", "cache_read_price_usd_per_1m", "cached_read_price_usd_per_1m"} {
		if value, err := strconv.ParseFloat(strings.TrimSpace(model.Metadata[key]), 64); err == nil && value > 0 {
			return value
		}
	}
	if model.InputPriceUSDPer1M <= 0 {
		return 0
	}
	ratio := defaultCacheReadEstimateRatio
	category := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.Name, model.Family)))
	if category == "deepseek" {
		ratio = deepSeekCacheReadEstimateRatio
		if strings.Contains(strings.ToLower(model.Name+" "+model.Family), "v4-pro") {
			ratio = deepSeekV4ProCacheReadRatio
		}
	}
	return model.InputPriceUSDPer1M * ratio
}

func minInt64(left int64, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func raiseQuotaAlerts(tx *gorm.DB, key APIKey, dayCounter, monthCounter *QuotaCounter) error {
	checks := []struct {
		limit     float64
		current   float64
		code      string
		message   string
		scopeType string
	}{
		{float64(key.Limits.DailyTokens), float64(dayCounter.TotalTokens), "daily_tokens_near_limit", "Daily token quota is near or above limit", "api_key"},
		{float64(key.Limits.MonthlyTokens), float64(monthCounter.TotalTokens), "monthly_tokens_near_limit", "Monthly token quota is near or above limit", "api_key"},
		{key.Limits.DailyCostUSD, dayCounter.CostUSD, "daily_cost_near_limit", "Daily cost quota is near or above limit", "api_key"},
		{key.Limits.MonthlyCostUSD, monthCounter.CostUSD, "monthly_cost_near_limit", "Monthly cost quota is near or above limit", "api_key"},
	}
	for _, check := range checks {
		if check.limit <= 0 || check.current < check.limit {
			continue
		}
		if err := tx.Create(&AlertEvent{
			ID:         NewID("alt"),
			ScopeType:  check.scopeType,
			ScopeID:    key.ID,
			Severity:   "warning",
			Code:       check.code,
			Message:    check.message,
			ResourceID: key.ProjectID,
			CreatedAt:  time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

type MinuteLimitScopes struct {
	RPM string
	TPM string
}

func quotaPolicyLimits(tx *gorm.DB, project Project, key APIKey) (QuotaLimits, MinuteLimitScopes, error) {
	var resources []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "quota-policies", StatusActive).
		Order("created_at asc, id asc").Find(&resources).Error; err != nil {
		return QuotaLimits{}, MinuteLimitScopes{}, err
	}
	var limits QuotaLimits
	var scopes MinuteLimitScopes
	for _, resource := range resources {
		scope := strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "scope_type")))
		}
		scopeID := strings.TrimSpace(stringField(resource.Fields, "scope_id"))
		if !quotaPolicyApplies(scope, scopeID, project, key) {
			continue
		}
		candidate := QuotaLimits{
			RateLimitRPM:    int64Field(resource.Fields, "rate_limit_rpm"),
			TokenLimitTPM:   int64Field(resource.Fields, "token_limit_tpm"),
			DailyRequests:   int64Field(resource.Fields, "daily_requests"),
			MonthlyRequests: int64Field(resource.Fields, "monthly_requests"),
			DailyTokens:     int64Field(resource.Fields, "daily_tokens"),
			MonthlyTokens:   int64Field(resource.Fields, "monthly_tokens"),
			DailyCostUSD:    float64Field(resource.Fields, "daily_cost_usd"),
			MonthlyCostUSD:  float64Field(resource.Fields, "monthly_cost_usd"),
			MaxConcurrency:  int64Field(resource.Fields, "max_concurrency"),
		}
		if strictLimitChanged(limits.RateLimitRPM, candidate.RateLimitRPM) {
			scopes.RPM = normalizedQuotaPolicyScope(scope)
		}
		if strictLimitChanged(limits.TokenLimitTPM, candidate.TokenLimitTPM) {
			scopes.TPM = normalizedQuotaPolicyScope(scope)
		}
		limits = mergeQuotaLimits(limits, candidate)
	}
	return limits, scopes, nil
}

func validateQuotaPolicyMinuteLimits(fields map[string]any) error {
	for _, key := range []string{"rate_limit_rpm", "token_limit_tpm"} {
		value, ok := fields[key]
		if !ok || value == nil {
			continue
		}
		if !validNonNegativeInt64(value) {
			return NewHTTPError(http.StatusBadRequest, "invalid_quota_policy_rate_limit", "Quota policy RPM and TPM limits must be non-negative 64-bit integers")
		}
	}
	return nil
}

func validNonNegativeInt64(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed >= 0
	case int64:
		return typed >= 0
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0) && typed >= 0 && typed < math.Exp2(63) && math.Trunc(typed) == typed
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return err == nil && parsed >= 0
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return err == nil && parsed >= 0
	default:
		return false
	}
}

func strictLimitChanged(current int64, candidate int64) bool {
	return candidate > 0 && (current <= 0 || candidate < current)
}

func normalizedQuotaPolicyScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "project":
		return "project"
	case "team":
		return "team"
	case "api_key", "key":
		return "api_key"
	default:
		return "global"
	}
}

func quotaPolicyApplies(scope string, scopeID string, project Project, key APIKey) bool {
	if scope == "" || scope == "global" || scope == "organization" {
		return scopeID == "" || scopeID == "default" || scopeID == "global"
	}
	switch scope {
	case "project":
		return scopeID == "" || scopeID == project.ID
	case "api_key", "key":
		return scopeID == "" || scopeID == key.ID
	case "team":
		return scopeID == "" || scopeID == project.TeamID
	default:
		return false
	}
}

// runtimeBudgetCandidate is an active budget that can still block the current
// call: enforced, covering the current period, and carrying a positive amount.
type runtimeBudgetCandidate struct {
	scope   string
	scopeID string
	amount  float64
}

// runtimeBudgetScopes records which scope totals the applicable budgets read, so
// the aggregation only loads the data those budgets actually need.
type runtimeBudgetScopes struct {
	global     bool
	team       bool
	costCenter bool
}

// runtimeBudgetTotals holds the current period's spend per budget scope. Every
// value is derived from one aggregate query, so admission issues a constant
// number of statements no matter how many budgets or usage records exist.
type runtimeBudgetTotals struct {
	global     float64
	project    float64
	team       float64
	costCenter float64
}

func (t runtimeBudgetTotals) forScope(scope string) float64 {
	switch scope {
	case "project":
		return t.project
	case "team":
		return t.team
	case "cost_center", "cost-center":
		return t.costCenter
	case "global", "organization":
		return t.global
	default:
		return 0
	}
}

// projectPeriodCost is one row of the per-project spend aggregate.
type projectPeriodCost struct {
	ProjectID string
	Total     float64
}

// checkRuntimeBudget rejects the call when an enforced budget covering this
// project is already spent. It filters budgets in two phases so a deployment
// without matching budgets never reads the usage tables: the candidate pass
// needs the budget rows alone, and the cost center, project, and usage lookups
// only happen once a candidate is known to apply.
//
// The caller supplies now so budget enforcement resolves its period from the
// same database clock reading that admission uses for the quota buckets. A
// local reading would place the two on different months whenever the database
// and this host disagree across a period boundary.
func (s *GormStore) checkRuntimeBudget(tx *gorm.DB, project Project, now time.Time) error {
	now = now.UTC()
	period := now.Format("2006-01")
	var budgets []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "budgets", StatusActive).Find(&budgets).Error; err != nil {
		return err
	}
	candidates, hasCostCenterScope := runtimeBudgetCandidates(budgets, now, period)
	if len(candidates) == 0 {
		return nil
	}
	// The cost center only decides whether cost center scoped budgets apply, so
	// deployments without one never read teams or quota policies here.
	costCenter := ""
	var teamsByID, quotasByID map[string]AdminResource
	if hasCostCenterScope {
		var err error
		teamsByID, quotasByID, err = loadCostCenterResources(tx)
		if err != nil {
			return err
		}
		costCenter = costCenterForProject(project, teamsByID, quotasByID)
	}
	applicable := make([]runtimeBudgetCandidate, 0, len(candidates))
	var scopes runtimeBudgetScopes
	for _, candidate := range candidates {
		if !budgetScopeAppliesToProject(candidate.scope, candidate.scopeID, project, costCenter) {
			continue
		}
		applicable = append(applicable, candidate)
		switch candidate.scope {
		case "team":
			scopes.team = true
		case "cost_center", "cost-center":
			scopes.costCenter = true
		case "global", "organization":
			scopes.global = true
		}
	}
	if len(applicable) == 0 {
		return nil
	}
	totals, err := s.aggregateRuntimeBudgetTotals(tx, period, project, scopes, costCenter, teamsByID, quotasByID)
	if err != nil {
		return err
	}
	for _, candidate := range applicable {
		if totals.forScope(candidate.scope) >= candidate.amount {
			return ErrBudgetExceeded
		}
	}
	return nil
}

// runtimeBudgetCandidates keeps the budgets that can block a call and reports
// whether any of them is cost center scoped.
func runtimeBudgetCandidates(budgets []AdminResource, now time.Time, period string) ([]runtimeBudgetCandidate, bool) {
	candidates := make([]runtimeBudgetCandidate, 0, len(budgets))
	hasCostCenterScope := false
	for _, budget := range budgets {
		if !budgetEnforced(budget) {
			continue
		}
		if budgetPeriod := strings.TrimSpace(stringField(budget.Fields, "period_ref")); budgetPeriod != "" && normalizeBillingPeriod(budgetPeriod, now) != period {
			continue
		}
		amount := float64Field(budget.Fields, "amount_usd")
		if amount <= 0 {
			continue
		}
		scope := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "scope")))
		candidates = append(candidates, runtimeBudgetCandidate{
			scope:   scope,
			scopeID: budgetScopeID(budget, scope),
			amount:  amount,
		})
		if scope == "cost_center" || scope == "cost-center" {
			hasCostCenterScope = true
		}
	}
	return candidates, hasCostCenterScope
}

// loadCostCenterResources reads the resources costCenterForProject resolves
// against. Like the per-project lookups it replaces, it ignores resource status.
// It does report read errors instead of falling through the cost center chain,
// because a budget resolved against a half-read table would silently admit
// spend the budget was meant to stop.
func loadCostCenterResources(tx *gorm.DB) (map[string]AdminResource, map[string]AdminResource, error) {
	var resources []AdminResource
	if err := tx.Where("kind IN ?", []string{"teams", "quota-policies"}).Find(&resources).Error; err != nil {
		return nil, nil, err
	}
	teamsByID := map[string]AdminResource{}
	quotasByID := map[string]AdminResource{}
	for _, resource := range resources {
		switch resource.Kind {
		case "teams":
			teamsByID[resource.ID] = resource
		case "quota-policies":
			quotasByID[resource.ID] = resource
		}
	}
	return teamsByID, quotasByID, nil
}

// aggregateRuntimeBudgetTotals sums the period once, grouped by project, and
// folds those subtotals into every scope the applicable budgets consult.
func (s *GormStore) aggregateRuntimeBudgetTotals(tx *gorm.DB, period string, project Project, scopes runtimeBudgetScopes, costCenter string, teamsByID, quotasByID map[string]AdminResource) (runtimeBudgetTotals, error) {
	query := tx.Model(&UsageRecord{}).Where("created_at >= ? AND created_at < ?", periodStart(period), periodEnd(period))
	if !scopes.global && !scopes.team && !scopes.costCenter {
		// Only this project's own budgets are in play, so stay on the
		// (project_id, created_at) index instead of scanning the whole period.
		query = query.Where("project_id = ?", project.ID)
	}
	var rows []projectPeriodCost
	if err := query.Select("project_id, COALESCE(SUM(cost_usd), 0) AS total").Group("project_id").Scan(&rows).Error; err != nil {
		return runtimeBudgetTotals{}, err
	}
	var projectsByID map[string]Project
	if scopes.team || scopes.costCenter {
		var projects []Project
		// These are the columns costCenterForProject and the team match read.
		if err := tx.Select("id", "team_id", "cost_center", "default_quota_ref").Find(&projects).Error; err != nil {
			return runtimeBudgetTotals{}, err
		}
		projectsByID = make(map[string]Project, len(projects))
		for _, item := range projects {
			projectsByID[item.ID] = item
		}
	}
	var totals runtimeBudgetTotals
	for _, row := range rows {
		totals.global += row.Total
		if row.ProjectID == project.ID {
			totals.project += row.Total
		}
		if !scopes.team && !scopes.costCenter {
			continue
		}
		// Usage left behind by a deleted project belongs to no team and no cost
		// center, which is what the per-record project lookup used to decide.
		rowProject, ok := projectsByID[row.ProjectID]
		if !ok {
			continue
		}
		if scopes.team && rowProject.TeamID == project.TeamID {
			totals.team += row.Total
		}
		if scopes.costCenter && costCenterForProject(rowProject, teamsByID, quotasByID) == costCenter {
			totals.costCenter += row.Total
		}
	}
	return totals, nil
}

func budgetScopeID(budget AdminResource, scope string) string {
	scopeID := strings.TrimSpace(stringField(budget.Fields, "scope_id"))
	switch scope {
	case "project":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "project_id"))
		}
	case "team":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "team_id"))
		}
	case "cost_center", "cost-center":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "cost_center"))
		}
	}
	return scopeID
}

func budgetEnforced(budget AdminResource) bool {
	mode := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "enforcement")))
	if mode == "warn" || mode == "monitor" || mode == "off" || mode == "disabled" {
		return false
	}
	return true
}

func budgetScopeAppliesToProject(scope string, scopeID string, project Project, costCenter string) bool {
	switch scope {
	case "project":
		return scopeID != "" && scopeID == project.ID
	case "team":
		return scopeID != "" && scopeID == project.TeamID
	case "cost_center", "cost-center":
		return scopeID != "" && scopeID == costCenter
	case "global", "organization":
		return true
	default:
		return false
	}
}

func mergeQuotaLimits(base QuotaLimits, override QuotaLimits) QuotaLimits {
	return QuotaLimits{
		RateLimitRPM:    strictInt64(base.RateLimitRPM, override.RateLimitRPM),
		TokenLimitTPM:   strictInt64(base.TokenLimitTPM, override.TokenLimitTPM),
		DailyRequests:   strictInt64(base.DailyRequests, override.DailyRequests),
		MonthlyRequests: strictInt64(base.MonthlyRequests, override.MonthlyRequests),
		DailyTokens:     strictInt64(base.DailyTokens, override.DailyTokens),
		MonthlyTokens:   strictInt64(base.MonthlyTokens, override.MonthlyTokens),
		DailyCostUSD:    strictFloat64(base.DailyCostUSD, override.DailyCostUSD),
		MonthlyCostUSD:  strictFloat64(base.MonthlyCostUSD, override.MonthlyCostUSD),
		MaxConcurrency:  strictInt64(base.MaxConcurrency, override.MaxConcurrency),
	}
}

func strictInt64(a int64, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func strictFloat64(a float64, b float64) float64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func int64Field(fields map[string]any, key string) int64 {
	if fields == nil {
		return 0
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		var parsed int64
		for _, ch := range strings.TrimSpace(typed) {
			if ch < '0' || ch > '9' {
				return 0
			}
			parsed = parsed*10 + int64(ch-'0')
		}
		return parsed
	default:
		return 0
	}
}

func float64Field(fields map[string]any, key string) float64 {
	if fields == nil {
		return 0
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		var parsed float64
		var scale float64
		for _, ch := range strings.TrimSpace(typed) {
			if ch == '.' && scale == 0 {
				scale = 1
				continue
			}
			if ch < '0' || ch > '9' {
				return 0
			}
			if scale > 0 {
				scale *= 10
				parsed += float64(ch-'0') / scale
			} else {
				parsed = parsed*10 + float64(ch-'0')
			}
		}
		return parsed
	default:
		return 0
	}
}

func (s *GormStore) costCenterForProject(project Project) string {
	return s.costCenterForProjectWithDB(s.db, project)
}

func (s *GormStore) costCenterForProjectWithDB(db *gorm.DB, project Project) string {
	if costCenter := strings.TrimSpace(project.CostCenter); costCenter != "" {
		return costCenter
	}
	if strings.TrimSpace(project.TeamID) != "" {
		var team AdminResource
		if err := db.First(&team, "kind = ? AND id = ?", "teams", project.TeamID).Error; err == nil {
			if costCenter := strings.TrimSpace(stringField(team.Fields, "cost_center")); costCenter != "" {
				return costCenter
			}
		}
	}
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		var quota AdminResource
		if err := db.First(&quota, "kind = ? AND id = ?", "quota-policies", project.DefaultQuotaRef).Error; err == nil {
			if costCenter := strings.TrimSpace(stringField(quota.Fields, "cost_center")); costCenter != "" {
				return costCenter
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

func normalizeBillingPeriod(period string, now time.Time) string {
	period = strings.TrimSpace(period)
	if period == "" {
		return now.UTC().Format("2006-01")
	}
	if len(period) >= 7 {
		return period[:7]
	}
	return now.UTC().Format("2006-01")
}

func periodStart(period string) time.Time {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		t, _ = time.Parse("2006-01", time.Now().UTC().Format("2006-01"))
	}
	return t.UTC()
}

func periodEnd(period string) time.Time {
	return periodStart(period).AddDate(0, 1, 0)
}

func defaultInvoiceNote(period string, costCenter string, amount float64) string {
	return fmt.Sprintf("TokenHub %s AI 用量内部结算，成本中心 %s，金额 USD %.4f。", period, costCenter, roundMoney(amount))
}

func roundMoney(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func sumFloatMap(items map[string]float64) float64 {
	var total float64
	for _, value := range items {
		total += value
	}
	return total
}

func deleteGeneratedResourcesByPeriod(tx *gorm.DB, kind string, period string) error {
	var items []AdminResource
	if err := tx.Where("kind = ?", kind).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if stringField(item.Fields, "period") != period {
			continue
		}
		if generated := strings.TrimSpace(stringField(item.Fields, "generated_by")); generated != "tokenhub" {
			continue
		}
		if err := tx.Delete(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

func updateBudgetsFromUsage(tx *gorm.DB, period string, costCenterTotals map[string]float64, projectTotals map[string]float64, teamTotals map[string]float64) error {
	var budgets []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "budgets", StatusActive).Find(&budgets).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, budget := range budgets {
		scope := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "scope")))
		scopeID := strings.TrimSpace(stringField(budget.Fields, "scope_id"))
		switch scope {
		case "cost_center", "cost-center":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "cost_center"))
			}
		case "project":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "project_id"))
			}
		case "team":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "team_id"))
			}
		default:
			continue
		}
		if scopeID == "" {
			continue
		}
		budgetPeriod := normalizeBillingPeriod(stringField(budget.Fields, "period_ref"), now)
		if budgetPeriod != period && strings.TrimSpace(stringField(budget.Fields, "period_ref")) != "" {
			continue
		}
		amount := float64Field(budget.Fields, "amount_usd")
		used := budgetUsedByScope(scope, scopeID, costCenterTotals, projectTotals, teamTotals)
		budget.Fields["used_usd"] = roundMoney(used)
		budget.Fields["remaining_usd"] = roundMoney(amount - used)
		budget.Fields["usage_percent"] = float64(0)
		if amount > 0 {
			budget.Fields["usage_percent"] = roundMoney(used / amount * 100)
		}
		budget.Fields["last_calculated_period"] = period
		budget.UpdatedAt = now
		if err := tx.Save(&budget).Error; err != nil {
			return err
		}
		warnPercent := float64Field(budget.Fields, "warn_percent")
		if warnPercent <= 0 {
			warnPercent = 80
		}
		if amount > 0 && used/amount*100 >= warnPercent {
			if err := tx.Create(&AlertEvent{
				ID:         NewID("alt"),
				ScopeType:  "budget",
				ScopeID:    budget.ID,
				Severity:   "warning",
				Code:       "budget_warn_threshold",
				Message:    fmt.Sprintf("Budget %s reached %.2f%% for %s", budget.Name, used/amount*100, period),
				ResourceID: scopeID,
				CreatedAt:  now,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func budgetUsedByScope(scope string, scopeID string, costCenterTotals map[string]float64, projectTotals map[string]float64, teamTotals map[string]float64) float64 {
	switch scope {
	case "cost_center", "cost-center":
		return costCenterTotals[scopeID]
	case "project":
		return projectTotals[scopeID]
	case "team":
		return teamTotals[scopeID]
	default:
		return 0
	}
}

func createAdminUser(db *gorm.DB, user AdminUser, password string) (AdminUser, error) {
	now := time.Now().UTC()
	if user.ID == "" {
		user.ID = NewID("usr")
	}
	if user.Username == "" {
		user.Username = user.Email
	}
	if user.Email == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_admin_user", "email is required")
	}
	if user.Name == "" {
		user.Name = user.Username
	}
	if user.Role == "" {
		user.Role = "user"
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	user.TeamIDs = normalizedTeamIDs(user.TeamID, user.TeamIDs)
	if password == "" && user.PasswordHash == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_admin_user", "password is required")
	}
	var count int64
	if err := db.Model(&AdminUser{}).
		Where("username = ? OR email = ?", user.Username, user.Email).
		Count(&count).Error; err != nil {
		return AdminUser{}, err
	}
	if count > 0 {
		return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Username or email already exists")
	}
	if user.PasswordHash == "" {
		passwordHash, err := hashPassword(password)
		if err != nil {
			return AdminUser{}, err
		}
		user.PasswordHash = passwordHash
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	if err := db.Create(&user).Error; err != nil {
		return AdminUser{}, writeConflict(err, "admin_user_conflict", "Username or email already exists")
	}
	return publicAdminUser(user), nil
}
