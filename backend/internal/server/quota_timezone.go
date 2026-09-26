package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA zones available in minimal deployment images.

	"gorm.io/gorm"
)

const quotaTimezoneField = "quota_timezone"

type quotaPeriods struct {
	Day   string `json:"day"`
	Month string `json:"month"`
}

func quotaLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "UTC"
	}
	// Local depends on the host and cannot define a shared cluster boundary.
	if name != "Local" {
		if location, err := time.LoadLocation(name); err == nil {
			return location, nil
		}
	}
	return nil, NewHTTPError(http.StatusBadRequest, "invalid_quota_timezone", "quota_timezone must be a valid IANA timezone, such as UTC or Asia/Shanghai")
}

func currentQuotaPeriods(tx *gorm.DB, at time.Time) (quotaPeriods, error) {
	var setting AdminResource
	err := tx.Where("kind = ? AND id = ?", "settings", gatewaySettingsID).Take(&setting).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return quotaPeriods{}, err
	}
	location, err := quotaLocation(stringField(setting.Fields, quotaTimezoneField))
	if err != nil {
		return quotaPeriods{}, err
	}
	local := at.In(location)
	return quotaPeriods{Day: local.Format("2006-01-02"), Month: local.Format("2006-01")}, nil
}

// Admission evidence pins settlement and durable job recovery to the original
// buckets even if an administrator changes the timezone while a call is running.
// Older admissions have no snapshot and always used UTC.
func admittedQuotaPeriods(tx *gorm.DB, requestID string, at time.Time) (quotaPeriods, error) {
	fallback := quotaPeriods{Day: dayBucket(at), Month: monthBucket(at)}
	if requestID == "" {
		return fallback, nil
	}
	var entry meteringEntry
	if err := tx.Select("payload").Take(&entry, "id = ?", requestID+":admission").Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fallback, nil
		}
		return quotaPeriods{}, err
	}
	var snapshot meteringRequestSnapshot
	if err := json.Unmarshal([]byte(entry.Payload), &snapshot); err != nil {
		return quotaPeriods{}, err
	}
	if snapshot.QuotaPeriods == nil {
		return fallback, nil
	}
	return *snapshot.QuotaPeriods, nil
}
