package server

import (
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const (
	agentSourceAdmin  = "admin"
	agentSourceConfig = "config"

	agentBindingAllow = "allow"
	agentBindingDeny  = "deny"
)

// Agent is the reviewed gateway representation of an upstream A2A Agent Card.
// CardJSON is the public, credential-free card served by TokenHub; upstream
// endpoints and credentials live on AgentInstance instead.
type Agent struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Slug        string    `json:"slug" gorm:"uniqueIndex"`
	Name        string    `json:"name"`
	Description string    `json:"description" gorm:"type:text"`
	Version     string    `json:"version"`
	Status      string    `json:"status" gorm:"index"`
	Source      string    `json:"source" gorm:"index"`
	SourceHash  string    `json:"source_hash,omitempty"`
	CardJSON    string    `json:"-" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AgentRevision struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	AgentID      string    `json:"agent_id" gorm:"index"`
	Revision     int64     `json:"revision"`
	Source       string    `json:"source"`
	CardJSON     string    `json:"-" gorm:"type:text"`
	InstanceJSON string    `json:"-" gorm:"type:text"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type AgentInstance struct {
	ID                    string            `json:"id" gorm:"primaryKey"`
	AgentID               string            `json:"agent_id" gorm:"index"`
	Name                  string            `json:"name"`
	URL                   string            `json:"url"`
	ProtocolBinding       string            `json:"protocol_binding"`
	ProtocolVersion       string            `json:"protocol_version"`
	Status                string            `json:"status" gorm:"index"`
	Healthy               bool              `json:"healthy" gorm:"index"`
	Priority              int               `json:"priority"`
	Weight                int               `json:"weight"`
	MaxConcurrency        int64             `json:"max_concurrency"`
	ActiveRequests        int64             `json:"active_requests" gorm:"not null;default:0"`
	FixedCostUSD          float64           `json:"fixed_cost_usd"`
	AllowedForwardHeaders []string          `json:"allowed_forward_headers,omitempty" gorm:"serializer:json"`
	HeadersCiphertext     string            `json:"-" gorm:"type:text"`
	FailureCount          int               `json:"failure_count"`
	CooldownUntil         *time.Time        `json:"cooldown_until,omitempty" gorm:"index"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	Headers               map[string]string `json:"headers,omitempty" gorm:"-"`
}

// AgentInstanceLease makes instance concurrency reservations recoverable when
// a process exits before it can release an in-flight request. Expired leases
// are reconciled before the next reservation.
type AgentInstanceLease struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	InstanceID string    `json:"instance_id" gorm:"index"`
	ExpiresAt  time.Time `json:"expires_at" gorm:"index"`
	CreatedAt  time.Time `json:"created_at"`
}

type AgentSkill struct {
	AgentID     string   `json:"agent_id" gorm:"primaryKey"`
	SkillID     string   `json:"skill_id" gorm:"primaryKey"`
	Name        string   `json:"name"`
	Description string   `json:"description" gorm:"type:text"`
	InputModes  []string `json:"input_modes,omitempty" gorm:"serializer:json"`
	OutputModes []string `json:"output_modes,omitempty" gorm:"serializer:json"`
	Examples    []string `json:"examples,omitempty" gorm:"serializer:json"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AgentAccessBinding is default-deny. A request is allowed only if an active
// allow binding matches one of its scopes and no matching deny binding exists.
type AgentAccessBinding struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	AgentID   string    `json:"agent_id" gorm:"index"`
	ScopeType string    `json:"scope_type" gorm:"index"`
	ScopeID   string    `json:"scope_id" gorm:"index"`
	Effect    string    `json:"effect" gorm:"index"`
	Skills    []string  `json:"skills,omitempty" gorm:"serializer:json"`
	Status    string    `json:"status" gorm:"index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentAccessGroup struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentAccessGroupMember struct {
	GroupID     string    `json:"group_id" gorm:"primaryKey"`
	SubjectType string    `json:"subject_type" gorm:"primaryKey"`
	SubjectID   string    `json:"subject_id" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"created_at"`
}

type AgentTask struct {
	ID             string     `json:"id" gorm:"primaryKey"`
	UpstreamTaskID string     `json:"-" gorm:"index"`
	AgentID        string     `json:"agent_id" gorm:"index"`
	InstanceID     string     `json:"instance_id" gorm:"index"`
	ProjectID      string     `json:"project_id" gorm:"index"`
	APIKeyID       string     `json:"api_key_id" gorm:"index"`
	EndUserID      string     `json:"end_user_id,omitempty" gorm:"index"`
	ExecutionID    string     `json:"execution_id" gorm:"index"`
	ContextID      string     `json:"context_id" gorm:"index"`
	State          string     `json:"state" gorm:"index"`
	SnapshotJSON   string     `json:"-" gorm:"type:text"`
	LastEventSeq   int64      `json:"last_event_sequence"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty" gorm:"index"`
}

type AgentTaskEvent struct {
	TaskID      string    `json:"task_id" gorm:"primaryKey"`
	Sequence    int64     `json:"sequence" gorm:"primaryKey"`
	EventType   string    `json:"event_type"`
	PayloadJSON string    `json:"-" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at"`
}

type AgentExecution struct {
	ID             string     `json:"id" gorm:"primaryKey"`
	RootAgentID    string     `json:"root_agent_id" gorm:"index"`
	ProjectID      string     `json:"project_id" gorm:"index"`
	APIKeyID       string     `json:"api_key_id" gorm:"index"`
	EndUserID      string     `json:"end_user_id,omitempty" gorm:"index"`
	TraceID        string     `json:"trace_id" gorm:"index"`
	Status         string     `json:"status" gorm:"index"`
	MaxAgentHops   int64      `json:"max_agent_hops"`
	MaxModelCalls  int64      `json:"max_model_calls"`
	MaxMCPCalls    int64      `json:"max_mcp_calls"`
	MaxTokens      int64      `json:"max_tokens"`
	MaxCostUSD     float64    `json:"max_cost_usd"`
	MaxConcurrency int64      `json:"max_concurrency"`
	AgentHops      int64      `json:"agent_hops"`
	ModelCalls     int64      `json:"model_calls"`
	MCPCalls       int64      `json:"mcp_calls"`
	Tokens         int64      `json:"tokens"`
	CostUSD        float64    `json:"cost_usd"`
	Deadline       *time.Time `json:"deadline,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type AgentExecutionEdge struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	ExecutionID  string    `json:"execution_id" gorm:"index"`
	ParentStepID string    `json:"parent_step_id,omitempty" gorm:"index"`
	CallerType   string    `json:"caller_type"`
	CallerID     string    `json:"caller_id,omitempty"`
	CalleeType   string    `json:"callee_type"`
	CalleeID     string    `json:"callee_id"`
	Depth        int64     `json:"depth"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type AgentUsageRecord struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	ExecutionID string    `json:"execution_id" gorm:"index"`
	StepID      string    `json:"step_id" gorm:"uniqueIndex"`
	TaskID      string    `json:"task_id,omitempty" gorm:"index"`
	AgentID     string    `json:"agent_id" gorm:"index"`
	ProjectID   string    `json:"project_id" gorm:"index"`
	APIKeyID    string    `json:"api_key_id" gorm:"index"`
	SourceType  string    `json:"source_type"`
	Tokens      int64     `json:"tokens"`
	CostUSD     float64   `json:"cost_usd"`
	CreatedAt   time.Time `json:"created_at"`
}

type AgentExecutionDetails struct {
	AgentExecution
	Edges []AgentExecutionEdge `json:"edges"`
	Usage []AgentUsageRecord   `json:"usage"`
	Tasks []AgentTask          `json:"tasks"`
}

type AgentWithDetails struct {
	Agent
	Card      *a2a.AgentCard  `json:"card"`
	Instances []AgentInstance `json:"instances"`
	Skills    []AgentSkill    `json:"skills"`
}

type AgentRegistration struct {
	Slug                  string            `json:"slug" yaml:"slug"`
	Status                string            `json:"status,omitempty" yaml:"status,omitempty"`
	CardURL               string            `json:"card_url,omitempty" yaml:"card_url,omitempty"`
	Card                  *a2a.AgentCard    `json:"card,omitempty" yaml:"card,omitempty"`
	UpstreamURL           string            `json:"upstream_url,omitempty" yaml:"upstream_url,omitempty"`
	Headers               map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	AllowedForwardHeaders []string          `json:"allowed_forward_headers,omitempty" yaml:"allowed_forward_headers,omitempty"`
	Priority              int               `json:"priority,omitempty" yaml:"priority,omitempty"`
	Weight                int               `json:"weight,omitempty" yaml:"weight,omitempty"`
	FixedCostUSD          float64           `json:"fixed_cost_usd,omitempty" yaml:"fixed_cost_usd,omitempty"`
	MaxConcurrency        int64             `json:"max_concurrency,omitempty" yaml:"max_concurrency,omitempty"`
}
