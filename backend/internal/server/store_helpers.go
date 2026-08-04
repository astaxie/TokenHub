package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func publicKeys(keys []APIKey) []APIKey {
	items := make([]APIKey, 0, len(keys))
	for _, key := range keys {
		hydrateAPIKey(&key)
		items = append(items, publicKey(key))
	}
	return items
}

func hydrateAPIKey(key *APIKey) {
	mode, allowed, err := normalizeModelAccess(key.ModelAccessMode, key.Allowed)
	if err != nil {
		mode, allowed = ModelAccessModeRestricted, []string{}
	}
	key.ModelAccessMode = mode
	key.Allowed = allowed
	key.AllowedModels = AllowedModelSet(key.Allowed)
}

func hydrateProject(project *Project) {
	mode, allowed, err := normalizeModelAccess(project.ModelAccessMode, project.AllowedModels)
	if err != nil {
		mode, allowed = ModelAccessModeRestricted, []string{}
	}
	project.ModelAccessMode = mode
	project.AllowedModels = allowed
}

func normalizeModelAccess(mode string, allowed []string) (string, []string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	allowed = uniqueStrings(allowed)
	sort.Strings(allowed)
	if mode == "" {
		if len(allowed) > 0 {
			mode = ModelAccessModeRestricted
		} else {
			mode = ModelAccessModeInherit
		}
	}
	switch mode {
	case ModelAccessModeInherit:
		return mode, []string{}, nil
	case ModelAccessModeRestricted:
		return mode, allowed, nil
	default:
		return "", nil, NewHTTPError(400, "invalid_model_access_mode", "model_access_mode must be inherit or restricted")
	}
}

func modelAllowedByScopes(project Project, key APIKey, modelName string) bool {
	hydrateProject(&project)
	hydrateAPIKey(&key)
	if project.ModelAccessMode == ModelAccessModeRestricted && !AllowedModelSet(project.AllowedModels)[modelName] {
		return false
	}
	if key.ModelAccessMode == ModelAccessModeRestricted && !key.AllowedModels[modelName] {
		return false
	}
	return true
}

func publicKey(key APIKey) APIKey {
	key.KeyHash = ""
	key.KeyCiphertext = ""
	if key.Allowed == nil && key.AllowedModels != nil {
		key.Allowed = make([]string, 0, len(key.AllowedModels))
		for model := range key.AllowedModels {
			key.Allowed = append(key.Allowed, model)
		}
		sort.Strings(key.Allowed)
	}
	return key
}

func publicAdminUser(user AdminUser) AdminUser {
	user.PasswordHash = ""
	return user
}

func ipAllowed(clientIP string, allowlist []string) bool {
	clientIP = strings.TrimSpace(clientIP)
	if clientIP == "" {
		return false
	}
	parsedIP := net.ParseIP(clientIP)
	for _, item := range allowlist {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" || item == clientIP {
			return true
		}
		if strings.Contains(item, "/") && parsedIP != nil {
			if _, network, err := net.ParseCIDR(item); err == nil && network.Contains(parsedIP) {
				return true
			}
		}
	}
	return false
}

func notFound(err error, code, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewHTTPError(404, code, message)
	}
	return err
}

func writeConflict(err error, code, message string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return NewHTTPError(409, code, message)
	}
	return err
}

func (s *GormStore) encryptSecret(secret string) string {
	if strings.TrimSpace(secret) == "" || strings.HasPrefix(secret, "enc:v1:") {
		return secret
	}
	block, err := aes.NewCipher(secretKeyBytes(s.secretKey))
	if err != nil {
		return secret
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return secret
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return secret
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(secret), nil)
	return "enc:v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...))
}

func (s *GormStore) decryptSecret(secret string) string {
	if !strings.HasPrefix(secret, "enc:v1:") {
		return secret
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "enc:v1:"))
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(secretKeyBytes(s.secretKey))
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	if len(data) < gcm.NonceSize() {
		return ""
	}
	nonce := data[:gcm.NonceSize()]
	ciphertext := data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ""
	}
	return string(plaintext)
}

