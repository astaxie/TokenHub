package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrPluginBackgroundJobNotFound       = errors.New("plugin background job is not registered")
	ErrPluginBackgroundJobUnavailable    = errors.New("plugin background job handler is unavailable")
	ErrPluginBackgroundJobInvalidPayload = errors.New("plugin background job payload is invalid")
	ErrPluginBackgroundJobInvalidResult  = errors.New("plugin background job result is invalid")
	ErrPluginBackgroundJobBusy           = errors.New("plugin background job concurrency limit reached")
	ErrPluginBackgroundJobShuttingDown   = errors.New("plugin background job runner is shutting down")
)

const maxBackgroundJobRetryDelay = time.Duration(1<<63 - 1)

type BackgroundJobRetryPolicy struct {
	MaxAttempts       int     `json:"max_attempts,omitempty" yaml:"max_attempts"`
	BackoffMillis     int     `json:"backoff_millis,omitempty" yaml:"backoff_millis"`
	BackoffMultiplier float64 `json:"backoff_multiplier,omitempty" yaml:"backoff_multiplier"`
	JitterMillis      int     `json:"jitter_millis,omitempty" yaml:"jitter_millis"`
	DeadLetter        bool    `json:"dead_letter,omitempty" yaml:"dead_letter"`
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
	Permissions    []PermissionDescriptor   `json:"permissions,omitempty"`
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

type BackgroundJobRunStatus string

const (
	BackgroundJobRunSucceeded BackgroundJobRunStatus = "succeeded"
	BackgroundJobRunFailed    BackgroundJobRunStatus = "failed"
	BackgroundJobRunSkipped   BackgroundJobRunStatus = "skipped"
)

type BackgroundJobRunRecord struct {
	PluginID    string                 `json:"plugin_id"`
	JobID       string                 `json:"job_id"`
	Trigger     string                 `json:"trigger"`
	Status      BackgroundJobRunStatus `json:"status"`
	Attempts    int                    `json:"attempts"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt time.Time              `json:"completed_at"`
	Error       string                 `json:"error,omitempty"`
	Result      BackgroundJobResult    `json:"result,omitempty"`
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
	if descriptor.Retry.BackoffMultiplier < 0 {
		return fmt.Errorf("plugin background job retry backoff_multiplier cannot be negative")
	}
	if descriptor.Retry.BackoffMultiplier > 0 && descriptor.Retry.BackoffMultiplier < 1 {
		return fmt.Errorf("plugin background job retry backoff_multiplier must be 1 or greater")
	}
	if descriptor.Retry.JitterMillis < 0 {
		return fmt.Errorf("plugin background job retry jitter_millis cannot be negative")
	}
	for _, permission := range descriptor.Permissions {
		if err := ValidatePermissionDescriptor(permission); err != nil {
			return err
		}
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
	if grant, ok := PluginPermissionGrantFromHandler(entry.handler); ok {
		if err := RequirePluginPermissions(entry.descriptor.Permissions, grant); err != nil {
			return BackgroundJobResult{}, err
		}
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
	descriptor.Permissions = NormalizePermissionDescriptors(descriptor.Permissions)
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

type BackgroundJobRunner struct {
	broker *BackgroundJobBroker

	mu        sync.Mutex
	running   map[string]int
	lastRuns  map[string]BackgroundJobRunRecord
	deadRuns  map[string][]BackgroundJobRunRecord
	scheduler *backgroundScheduler

	retrySleeper func(context.Context, time.Duration) error
	retryJitter  func(time.Duration) time.Duration

	lifecycleMu    sync.Mutex
	shuttingDown   bool
	shutdownActive bool
	activeRuns     map[*backgroundJobRunToken]struct{}
}

type backgroundJobRunToken struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewBackgroundJobRunner(broker *BackgroundJobBroker) *BackgroundJobRunner {
	runner := &BackgroundJobRunner{
		broker:       broker,
		running:      map[string]int{},
		lastRuns:     map[string]BackgroundJobRunRecord{},
		deadRuns:     map[string][]BackgroundJobRunRecord{},
		retrySleeper: backgroundJobRetrySleep,
		retryJitter:  backgroundJobRetryJitter,
		activeRuns:   map[*backgroundJobRunToken]struct{}{},
	}
	runner.scheduler = newBackgroundScheduler(runner)
	return runner
}

func (r *BackgroundJobRunner) Run(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobRunRecord, error) {
	if r == nil || r.broker == nil {
		return BackgroundJobRunRecord{}, fmt.Errorf("plugin background job runner is not configured")
	}
	invocation.PluginID = strings.TrimSpace(invocation.PluginID)
	invocation.JobID = strings.TrimSpace(invocation.JobID)
	if invocation.Trigger == "" {
		invocation.Trigger = "manual"
	}
	descriptor, ok := r.broker.Describe(invocation.PluginID, invocation.JobID)
	if !ok {
		return BackgroundJobRunRecord{}, ErrPluginBackgroundJobNotFound
	}
	runCtx, runToken, ok := r.beginRun(ctx)
	if !ok {
		now := time.Now().UTC()
		record := BackgroundJobRunRecord{
			PluginID:    invocation.PluginID,
			JobID:       invocation.JobID,
			Trigger:     invocation.Trigger,
			Status:      BackgroundJobRunSkipped,
			StartedAt:   now,
			CompletedAt: now,
			Error:       ErrPluginBackgroundJobShuttingDown.Error(),
		}
		r.recordRun(record)
		return record, ErrPluginBackgroundJobShuttingDown
	}
	defer r.finishRun(runToken)
	if !r.tryStart(descriptor) {
		now := time.Now().UTC()
		record := BackgroundJobRunRecord{
			PluginID:    invocation.PluginID,
			JobID:       invocation.JobID,
			Trigger:     invocation.Trigger,
			Status:      BackgroundJobRunSkipped,
			StartedAt:   now,
			CompletedAt: now,
			Error:       ErrPluginBackgroundJobBusy.Error(),
		}
		r.recordRun(record)
		return record, ErrPluginBackgroundJobBusy
	}
	defer r.finish(descriptor)
	record, err := r.executeWithRetry(runCtx, descriptor, invocation)
	r.recordRun(record)
	if record.Status == BackgroundJobRunFailed && descriptor.Retry.DeadLetter {
		r.recordDeadLetter(record)
	}
	if record.Status != BackgroundJobRunSucceeded {
		return record, err
	}
	return record, nil
}

func (r *BackgroundJobRunner) LastRuns() []BackgroundJobRunRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]BackgroundJobRunRecord, 0, len(r.lastRuns))
	for _, record := range r.lastRuns {
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PluginID != items[j].PluginID {
			return items[i].PluginID < items[j].PluginID
		}
		return items[i].JobID < items[j].JobID
	})
	return items
}

func (r *BackgroundJobRunner) DeadLetters() []BackgroundJobRunRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]BackgroundJobRunRecord, 0)
	for _, records := range r.deadRuns {
		items = append(items, records...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PluginID != items[j].PluginID {
			return items[i].PluginID < items[j].PluginID
		}
		if items[i].JobID != items[j].JobID {
			return items[i].JobID < items[j].JobID
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items
}

func (r *BackgroundJobRunner) RunDue(ctx context.Context, now time.Time, trigger string) []BackgroundJobRunRecord {
	if r == nil || r.broker == nil {
		return nil
	}
	jobs := r.broker.List()
	records := make([]BackgroundJobRunRecord, 0, len(jobs))
	for _, job := range jobs {
		if ctx.Err() != nil {
			return records
		}
		last, ok := r.lastRun(job.PluginID, job.JobID)
		if !backgroundJobDue(job.Schedule, last, ok, now) {
			continue
		}
		record, _ := r.Run(ctx, BackgroundJobInvocation{
			PluginID: job.PluginID,
			JobID:    job.JobID,
			Trigger:  trigger,
		})
		records = append(records, record)
	}
	return records
}

func (r *BackgroundJobRunner) executeWithRetry(ctx context.Context, descriptor BackgroundJobDescriptor, invocation BackgroundJobInvocation) (BackgroundJobRunRecord, error) {
	startedAt := time.Now().UTC()
	record := BackgroundJobRunRecord{
		PluginID:  invocation.PluginID,
		JobID:     invocation.JobID,
		Trigger:   invocation.Trigger,
		StartedAt: startedAt,
	}
	attempts := descriptor.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		attemptCtx := ctx
		var cancel context.CancelFunc
		if descriptor.TimeoutMillis > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(descriptor.TimeoutMillis)*time.Millisecond)
		}
		result, err := r.broker.Execute(attemptCtx, invocation)
		if cancel != nil {
			cancel()
		}
		record.Attempts = attempt
		if err == nil {
			record.Status = BackgroundJobRunSucceeded
			record.Result = result
			record.CompletedAt = time.Now().UTC()
			return record, nil
		}
		lastErr = err
		if attempt < attempts {
			delay := r.retryDelay(descriptor.Retry, attempt)
			if delay > 0 {
				sleeper := r.retrySleeper
				if sleeper == nil {
					sleeper = backgroundJobRetrySleep
				}
				if sleepErr := sleeper(ctx, delay); sleepErr != nil {
					lastErr = sleepErr
					break
				}
			}
		}
	}
	record.Status = BackgroundJobRunFailed
	record.CompletedAt = time.Now().UTC()
	if lastErr != nil {
		record.Error = lastErr.Error()
	}
	return record, lastErr
}

func (r *BackgroundJobRunner) tryStart(descriptor BackgroundJobDescriptor) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := pluginBackgroundJobKey(descriptor.PluginID, descriptor.JobID)
	limit := descriptor.MaxConcurrency
	if limit <= 0 {
		limit = 1
	}
	if r.running[key] >= limit {
		return false
	}
	r.running[key]++
	return true
}

func (r *BackgroundJobRunner) finish(descriptor BackgroundJobDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := pluginBackgroundJobKey(descriptor.PluginID, descriptor.JobID)
	if r.running[key] <= 1 {
		delete(r.running, key)
		return
	}
	r.running[key]--
}

func (r *BackgroundJobRunner) recordRun(record BackgroundJobRunRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastRuns[pluginBackgroundJobKey(record.PluginID, record.JobID)] = record
}

func (r *BackgroundJobRunner) recordDeadLetter(record BackgroundJobRunRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := pluginBackgroundJobKey(record.PluginID, record.JobID)
	r.deadRuns[key] = append(r.deadRuns[key], record)
}

func (r *BackgroundJobRunner) lastRun(pluginID string, jobID string) (BackgroundJobRunRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.lastRuns[pluginBackgroundJobKey(pluginID, jobID)]
	return record, ok
}

func (r *BackgroundJobRunner) retryDelay(policy BackgroundJobRetryPolicy, failedAttempt int) time.Duration {
	delay := backgroundJobRetryBackoff(policy, failedAttempt)
	if policy.JitterMillis <= 0 {
		return delay
	}
	jitterFunc := r.retryJitter
	if jitterFunc == nil {
		jitterFunc = backgroundJobRetryJitter
	}
	jitter := jitterFunc(time.Duration(policy.JitterMillis) * time.Millisecond)
	if jitter <= 0 {
		return delay
	}
	if delay > maxBackgroundJobRetryDelay-jitter {
		return maxBackgroundJobRetryDelay
	}
	return delay + jitter
}

func backgroundJobRetryBackoff(policy BackgroundJobRetryPolicy, failedAttempt int) time.Duration {
	if policy.BackoffMillis <= 0 || failedAttempt <= 0 {
		return 0
	}
	base := float64(time.Duration(policy.BackoffMillis) * time.Millisecond)
	multiplier := policy.BackoffMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	delay := base * math.Pow(multiplier, float64(failedAttempt-1))
	if delay >= float64(maxBackgroundJobRetryDelay) {
		return maxBackgroundJobRetryDelay
	}
	return time.Duration(delay)
}

func backgroundJobRetrySleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func backgroundJobRetryJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max) + 1))
}

func (r *BackgroundJobRunner) configureRetryForTest(sleeper func(context.Context, time.Duration) error, jitter func(time.Duration) time.Duration) {
	if r == nil {
		return
	}
	if sleeper != nil {
		r.retrySleeper = sleeper
	}
	if jitter != nil {
		r.retryJitter = jitter
	}
}

func (r *BackgroundJobRunner) beginRun(ctx context.Context) (context.Context, *backgroundJobRunToken, bool) {
	runCtx, cancel := context.WithCancel(ctx)
	token := &backgroundJobRunToken{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.shuttingDown {
		cancel()
		return nil, nil, false
	}
	r.activeRuns[token] = struct{}{}
	return runCtx, token, true
}

func (r *BackgroundJobRunner) finishRun(token *backgroundJobRunToken) {
	if r == nil || token == nil {
		return
	}
	token.cancel()
	r.lifecycleMu.Lock()
	delete(r.activeRuns, token)
	if len(r.activeRuns) == 0 && !r.shutdownActive {
		r.shuttingDown = false
	}
	r.lifecycleMu.Unlock()
	close(token.done)
}

func (r *BackgroundJobRunner) beginShutdown() []chan struct{} {
	if r == nil {
		return nil
	}
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.shuttingDown = true
	r.shutdownActive = true
	dones := make([]chan struct{}, 0, len(r.activeRuns))
	for token := range r.activeRuns {
		token.cancel()
		dones = append(dones, token.done)
	}
	return dones
}

func (r *BackgroundJobRunner) finishShutdown() {
	if r == nil {
		return
	}
	r.lifecycleMu.Lock()
	r.shutdownActive = false
	if len(r.activeRuns) == 0 {
		r.shuttingDown = false
	}
	r.lifecycleMu.Unlock()
}

func waitBackgroundJobRuns(ctx context.Context, dones []chan struct{}) error {
	for _, done := range dones {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func backgroundJobDue(schedule string, last BackgroundJobRunRecord, hasLast bool, now time.Time) bool {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return false
	}
	if schedule == "@startup" {
		return !hasLast
	}
	interval, ok := backgroundJobScheduleInterval(schedule)
	if !ok {
		return false
	}
	if !hasLast {
		return true
	}
	return !last.StartedAt.Add(interval).After(now.UTC())
}

func backgroundJobScheduleInterval(schedule string) (time.Duration, bool) {
	if interval, err := time.ParseDuration(schedule); err == nil && interval > 0 {
		return interval, true
	}
	parts := strings.Fields(schedule)
	if len(parts) != 5 || parts[0] == "" {
		return 0, false
	}
	for _, part := range parts[1:] {
		if part != "*" {
			return 0, false
		}
	}
	if !strings.HasPrefix(parts[0], "*/") {
		return 0, false
	}
	minutes, err := parsePositiveInt(parts[0][2:])
	if err != nil {
		return 0, false
	}
	return time.Duration(minutes) * time.Minute, true
}

func parsePositiveInt(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty integer")
	}
	var result int
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid integer %q", value)
		}
		result = result*10 + int(ch-'0')
	}
	if result <= 0 {
		return 0, fmt.Errorf("integer must be positive")
	}
	return result, nil
}
