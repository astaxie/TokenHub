package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrPluginBackgroundJobNotFound       = errors.New("plugin background job is not registered")
	ErrPluginBackgroundJobUnavailable    = errors.New("plugin background job handler is unavailable")
	ErrPluginBackgroundJobInvalidPayload = errors.New("plugin background job payload is invalid")
	ErrPluginBackgroundJobInvalidResult  = errors.New("plugin background job result is invalid")
)

type BackgroundJobRetryPolicy struct {
	MaxAttempts   int `json:"max_attempts,omitempty" yaml:"max_attempts"`
	BackoffMillis int `json:"backoff_millis,omitempty" yaml:"backoff_millis"`
}

type BackgroundJobDescriptor struct {
	PluginID       string                   `json:"plugin_id"`
	JobID          string                   `json:"job_id"`
	Title          string                   `json:"title,omitempty"`
	Capability     string                   `json:"capability,omitempty"`
	Subject        string                   `json:"subject,omitempty"`
	Schedule       string                   `json:"schedule"`
	TimeoutMillis  int                      `json:"timeout_millis,omitempty"`
	MaxConcurrency int                      `json:"max_concurrency"`
	Retry          BackgroundJobRetryPolicy `json:"retry,omitempty"`
	InputSchema    map[string]any           `json:"input_schema,omitempty"`
	OutputSchema   map[string]any           `json:"output_schema,omitempty"`
}

type BackgroundJobInvocation struct {
	PluginID string          `json:"plugin_id"`
	JobID    string          `json:"job_id"`
	Trigger  string          `json:"trigger,omitempty"`
	Actor    ActionActor     `json:"actor,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type BackgroundJobResult struct {
	Data     any               `json:"data,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type BackgroundJobHandler interface {
	ExecuteBackgroundJob(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error)
}

type BackgroundJobHandlerFunc func(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error)

func (f BackgroundJobHandlerFunc) ExecuteBackgroundJob(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
	return f(ctx, invocation)
}

type unavailableBackgroundJobHandler struct{}

func (unavailableBackgroundJobHandler) ExecuteBackgroundJob(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
	return BackgroundJobResult{}, ErrPluginBackgroundJobUnavailable
}

type BackgroundJobBroker struct {
	jobs map[string]backgroundJobEntry
}

type backgroundJobEntry struct {
	descriptor BackgroundJobDescriptor
	handler    BackgroundJobHandler
}

func NewBackgroundJobBroker() *BackgroundJobBroker {
	return &BackgroundJobBroker{jobs: map[string]backgroundJobEntry{}}
}

func (b *BackgroundJobBroker) RegisterDescriptor(descriptor BackgroundJobDescriptor) error {
	return b.register(descriptor, unavailableBackgroundJobHandler{}, false)
}

func (b *BackgroundJobBroker) Register(descriptor BackgroundJobDescriptor, handler BackgroundJobHandler) error {
	return b.register(descriptor, handler, true)
}

func (b *BackgroundJobBroker) register(descriptor BackgroundJobDescriptor, handler BackgroundJobHandler, allowDescriptorBinding bool) error {
	if b == nil {
		return fmt.Errorf("plugin background job broker is not configured")
	}
	descriptor = NormalizeBackgroundJobDescriptor(descriptor)
	if descriptor.PluginID == "" {
		return fmt.Errorf("plugin background job plugin id is required")
	}
	if descriptor.JobID == "" {
		return fmt.Errorf("plugin background job id is required")
	}
	if descriptor.Schedule == "" {
		return fmt.Errorf("plugin background job schedule is required")
	}
	if descriptor.TimeoutMillis < 0 {
		return fmt.Errorf("plugin background job timeout_millis cannot be negative")
	}
	if descriptor.MaxConcurrency <= 0 {
		return fmt.Errorf("plugin background job max_concurrency must be positive")
	}
	if descriptor.Retry.MaxAttempts < 0 {
		return fmt.Errorf("plugin background job retry max_attempts cannot be negative")
	}
	if descriptor.Retry.BackoffMillis < 0 {
		return fmt.Errorf("plugin background job retry backoff_millis cannot be negative")
	}
	if handler == nil {
		return fmt.Errorf("plugin background job handler is required")
	}
	key := pluginBackgroundJobKey(descriptor.PluginID, descriptor.JobID)
	if existing, ok := b.jobs[key]; ok && !(allowDescriptorBinding && isUnavailableBackgroundJobHandler(existing.handler)) {
		return fmt.Errorf("plugin background job %s from plugin %s is already registered", descriptor.JobID, descriptor.PluginID)
	}
	b.jobs[key] = backgroundJobEntry{descriptor: descriptor, handler: handler}
	return nil
}