func secretKeyBytes(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func defaultInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func validProviderResourceBulkAction(action string) bool {
	switch action {
	case "enable", "disable", "test", "clear_error", "reset_usage":
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeRouteProjectScope(scope string, projectIDs []string) (string, []string) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != RouteProjectScopeInclude && scope != RouteProjectScopeExclude {
		return RouteProjectScopeAll, nil
	}
	projectIDs = uniqueStrings(projectIDs)
	sort.Strings(projectIDs)
	return scope, projectIDs
}

func cloneFields(fields map[string]any) map[string]any {
	cloned := map[string]any{}
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

func inferMonitorTargetType(fields map[string]any) string {
	if strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource")) != "" {
		return "resource"
	}
	if strings.TrimSpace(firstStringField(fields, "model", "model_name")) != "" {
		return "model"
	}
	if strings.TrimSpace(firstStringField(fields, "provider_id", "provider")) != "" {
		return "provider"
	}
	return "unknown"
}

func firstStringField(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value := stringField(fields, key)
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func okFailed(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func monitorProviderMessage(provider Provider, healthy bool) string {
	if healthy {
		return "Provider 状态正常"
	}
	return "Provider 未启用或不可用：" + provider.Status
}

func monitorResourceMessage(resource ProviderResource, healthy bool) string {
	if healthy {
		return "Provider 资源实例状态正常"
	}
	return "Provider 资源实例未启用或不可用：" + resource.Status
}

func exceedsRequestQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyRequests > 0 && day.Requests >= limits.DailyRequests) ||
		(limits.MonthlyRequests > 0 && month.Requests >= limits.MonthlyRequests)
}

func exceedsTokenQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyTokens > 0 && day.TotalTokens >= limits.DailyTokens) ||
		(limits.MonthlyTokens > 0 && month.TotalTokens >= limits.MonthlyTokens)
}

func exceedsCostQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyCostUSD > 0 && day.CostUSD >= limits.DailyCostUSD) ||
		(limits.MonthlyCostUSD > 0 && month.CostUSD >= limits.MonthlyCostUSD)
}

func addUsage(counter *QuotaCounter, usage Usage) {
	counter.PromptTokens += usage.PromptTokens
	counter.CompletionTokens += usage.CompletionTokens
	counter.TotalTokens += usage.TotalTokens
	counter.CostUSD += usage.CostUSD
}

func aggregateUsage(records []UsageRecord, keyFn func(UsageRecord) string) []map[string]any {
	type bucket struct {
		Key               string
		Requests          int64
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		TotalTokens       int64
		CostUSD           float64
	}
	buckets := map[string]*bucket{}
	for _, record := range records {
		key := keyFn(record)
		if key == "" {
			key = "unknown"
		}
		item, ok := buckets[key]
		if !ok {
			item = &bucket{Key: key}
			buckets[key] = item
		}
		item.Requests++
		item.InputTokens += record.InputTokens
		item.CachedInputTokens += record.CachedInputTokens
		item.OutputTokens += record.OutputTokens
		item.TotalTokens += record.TotalTokens
		item.CostUSD += record.CostUSD
	}
	items := make([]bucket, 0, len(buckets))
	for _, item := range buckets {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CostUSD == items[j].CostUSD {
			return items[i].TotalTokens > items[j].TotalTokens
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
		})
	}
	return result
}

func dayBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func monthBucket(t time.Time) string {
	return t.UTC().Format("2006-01")
}

func minuteBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04")
}

func resourcePrefix(kind string) string {
	switch kind {
	case "teams":
		return "team"
	case "role-configs":
		return "role"
	case "project-members":
		return "pm"
	case "identity-providers":
		return "idp"
	case "users":
		return "usr"
	case "monitors":
		return "mon"
	case "proxies":
		return "prx"
	case "announcements":
		return "ann"
	case "settings":
		return "cfg"
	case "security-policies":
		return "sec"
	case "alert-rules":
		return "alr"
	case "quota-policies":
		return "quo"
	case "notification-channels":
		return "ntf"
	case "cost-centers":
		return "cc"
	case "budgets":
		return "bdg"
	case "chargebacks":
		return "cbk"
	case "invoices":
		return "inv"
	case "reports":
		return "rpt"
	case "approval-flows":
		return "apf"
	default:
		return "res"
	}
}

// GetDatabaseStatus returns the database type, Docker environment, and connection status.
func (s *GormStore) GetDatabaseStatus() (map[string]interface{}, error) {
	status := make(map[string]interface{})

	// 1. Detect the database type.
	dbType := "sqlite"
	if s.db.Dialector.Name() == "postgres" {
		dbType = "postgres"
	}
	status["database_type"] = dbType

	// 2. Detect whether running in Docker.
	isDocker := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		isDocker = true
	}
	status["is_docker"] = isDocker

	// 3. Test the database connection.
	sqlDB, err := s.db.DB()
	if err != nil {
		status["connection_ok"] = false
		return status, nil
	}

	if err := sqlDB.Ping(); err != nil {
		status["connection_ok"] = false
		return status, nil
	}
	status["connection_ok"] = true

	// 4. If PostgreSQL, retrieve the version information.
	if dbType == "postgres" {
		var version string
		if err := s.db.Raw("SELECT version()").Scan(&version).Error; err == nil {
			status["postgres_version"] = version
		}
	}

	// 5. Get the redacted database URL.
	if databaseURL := os.Getenv("TOKENHUB_DATABASE_URL"); databaseURL != "" {
		status["database_url"] = redactDatabaseURL(databaseURL)
	}

	return status, nil
}

func (s *GormStore) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// SetGatewayMetrics attaches the metrics collectors. It is called once during server
// construction, before the store serves traffic.
func (s *GormStore) SetGatewayMetrics(metrics *GatewayMetrics) {
	s.metrics = metrics
}
