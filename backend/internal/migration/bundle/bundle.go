package bundle

import (
	"encoding/json"
	"fmt"
	"time"

	"tokenhub/backend/internal/server"
)

// CanonicalMigrationBundle is the versioned, source-agnostic payload
// that source adapters produce and TokenHub sinks consume.
//
// The bundle is a plain JSON document and MUST NOT carry plaintext
// secrets. Every credential is expressed as a SecretRef (see
// secretref.go) and resolved at apply time by a SecretResolver.
type CanonicalMigrationBundle struct {
	SchemaVersion string    `json:"schema_version"`
	Source        Source    `json:"source"`
	GeneratedAt   time.Time `json:"generated_at"`

	Providers         []ProviderRef         `json:"providers,omitempty"`
	ProviderResources []ProviderResourceRef `json:"provider_resources,omitempty"`
	Models            []ModelRef            `json:"models,omitempty"`
	Routes            []RouteRef            `json:"routes,omitempty"`
	Teams             []TeamRef             `json:"teams,omitempty"`
	Projects          []ProjectRef          `json:"projects,omitempty"`
	Users             []UserRef             `json:"users,omitempty"`
	APIKeys           []APIKeyRef           `json:"api_keys,omitempty"`
	QuotaPolicies     []QuotaPolicyRef      `json:"quota_policies,omitempty"`

	// Warnings collected while extracting the bundle. Non-fatal.
	Warnings []Warning `json:"warnings,omitempty"`
}

// Source describes the origin of the bundle.
type Source struct {
	Adapter        string            `json:"adapter"`
	AdapterVersion string            `json:"adapter_version"`
	ToolVersion    string            `json:"tool_version,omitempty"`
	OriginURL      string            `json:"origin_url,omitempty"`
	OriginNote     string            `json:"origin_note,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// Warning severity levels.
const (
	SeverityInfo    = "info"
	SeverityWarn    = "warn"
	SeverityBlocker = "blocker"
)

// Warning is a non-fatal issue encountered while producing the bundle.
type Warning struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
}

// ExternalRef records the identifier used by the source system so
// operators can trace bundle entries back to their origin.
type ExternalRef struct {
	System string `json:"system"`
	ID     string `json:"id"`
}

// ProviderRef wraps a TokenHub Provider with migration metadata.
type ProviderRef struct {
	ExternalRef   ExternalRef          `json:"external_ref"`
	Spec          server.Provider      `json:"spec"`
	APIKeySecret  *SecretRef           `json:"api_key_secret,omitempty"`
	HeaderSecrets map[string]SecretRef `json:"header_secrets,omitempty"`
}

// ProviderResourceRef wraps a TokenHub ProviderResource with migration
// metadata. ProviderRef is the local ExternalRef.ID of the parent
// ProviderRef inside the same bundle.
type ProviderResourceRef struct {
	ExternalRef   ExternalRef             `json:"external_ref"`
	ProviderRef   string                  `json:"provider_ref"`
	Spec          server.ProviderResource `json:"spec"`
	APIKeySecret  *SecretRef              `json:"api_key_secret,omitempty"`
	HeaderSecrets map[string]SecretRef    `json:"header_secrets,omitempty"`
}

// ModelRef wraps a TokenHub Model with migration metadata.
type ModelRef struct {
	ExternalRef ExternalRef  `json:"external_ref"`
	Spec        server.Model `json:"spec"`
}

// RouteRef wraps a TokenHub ModelRoute with migration metadata.
// ModelRef, ProviderRef and ProviderResourceRef are local
// ExternalRef.ID values of the referenced resources inside the
// same bundle.
type RouteRef struct {
	ExternalRef         ExternalRef       `json:"external_ref"`
	ModelRef            string            `json:"model_ref,omitempty"`
	ProviderRef         string            `json:"provider_ref,omitempty"`
	ProviderResourceRef string            `json:"provider_resource_ref,omitempty"`
	Spec                server.ModelRoute `json:"spec"`
}

// TeamRef is a lightweight team representation. TokenHub does not
// model teams as a first-class resource; the sink materialises teams
// via the users and projects that reference them.
type TeamRef struct {
	ExternalRef ExternalRef `json:"external_ref"`
	ID          string      `json:"id"`
	Name        string      `json:"name"`
}

// ProjectRef wraps a TokenHub Project with migration metadata.
type ProjectRef struct {
	ExternalRef ExternalRef    `json:"external_ref"`
	TeamRef     string         `json:"team_ref,omitempty"`
	Spec        server.Project `json:"spec"`
}

// UserRef wraps a TokenHub AdminUser with migration metadata. Password
// material is intentionally out of scope: the sink issues a password
// reset for each imported admin user.
type UserRef struct {
	ExternalRef ExternalRef      `json:"external_ref"`
	TeamRef     string           `json:"team_ref,omitempty"`
	Spec        server.AdminUser `json:"spec"`
}

// APIKeyRef wraps a TokenHub APIKey with migration metadata. The raw
// key material is never stored in the bundle; it is regenerated by
// the sink or resolved from KeySecret when the source system can
// export the plaintext key.
type APIKeyRef struct {
	ExternalRef ExternalRef   `json:"external_ref"`
	ProjectRef  string        `json:"project_ref,omitempty"`
	Spec        server.APIKey `json:"spec"`
	KeySecret   *SecretRef    `json:"key_secret,omitempty"`
}

// QuotaPolicyRef records a named quota template that can be reused by
// multiple API keys inside the bundle.
type QuotaPolicyRef struct {
	ExternalRef ExternalRef        `json:"external_ref"`
	Name        string             `json:"name"`
	Limits      server.QuotaLimits `json:"limits"`
}

// Marshal serialises the bundle to indented JSON.
func Marshal(b *CanonicalMigrationBundle) ([]byte, error) {
	if b == nil {
		return nil, fmt.Errorf("bundle: cannot marshal nil bundle")
	}
	return json.MarshalIndent(b, "", "  ")
}

// Unmarshal parses a JSON payload into a CanonicalMigrationBundle and
// enforces schema-version compatibility. It does not run full JSON
// Schema validation; call Validate for that.
func Unmarshal(data []byte) (*CanonicalMigrationBundle, error) {
	var out CanonicalMigrationBundle
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("bundle: unmarshal: %w", err)
	}
	if err := RejectIfIncompatible(out.SchemaVersion); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnmarshalCheckpoint parses a checkpoint JSON payload into dst.
func UnmarshalCheckpoint(data []byte, dst any) error {
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("bundle: unmarshal checkpoint: %w", err)
	}
	return nil
}