func (b *BackgroundJobBroker) Execute(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
	if b == nil {
		return BackgroundJobResult{}, fmt.Errorf("plugin background job broker is not configured")
	}
	invocation.PluginID = strings.TrimSpace(invocation.PluginID)
	invocation.JobID = strings.TrimSpace(invocation.JobID)
	entry, ok := b.jobs[pluginBackgroundJobKey(invocation.PluginID, invocation.JobID)]
	if !ok {
		return BackgroundJobResult{}, ErrPluginBackgroundJobNotFound
	}
	if err := validateBackgroundJobPayload(entry.descriptor.InputSchema, invocation.Payload); err != nil {
		return BackgroundJobResult{}, err
	}
	result, err := entry.handler.ExecuteBackgroundJob(ctx, invocation)
	if err != nil {
		return BackgroundJobResult{}, err
	}
	if err := validateBackgroundJobResult(entry.descriptor.OutputSchema, result.Data); err != nil {
		return BackgroundJobResult{}, err
	}
	return result, nil
}

func (b *BackgroundJobBroker) Describe(pluginID string, jobID string) (BackgroundJobDescriptor, bool) {
	if b == nil {
		return BackgroundJobDescriptor{}, false
	}
	entry, ok := b.jobs[pluginBackgroundJobKey(pluginID, jobID)]
	return entry.descriptor, ok
}

func (b *BackgroundJobBroker) List() []BackgroundJobDescriptor {
	if b == nil {
		return nil
	}
	items := make([]BackgroundJobDescriptor, 0, len(b.jobs))
	for _, entry := range b.jobs {
		items = append(items, entry.descriptor)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PluginID != items[j].PluginID {
			return items[i].PluginID < items[j].PluginID
		}
		return items[i].JobID < items[j].JobID
	})
	return items
}

func NormalizeBackgroundJobDescriptor(descriptor BackgroundJobDescriptor) BackgroundJobDescriptor {
	descriptor.PluginID = strings.TrimSpace(descriptor.PluginID)
	descriptor.JobID = strings.TrimSpace(descriptor.JobID)
	descriptor.Title = strings.TrimSpace(descriptor.Title)
	descriptor.Capability = strings.TrimSpace(descriptor.Capability)
	descriptor.Subject = strings.TrimSpace(descriptor.Subject)
	descriptor.Schedule = strings.TrimSpace(descriptor.Schedule)
	if descriptor.MaxConcurrency == 0 {
		descriptor.MaxConcurrency = 1
	}
	return descriptor
}

func pluginBackgroundJobKey(pluginID string, jobID string) string {
	return strings.TrimSpace(pluginID) + "\x00" + strings.TrimSpace(jobID)
}

func isUnavailableBackgroundJobHandler(handler BackgroundJobHandler) bool {
	_, ok := handler.(unavailableBackgroundJobHandler)
	return ok
}

func validateBackgroundJobPayload(schema map[string]any, payload json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var value any
	if len(strings.TrimSpace(string(payload))) == 0 {
		value = map[string]any{}
	} else if err := json.Unmarshal(payload, &value); err != nil {
		return fmt.Errorf("%w: JSON could not be decoded", ErrPluginBackgroundJobInvalidPayload)
	}
	return validateActionSchemaValue("$", value, schema, ErrPluginBackgroundJobInvalidPayload)
}

func validateBackgroundJobResult(schema map[string]any, data any) error {
	if len(schema) == 0 {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w: JSON could not be encoded", ErrPluginBackgroundJobInvalidResult)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: JSON could not be decoded", ErrPluginBackgroundJobInvalidResult)
	}
	return validateActionSchemaValue("$.data", value, schema, ErrPluginBackgroundJobInvalidResult)
}
