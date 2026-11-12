package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrMissingName        = errors.New("analytics credential name is required")
	ErrInvalidScope       = errors.New("invalid analytics scope")
	ErrMissingProject     = errors.New("project analytics scope requires a project")
	ErrInvalidExpiry      = errors.New("analytics credential expiry is invalid")
	ErrInvalidTimeRange   = errors.New("analytics query time range is invalid")
	ErrInvalidGranularity = errors.New("analytics query granularity is invalid")
	ErrInvalidLimit       = errors.New("analytics query limit is invalid")
	ErrInvalidGrouping    = errors.New("request granularity cannot be combined with group_by")
	ErrInvalidSequence    = errors.New("analytics query sequence range is invalid")
)

// ValidateCredential checks the credential invariants that are independent of
// the persistence adapter. The adapter remains responsible for IDs, hashes,
// status transitions, and uniqueness conflicts.
func ValidateCredential(credential Credential, now time.Time) error {
	if credential.Name == "" {
		return ErrMissingName
	}
	if err := ValidateCredentialScope(credential.ScopeType, credential.ProjectID); err != nil {
		return err
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(now) {
		return ErrInvalidExpiry
	}
	return nil
}

// ValidateCredentialScope validates the scope invariant independently of any
// transport or persistence error type.
func ValidateCredentialScope(scope, projectID string) error {
	switch scope {
	case ScopeOrganization:
		return nil
	case ScopeProject:
		if projectID == "" {
			return ErrMissingProject
		}
		return nil
	default:
		return ErrInvalidScope
	}
}

// ValidateQuery checks invariants shared by HTTP and non-HTTP consumers.
func ValidateQuery(query Query) error {
	if query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) {
		return ErrInvalidTimeRange
	}
	switch query.Granularity {
	case "request", "none", "hour", "day", "month":
	default:
		return ErrInvalidGranularity
	}
	if query.Limit <= 0 {
		return ErrInvalidLimit
	}
	if query.Granularity == "request" && len(query.GroupBy) > 0 {
		return ErrInvalidGrouping
	}
	if query.AfterSequence < 0 || query.ThroughSequence < 0 ||
		(query.ThroughSequenceSet && query.AfterSequence > query.ThroughSequence) {
		return ErrInvalidSequence
	}
	return nil
}

// DedupeKey returns the stable identity used by snapshot and incremental
// exports. It intentionally includes query dimensions so a row cannot be
// reused across incompatible aggregations.
func DedupeKey(row Row, query Query) string {
	if query.Granularity == "request" {
		return row.RequestID
	}
	data, _ := json.Marshal(struct {
		Granularity string
		GroupBy     []string
		Filters     []string
		Row         []string
	}{
		Granularity: query.Granularity,
		GroupBy:     query.GroupBy,
		Filters: []string{
			query.ProjectID, query.UserID, query.APIKeyID,
			query.ProviderID, query.Model, query.Status,
		},
		Row: []string{
			row.Bucket, row.ProjectID, row.UserID, row.APIKeyID,
			row.ProviderID, row.Model, row.Status,
		},
	})
	if query.Incremental {
		data, _ = json.Marshal(struct {
			Base  json.RawMessage
			After int64
		}{Base: data, After: query.AfterSequence})
	}
	sum := sha256.Sum256(data)
	return "aggregate_" + hex.EncodeToString(sum[:])
}

// QueryKey binds a cursor to its credential and query dimensions.
func QueryKey(credentialID string, query Query) string {
	data, _ := json.Marshal(struct {
		CredentialID string
		ProjectID    string
		UserID       string
		APIKeyID     string
		ProviderID   string
		Model        string
		Status       string
		Granularity  string
		GroupBy      []string
	}{
		CredentialID: credentialID,
		ProjectID:    query.ProjectID,
		UserID:       query.UserID,
		APIKeyID:     query.APIKeyID,
		ProviderID:   query.ProviderID,
		Model:        query.Model,
		Status:       query.Status,
		Granularity:  query.Granularity,
		GroupBy:      query.GroupBy,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
