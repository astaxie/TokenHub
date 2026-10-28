// Package analytics owns token-cost query semantics and the application
// contracts used by analytics adapters. HTTP and database representations
// remain outside this package.
package analytics

import (
	"context"
	"time"
)

const (
	ScopeOrganization = "organization"
	ScopeProject      = "project"

	SchemaVersion = "1.0"
	StatusSuccess = "success"
	StatusError   = "error"

	IncrementalSnapshot = "snapshot"
	IncrementalChanges  = "changes"

	CursorVersion = 3
)

// Credential is the domain projection of a read-only analytics credential.
// Secret hashes and persistence tags belong to the adapter layer.
type Credential struct {
	ID         string
	Name       string
	KeyPrefix  string
	KeySuffix  string
	ScopeType  string
	ProjectID  string
	Status     string
	CreatedBy  string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Metrics struct {
	RequestCount      int64
	ErrorCount        int64
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	OutputTokens      int64
	ReasoningTokens   int64
	TotalTokens       int64
	EstimatedCostUSD  float64
}

type Row struct {
	DedupeKey  string
	Bucket     string
	RequestID  string
	OccurredAt string
	ProjectID  string
	UserID     string
	APIKeyID   string
	ProviderID string
	Model      string
	Status     string
	StatusCode int
	Metrics    Metrics
}

// Page keeps rows and the checkpoint from one consistent persistence
// snapshot. The adapter owns how that snapshot is established.
type Page struct {
	Rows       []Row
	HasMore    bool
	Checkpoint int64
}

type Query struct {
	From               time.Time
	To                 time.Time
	ProjectID          string
	UserID             string
	APIKeyID           string
	ProviderID         string
	Model              string
	Status             string
	Granularity        string
	GroupBy            []string
	Limit              int
	AfterAt            time.Time
	AfterID            string
	Offset             int
	AfterSequence      int64
	ThroughSequence    int64
	ThroughSequenceSet bool
	Incremental        bool
}

// CredentialStore is the persistence port for credential lifecycle use cases.
type CredentialStore interface {
	CreateCredential(context.Context, Credential, string) (Credential, string, error)
	ListCredentials(context.Context) ([]Credential, error)
	RevokeCredential(context.Context, string) (Credential, error)
	ValidateCredential(context.Context, string) (Credential, error)
}

// TokenCostStore is the persistence port for a consistent token-cost page.
type TokenCostStore interface {
	QueryTokenCostPage(context.Context, Query) (Page, error)
}
