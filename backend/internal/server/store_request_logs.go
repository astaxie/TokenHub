package server

import (
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

type RequestLogQuery struct {
	Page       int
	PageSize   int
	Status     string
	Keyword    string
	ModelName  string
	Since      time.Time
	Until      time.Time
	Global     bool
	ProjectIDs []string
	APIKeyIDs  []string
}

type RequestLogPagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

type RequestLogSummary struct {
	All              int64 `json:"all"`
	OK               int64 `json:"ok"`
	Error            int64 `json:"error"`
	AverageLatencyMS int64 `json:"average_latency_ms"`
}

type RequestLogPage struct {
	Data       []RequestLog         `json:"data"`
	Pagination RequestLogPagination `json:"pagination"`
	Summary    RequestLogSummary    `json:"summary"`
}

func (s *GormStore) QueryRequestLogs(query RequestLogQuery) (RequestLogPage, error) {
	summaryQuery := requestLogBaseQuery(s.db, query)
	var summaryRow struct {
		AllCount       int64
		OKCount        int64
		ErrorCount     int64
		AverageLatency float64
	}
	if err := summaryQuery.Select(
		"COUNT(*) AS all_count, " +
			"COALESCE(SUM(CASE WHEN status_code < 400 THEN 1 ELSE 0 END), 0) AS ok_count, " +
			"COALESCE(SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count, " +
			"COALESCE(AVG(latency_ms), 0) AS average_latency",
	).Scan(&summaryRow).Error; err != nil {
		return RequestLogPage{}, err
	}

	total := summaryRow.AllCount
	if query.Status != "" && query.Status != "all" {
		countQuery := applyRequestLogStatus(requestLogBaseQuery(s.db, query), query.Status)
		if err := countQuery.Count(&total).Error; err != nil {
			return RequestLogPage{}, err
		}
	}
	logs := make([]RequestLog, 0, query.PageSize)
	offset := (query.Page - 1) * query.PageSize
	pageQuery := applyRequestLogStatus(requestLogBaseQuery(s.db, query), query.Status)
	if err := pageQuery.Order("created_at DESC, id DESC").Limit(query.PageSize).Offset(offset).Find(&logs).Error; err != nil {
		return RequestLogPage{}, err
	}
	if err := s.enrichRequestLogPageWithUsage(logs); err != nil {
		return RequestLogPage{}, err
	}

	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(query.PageSize) - 1) / int64(query.PageSize))
	}
	return RequestLogPage{
		Data: logs,
		Pagination: RequestLogPagination{
			Page:       query.Page,
			PageSize:   query.PageSize,
			Total:      total,
			TotalPages: totalPages,
		},
		Summary: RequestLogSummary{
			All:              summaryRow.AllCount,
			OK:               summaryRow.OKCount,
			Error:            summaryRow.ErrorCount,
			AverageLatencyMS: int64(math.Round(summaryRow.AverageLatency)),
		},
	}, nil
}

func requestLogBaseQuery(db *gorm.DB, query RequestLogQuery) *gorm.DB {
	return applyRequestLogFilters(applyRequestLogKeyword(applyRequestLogScope(db.Model(&RequestLog{}), query), query.Keyword), query)
}

func applyRequestLogFilters(db *gorm.DB, query RequestLogQuery) *gorm.DB {
	if query.ModelName != "" {
		db = db.Where("model_name = ?", query.ModelName)
	}
	if !query.Since.IsZero() {
		db = db.Where("created_at >= ?", query.Since)
	}
	if !query.Until.IsZero() {
		db = db.Where("created_at <= ?", query.Until)
	}
	return db
}

func applyRequestLogKeyword(db *gorm.DB, keyword string) *gorm.DB {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return db
	}
	pattern := "%" + escapeLikePattern(strings.ToLower(keyword)) + "%"
	columns := []string{
		"request_id",
		"project_id",
		"api_key_id",
		"model_name",
		"provider_id",
		"provider_resource_id",
		"provider_model",
		"error_code",
	}
	conditions := make([]string, 0, len(columns)+3)
	args := make([]any, 0, len(columns)+3)
	for _, column := range columns {
		conditions = append(conditions, "LOWER(COALESCE("+column+", '')) LIKE ? ESCAPE '!'")
		args = append(args, pattern)
	}
	conditions = append(conditions,
		"LOWER(CAST(status_code AS TEXT)) LIKE ? ESCAPE '!'",
		"EXISTS (SELECT 1 FROM projects WHERE projects.id = request_logs.project_id AND LOWER(COALESCE(projects.name, '')) LIKE ? ESCAPE '!')",
		"EXISTS (SELECT 1 FROM providers WHERE providers.id = request_logs.provider_id AND LOWER(COALESCE(providers.name, '')) LIKE ? ESCAPE '!')",
	)
	args = append(args, pattern, pattern, pattern)
	return db.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func escapeLikePattern(value string) string {
	return strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(value)
}

func applyRequestLogStatus(db *gorm.DB, status string) *gorm.DB {
	switch status {
	case "ok":
		return db.Where("status_code < 400")
	case "error":
		return db.Where("status_code >= 400")
	default:
		return db
	}
}

func applyRequestLogScope(db *gorm.DB, query RequestLogQuery) *gorm.DB {
	if query.Global {
		return db
	}
	switch {
	case len(query.ProjectIDs) > 0 && len(query.APIKeyIDs) > 0:
		return db.Where("(project_id IN ? OR api_key_id IN ?)", query.ProjectIDs, query.APIKeyIDs)
	case len(query.ProjectIDs) > 0:
		return db.Where("project_id IN ?", query.ProjectIDs)
	case len(query.APIKeyIDs) > 0:
		return db.Where("api_key_id IN ?", query.APIKeyIDs)
	default:
		return db.Where("1 = 0")
	}
}

func (s *GormStore) enrichRequestLogPageWithUsage(logs []RequestLog) error {
	requestIDs := make([]string, 0, len(logs))
	logsByScope := make(map[requestLogUsageScope]*RequestLog, len(logs))
	for index := range logs {
		log := &logs[index]
		if log.RequestID != "" {
			requestIDs = append(requestIDs, log.RequestID)
			logsByScope[requestLogUsageScope{
				RequestID: log.RequestID,
				ProjectID: log.ProjectID,
				APIKeyID:  log.APIKeyID,
			}] = log
		}
	}
	if len(requestIDs) == 0 {
		return nil
	}
	var records []UsageRecord
	if err := s.db.Where("request_id IN ?", requestIDs).Find(&records).Error; err != nil {
		return err
	}
	for _, record := range records {
		log := logsByScope[requestLogUsageScope{
			RequestID: record.RequestID,
			ProjectID: record.ProjectID,
			APIKeyID:  record.APIKeyID,
		}]
		if log != nil {
			enrichRequestLogWithUsageRecord(log, record)
		}
	}
	return nil
}

type requestLogUsageScope struct {
	RequestID string
	ProjectID string
	APIKeyID  string
}
