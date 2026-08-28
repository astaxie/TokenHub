package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrGatewayHookDenied         = errors.New("gateway hook denied the request")
	ErrGatewayHookRouteSkipped   = errors.New("gateway hook skipped the route")
	ErrGatewayHookShortCircuited = errors.New("gateway hook short-circuited the request")
)

type GatewayHookDecision string

const (
	HookDecisionContinue     GatewayHookDecision = "continue"
	HookDecisionDeny         GatewayHookDecision = "deny"
	HookDecisionShortCircuit GatewayHookDecision = "short_circuit"
)

type GatewayEnvelope struct {
	Version        string                     `json:"version"`
	Protocol       string                     `json:"protocol"`
	Operation      string                     `json:"operation"`
	Model          string                     `json:"model"`
	RequestBody    json.RawMessage            `json:"request_body,omitempty"`
	NormalizedText []TextSegment              `json:"normalized_text,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
}

type TextSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type GatewayHookInput struct {
	RequestID        string           `json:"request_id"`
	Stage            GatewayHookStage `json:"stage"`
	Envelope         GatewayEnvelope  `json:"envelope"`
	Data             GatewayHookData  `json:"data,omitempty"`
	OriginalEnvelope GatewayEnvelope  `json:"-"`
	OriginalData     GatewayHookData  `json:"-"`
}

type GatewayHookData map[GatewayDataClass]json.RawMessage

type GatewayHookResult struct {
	Decision    GatewayHookDecision           `json:"decision"`
	Writes      map[GatewayDataClass]RawPatch `json:"writes,omitempty"`
	AuditEvents []json.RawMessage             `json:"audit_events,omitempty"`
}

type RawPatch struct {
	Value json.RawMessage `json:"value"`
}

type GatewayHookHandler interface {
	ExecuteGatewayHook(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error)
}

type GatewayHookHandlerFunc func(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error)

func (f GatewayHookHandlerFunc) ExecuteGatewayHook(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error) {
	return f(ctx, input)
}

type GatewayHookRunReport struct {
	Stage            GatewayHookStage       `json:"stage"`
	Results          []GatewayHookRunResult `json:"results"`
	TerminalDecision GatewayHookDecision    `json:"terminal_decision,omitempty"`
}

type GatewayHookRunResult struct {
	PluginID      string                        `json:"plugin_id"`
	HookID        string                        `json:"hook_id"`
	Decision      GatewayHookDecision           `json:"decision"`
	Writes        map[GatewayDataClass]RawPatch `json:"writes,omitempty"`
	FailurePolicy GatewayHookFailurePolicy      `json:"failure_policy"`
	Status        GatewayHookRunStatus          `json:"status"`
	Error         string                        `json:"error,omitempty"`
	DurationMS    int64                         `json:"duration_ms"`
}

type GatewayHookRunStatus string

const (
	HookRunSucceeded GatewayHookRunStatus = "succeeded"
	HookRunSkipped   GatewayHookRunStatus = "skipped"
	HookRunFailed    GatewayHookRunStatus = "failed"
)

type GatewayHookRunner struct {
	chain    *GatewayChainRegistry
	handlers map[string]GatewayHookHandler
}

func NewGatewayHookRunner(chain *GatewayChainRegistry) *GatewayHookRunner {
	return &GatewayHookRunner{
		chain:    chain,
		handlers: map[string]GatewayHookHandler{},
	}
}

func (r *GatewayHookRunner) RegisterHandler(descriptor GatewayHookDescriptor, handler GatewayHookHandler) error {
	if r == nil {
		return fmt.Errorf("gateway hook runner is not configured")
	}
	if handler == nil {
		return fmt.Errorf("gateway hook handler is required")
	}
	if descriptor.PluginID == "" || descriptor.HookID == "" {
		return fmt.Errorf("gateway hook descriptor identity is required")
	}
	r.handlers[gatewayHookKey(descriptor)] = handler
	return nil
}

func (r *GatewayHookRunner) RunStage(ctx context.Context, stage GatewayHookStage, input GatewayHookInput) (GatewayHookRunReport, error) {
	if r == nil || r.chain == nil {
		return GatewayHookRunReport{Stage: stage}, nil
	}
	return r.RunStageHooks(ctx, stage, input, r.chain.Hooks(stage))
}

func (r *GatewayHookRunner) RunStageHooks(ctx context.Context, stage GatewayHookStage, input GatewayHookInput, hooks []GatewayHookDescriptor) (GatewayHookRunReport, error) {
	report := GatewayHookRunReport{Stage: stage}
	if r == nil {
		return report, nil
	}
	input = preserveGatewayHookOriginals(input)
	for _, hook := range hooks {
		if hook.Stage != stage {
			continue
		}
		result, err := r.runHook(ctx, hook, input)
		report.Results = append(report.Results, result)
		if err != nil {
			return report, err
		}
		applyGatewayHookWritesToInput(&input, result.Writes)
		if result.Decision != HookDecisionContinue {
			report.TerminalDecision = result.Decision
			if result.Decision == HookDecisionDeny {
				return report, ErrGatewayHookDenied
			}
			return report, nil
		}
	}
	return report, nil
}

func (r *GatewayHookRunner) runHook(ctx context.Context, hook GatewayHookDescriptor, input GatewayHookInput) (GatewayHookRunResult, error) {
	startedAt := time.Now()
	run := GatewayHookRunResult{
		PluginID:      hook.PluginID,
		HookID:        hook.HookID,
		FailurePolicy: hook.FailurePolicy,
		Status:        HookRunSkipped,
		Decision:      HookDecisionContinue,
	}
	handler := r.handlers[gatewayHookKey(hook)]
	if handler == nil {
		err := fmt.Errorf("gateway hook handler %s/%s is not registered", hook.PluginID, hook.HookID)
		run.DurationMS = elapsedMillis(startedAt)
		return applyGatewayHookFailure(run, hook, err)
	}
	hookCtx := ctx
	cancel := func() {}
	if hook.TimeoutMillis > 0 {
		hookCtx, cancel = context.WithTimeout(ctx, time.Duration(hook.TimeoutMillis)*time.Millisecond)
	}
	defer cancel()
	input = clipGatewayHookInput(input, hook.Reads)
	input.Stage = hook.Stage
	result, err := handler.ExecuteGatewayHook(hookCtx, input)
	if err == nil {
		err = hookCtx.Err()
	}
	if err != nil {
		run.DurationMS = elapsedMillis(startedAt)
		return applyGatewayHookFailure(run, hook, err)
	}
	if result.Decision == "" {
		result.Decision = HookDecisionContinue
	}
	if err := validateGatewayHookResult(hook, result); err != nil {
		run.DurationMS = elapsedMillis(startedAt)
		return applyGatewayHookFailure(run, hook, err)
	}
	run.Status = HookRunSucceeded
	run.Decision = result.Decision
	run.Writes = result.Writes
	run.DurationMS = elapsedMillis(startedAt)
	return run, nil
}

func validateGatewayHookResult(hook GatewayHookDescriptor, result GatewayHookResult) error {
	stagePolicy, ok := GatewayStagePolicy(hook.Stage)
	if !ok {
		return fmt.Errorf("unsupported gateway hook stage %q", hook.Stage)
	}
	switch result.Decision {
	case HookDecisionContinue, HookDecisionDeny, HookDecisionShortCircuit:
	default:
		return fmt.Errorf("gateway hook %s/%s returned unsupported decision %q", hook.PluginID, hook.HookID, result.Decision)
	}
	if result.Decision == HookDecisionDeny && !stagePolicy.AllowsDeny {
		return fmt.Errorf("gateway hook stage %q does not allow deny decisions", hook.Stage)
	}
	if result.Decision == HookDecisionShortCircuit && !stagePolicy.AllowsShortCircuit {
		return fmt.Errorf("gateway hook stage %q does not allow short-circuit decisions", hook.Stage)
	}
	if len(result.Writes) == 0 {
		return nil
	}
	declared := map[GatewayDataClass]struct{}{}
	for _, dataClass := range hook.Writes {
		declared[dataClass] = struct{}{}
	}
	stageAllowed := map[GatewayDataClass]struct{}{}
	for _, dataClass := range stagePolicy.Writes {
		stageAllowed[dataClass] = struct{}{}
	}
	for dataClass := range result.Writes {
		if _, ok := declared[dataClass]; !ok {
			return fmt.Errorf("gateway hook %s/%s wrote undeclared data class %q", hook.PluginID, hook.HookID, dataClass)
		}
		if _, ok := stageAllowed[dataClass]; !ok {
			return fmt.Errorf("gateway hook stage %q cannot write data class %q", hook.Stage, dataClass)
		}
	}
	return nil
}

func applyGatewayHookWritesToInput(input *GatewayHookInput, writes map[GatewayDataClass]RawPatch) {
	if input == nil || len(writes) == 0 {
		return
	}
	if input.Data == nil {
		input.Data = GatewayHookData{}
	}
	for dataClass, patch := range writes {
		value := cloneRawMessage(patch.Value)
		input.Data[dataClass] = value
		switch dataClass {
		case DataRequestBody:
			input.Envelope.RequestBody = cloneRawMessage(value)
		case DataProviderRequest:
			input.Envelope.RequestBody = cloneRawMessage(value)
		case DataProviderResponse:
			input.Envelope.RequestBody = cloneRawMessage(value)
		case DataNormalizedText:
			var segments []TextSegment
			if json.Unmarshal(value, &segments) == nil {
				input.Envelope.NormalizedText = segments
			}
		}
	}
}

func clipGatewayHookInput(input GatewayHookInput, reads []GatewayDataClass) GatewayHookInput {
	allowed := map[GatewayDataClass]struct{}{}
	for _, dataClass := range reads {
		allowed[dataClass] = struct{}{}
	}
	clipped := GatewayHookInput{
		RequestID: input.RequestID,
		Stage:     input.Stage,
		Envelope: GatewayEnvelope{
			Version:   input.Envelope.Version,
			Protocol:  input.Envelope.Protocol,
			Operation: input.Envelope.Operation,
			Model:     input.Envelope.Model,
		},
		OriginalEnvelope: cloneGatewayEnvelope(input.OriginalEnvelope),
		OriginalData:     clipGatewayHookData(input.OriginalData, reads),
	}
	if _, ok := allowed[DataRequestBody]; ok {
		clipped.Envelope.RequestBody = cloneRawMessage(input.Envelope.RequestBody)
	} else if _, ok := allowed[DataProviderRequest]; ok {
		clipped.Envelope.RequestBody = cloneRawMessage(input.Envelope.RequestBody)
	} else if _, ok := allowed[DataProviderResponse]; ok {
		clipped.Envelope.RequestBody = cloneRawMessage(input.Envelope.RequestBody)
	}
	if _, ok := allowed[DataNormalizedText]; ok {
		clipped.Envelope.NormalizedText = append([]TextSegment(nil), input.Envelope.NormalizedText...)
	}
	if len(input.Data) > 0 {
		clipped.Data = GatewayHookData{}
		for dataClass := range allowed {
			if value, ok := input.Data[dataClass]; ok {
				clipped.Data[dataClass] = cloneRawMessage(value)
			}
		}
		if len(clipped.Data) == 0 {
			clipped.Data = nil
		}
	}
	return clipped
}

func preserveGatewayHookOriginals(input GatewayHookInput) GatewayHookInput {
	if gatewayEnvelopeEmpty(input.OriginalEnvelope) {
		input.OriginalEnvelope = cloneGatewayEnvelope(input.Envelope)
	}
	if input.OriginalData == nil {
		input.OriginalData = cloneGatewayHookData(input.Data)
	}
	return input
}

func applyGatewayHookFailure(run GatewayHookRunResult, hook GatewayHookDescriptor, err error) (GatewayHookRunResult, error) {
	run.Status = HookRunFailed
	run.Error = err.Error()
	switch hook.FailurePolicy {
	case FailurePolicyFailOpen, FailurePolicyObserveOnly:
		run.Status = HookRunSkipped
		return run, nil
	case FailurePolicySkipRoute:
		return run, fmt.Errorf("%w: %w", ErrGatewayHookRouteSkipped, err)
	default:
		return run, err
	}
}

func NoopGatewayHookHandler() GatewayHookHandler {
	return GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		return GatewayHookResult{Decision: HookDecisionContinue}, nil
	})
}

func IsGatewayHookTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func IsGatewayHookDenied(err error) bool {
	return errors.Is(err, ErrGatewayHookDenied)
}

func IsGatewayHookRouteSkipped(err error) bool {
	return errors.Is(err, ErrGatewayHookRouteSkipped)
}

func IsGatewayHookShortCircuited(err error) bool {
	return errors.Is(err, ErrGatewayHookShortCircuited)
}

func gatewayHookKey(hook GatewayHookDescriptor) string {
	return hook.PluginID + "\x00" + hook.HookID
}

func elapsedMillis(startedAt time.Time) int64 {
	if startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(startedAt).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneGatewayEnvelope(envelope GatewayEnvelope) GatewayEnvelope {
	clone := GatewayEnvelope{
		Version:        envelope.Version,
		Protocol:       envelope.Protocol,
		Operation:      envelope.Operation,
		Model:          envelope.Model,
		RequestBody:    cloneRawMessage(envelope.RequestBody),
		NormalizedText: append([]TextSegment(nil), envelope.NormalizedText...),
	}
	if len(envelope.Metadata) > 0 {
		clone.Metadata = map[string]json.RawMessage{}
		for key, value := range envelope.Metadata {
			clone.Metadata[key] = cloneRawMessage(value)
		}
	}
	return clone
}

func cloneGatewayHookData(data GatewayHookData) GatewayHookData {
	if len(data) == 0 {
		return nil
	}
	clone := GatewayHookData{}
	for dataClass, value := range data {
		clone[dataClass] = cloneRawMessage(value)
	}
	return clone
}

func clipGatewayHookData(data GatewayHookData, reads []GatewayDataClass) GatewayHookData {
	if len(data) == 0 {
		return nil
	}
	allowed := map[GatewayDataClass]struct{}{}
	for _, dataClass := range reads {
		allowed[dataClass] = struct{}{}
	}
	clipped := GatewayHookData{}
	for dataClass := range allowed {
		if value, ok := data[dataClass]; ok {
			clipped[dataClass] = cloneRawMessage(value)
		}
	}
	if len(clipped) == 0 {
		return nil
	}
	return clipped
}

func gatewayEnvelopeEmpty(envelope GatewayEnvelope) bool {
	return envelope.Version == "" &&
		envelope.Protocol == "" &&
		envelope.Operation == "" &&
		envelope.Model == "" &&
		len(envelope.RequestBody) == 0 &&
		len(envelope.NormalizedText) == 0 &&
		len(envelope.Metadata) == 0
}
