package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Attribute names Langfuse maps onto its own trace and observation fields.
//
// The gen_ai.* names come from the OpenTelemetry GenAI semantic conventions and are
// what makes these spans readable by backends other than Langfuse. Note the provider
// attribute is gen_ai.provider.name: gen_ai.system is the older spelling and is
// deprecated.
const (
	attrTraceName         = "langfuse.trace.name"
	attrTraceTags         = "langfuse.trace.tags"
	attrTraceMetadata     = "langfuse.trace.metadata."
	attrUserID            = "langfuse.user.id"
	attrSessionID         = "langfuse.session.id"
	attrEnvironment       = "langfuse.environment"
	attrRelease           = "langfuse.release"
	attrObservationType   = "langfuse.observation.type"
	attrObservationLevel  = "langfuse.observation.level"
	attrObservationInput  = "langfuse.observation.input"
	attrObservationOutput = "langfuse.observation.output"
	attrUsageDetails      = "langfuse.observation.usage_details"
	attrCostDetails       = "langfuse.observation.cost_details"

	attrProviderName  = "gen_ai.provider.name"
	attrRequestModel  = "gen_ai.request.model"
	attrResponseModel = "gen_ai.response.model"

	observationTypeSpan       = "span"
	observationTypeGeneration = "generation"
	observationLevelError     = "ERROR"
	observationLevelDefault   = "DEFAULT"
)

// spanIdentityKey carries the IDs a span should be created with.
//
// The OpenTelemetry SDK mints IDs through an IDGenerator that only receives a
// context, so this is how a request ID reaches it. The alternative — starting the
// span under a synthetic parent to fix the trace ID — produces a span whose parent
// is never exported, which is not a root.
type spanIdentityKeyType struct{}

var spanIdentityKey spanIdentityKeyType

type spanIdentity struct {
	traceID trace.TraceID
	spanID  trace.SpanID
}

func withSpanIdentity(ctx context.Context, traceID trace.TraceID, spanID trace.SpanID) context.Context {
	return context.WithValue(ctx, spanIdentityKey, spanIdentity{traceID: traceID, spanID: spanID})
}

// deterministicIDGenerator derives every ID from the gateway request ID, so the
// x-request-id a client already holds is enough to find the trace with no lookup
// table.
//
// This is a lookup convenience, not a deduplication guarantee: Langfuse may still
// create a duplicate observation if the same span ID arrives twice. Export retries
// are disabled for that reason.
type deterministicIDGenerator struct{}

func (deterministicIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	identity, ok := ctx.Value(spanIdentityKey).(spanIdentity)
	if !ok {
		return randomTraceID(), randomSpanID()
	}
	return identity.traceID, identity.spanID
}

func (deterministicIDGenerator) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	identity, ok := ctx.Value(spanIdentityKey).(spanIdentity)
	if !ok {
		return randomSpanID()
	}
	return identity.spanID
}

func randomTraceID() trace.TraceID {
	var id trace.TraceID
	_, _ = rand.Read(id[:])
	if !id.IsValid() {
		id[0] = 1
	}
	return id
}

func randomSpanID() trace.SpanID {
	var id trace.SpanID
	_, _ = rand.Read(id[:])
	if !id.IsValid() {
		id[0] = 1
	}
	return id
}

// record converts one completion into a trace: a root span for the request, and a
// generation child for every candidate that entered the invocation path.
func (e *otlpTraceEmitter) record(completion GatewayCallCompletion) {
	// The local reading, not the database one in StartedAt: it is compared with
	// FinishedAt, which this process stamps, so a database host running ahead
	// would otherwise place the whole trace in the future.
	startedAt := completion.Call.measuredStart()
	if startedAt.IsZero() {
		startedAt = completion.FinishedAt
	}
	finishedAt := completion.FinishedAt
	if finishedAt.Before(startedAt) {
		finishedAt = startedAt
	}

	shared := e.sharedAttributes(completion)
	traceID := traceIDForRequest(completion.Call.RequestID)
	// Detached from any request context: a client that hangs up must not cancel the
	// record of what it did.
	ctx := withSpanIdentity(context.Background(), traceID, spanIDForRequest(completion.Call.RequestID, "request"))

	rootAttributes := append([]attribute.KeyValue{}, shared...)
	rootAttributes = append(rootAttributes,
		attribute.String(attrObservationType, observationTypeSpan),
		attribute.String(attrObservationLevel, observationLevel(completion.StatusCode)),
		attribute.Int("http.response.status_code", completion.StatusCode),
	)
	rootAttributes = appendStringAttribute(rootAttributes, "error.type", completion.ErrorCode)
	// The root deliberately carries no usage or cost. Langfuse v4 aggregates over
	// observations, so repeating the children's usage here would double every token
	// and every dollar in the project totals.
	//
	// Input and output are already rendered and already gated at the completion
	// boundary: with payload capture off they are empty, so nothing is filtered here.
	rootAttributes = appendStringAttribute(rootAttributes, attrObservationInput, completion.RequestBody)
	rootAttributes = appendStringAttribute(rootAttributes, attrObservationOutput, completion.ResponseBody)
	rootAttributes = appendStringAttribute(rootAttributes, "error.message", completion.ErrorMessage)

	ctx, root := e.tracer.Start(ctx, traceName(completion),
		trace.WithTimestamp(startedAt),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(rootAttributes...),
	)
	if completion.StatusCode >= 400 {
		root.SetStatus(codes.Error, completion.ErrorCode)
	}

	for index, attempt := range completion.Attempts {
		if attempt.Invoked {
			e.recordAttempt(ctx, completion, shared, index, attempt, traceID)
			continue
		}
		// A candidate that never reached a provider is not an observation: it made no
		// generation and consumed nothing. Recording it as an event still keeps the
		// routing decision visible on the trace.
		root.AddEvent("gateway.route_skipped", trace.WithTimestamp(startedAt), trace.WithAttributes(
			attribute.Int("gateway.attempt", index+1),
			attribute.String("gateway.provider_id", attempt.Selection.Provider.ID),
			attribute.String("gateway.provider_model", attempt.Selection.ProviderModel),
			attribute.String("error.type", attempt.ErrorCode),
		))
	}

	root.End(trace.WithTimestamp(finishedAt))
}

// recordAttempt emits one generation. Usage and cost live here rather than on the
// root so a failover reports what each candidate actually consumed.
func (e *otlpTraceEmitter) recordAttempt(ctx context.Context, completion GatewayCallCompletion, shared []attribute.KeyValue, index int, attempt RouteAttempt, traceID trace.TraceID) {
	startedAt := attempt.StartedAt
	if startedAt.IsZero() {
		startedAt = completion.Call.measuredStart()
	}
	endedAt := attempt.EndedAt
	if endedAt.Before(startedAt) {
		endedAt = startedAt
	}

	attributes := append([]attribute.KeyValue{}, shared...)
	attributes = append(attributes,
		attribute.String(attrObservationType, observationTypeGeneration),
		attribute.String(attrObservationLevel, observationLevel(attempt.Status)),
		attribute.Int("gateway.attempt", index+1),
		attribute.Int("http.response.status_code", attempt.Status),
	)
	attributes = appendStringAttribute(attributes, attrProviderName, attempt.Selection.Provider.Type)
	attributes = appendStringAttribute(attributes, attrRequestModel, attempt.Selection.ProviderModel)
	attributes = appendStringAttribute(attributes, attrResponseModel, attempt.Usage.ServedModel)
	attributes = appendStringAttribute(attributes, "gateway.provider_id", attempt.Selection.Provider.ID)
	attributes = appendStringAttribute(attributes, "gateway.provider_resource_id", routeResourceID(attempt.Selection))
	attributes = appendStringAttribute(attributes, "gateway.route_id", attempt.Selection.Route.ID)
	// Attempt-local, so they belong here rather than on the shared attributes. Putting
	// the call's final Usage on every generation would label a failed candidate with
	// the upstream request ID of the one that eventually succeeded.
	attributes = appendStringAttribute(attributes, "gateway.transport", attempt.Usage.Transport)
	attributes = appendStringAttribute(attributes, "gateway.upstream_request_id", attempt.Usage.UpstreamRequestID)
	attributes = appendStringAttribute(attributes, "error.type", attempt.ErrorCode)
	if details := usageDetails(attempt.Usage); details != "" {
		attributes = append(attributes, attribute.String(attrUsageDetails, details))
	}
	if details := costDetails(attempt.Usage); details != "" {
		attributes = append(attributes, attribute.String(attrCostDetails, details))
	}
	// Empty unless payload capture is on, where it is cleared at the completion
	// boundary along with the request and response bodies.
	attributes = appendStringAttribute(attributes, "error.message", attempt.Error)

	ctx = withSpanIdentity(ctx, traceID, spanIDForRequest(completion.Call.RequestID, fmt.Sprintf("attempt:%d", index+1)))
	_, span := e.tracer.Start(ctx, generationName(attempt),
		trace.WithTimestamp(startedAt),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attributes...),
	)
	if attempt.Status >= 400 {
		span.SetStatus(codes.Error, attempt.ErrorCode)
	}
	span.End(trace.WithTimestamp(endedAt))
}

// sharedAttributes are the trace-level attributes Langfuse v4 needs on every span.
// An attribute present only on the root cannot be used to filter or aggregate that
// root's children, so these are repeated rather than set once.
func (e *otlpTraceEmitter) sharedAttributes(completion GatewayCallCompletion) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String(attrTraceName, traceName(completion)),
		attribute.StringSlice(attrTraceTags, traceTags(completion)),
	}
	attributes = appendStringAttribute(attributes, attrEnvironment, e.environment)
	attributes = appendStringAttribute(attributes, attrRelease, e.release)
	// The API key is the calling identity of a gateway, which is what Langfuse's user
	// view is useful for here. It is deliberately not the key's owner: that is an
	// administrator, not the caller, and the two are recorded separately below.
	attributes = appendStringAttribute(attributes, attrUserID, completion.Call.Key.ID)
	attributes = appendStringAttribute(attributes, attrSessionID, sessionID(completion.Call.Affinity))

	attributes = appendStringAttribute(attributes, attrTraceMetadata+"request_id", completion.Call.RequestID)
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"completion_kind", string(completion.Kind))
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"project_id", completion.Call.Project.ID)
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"project_name", completion.Call.Project.Name)
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"api_key_id", completion.Call.Key.ID)
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"api_key_name", completion.Call.Key.Name)
	// The administrative owner of the key or project. Named for what it is so it is
	// never mistaken for the end user who made the request.
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"attributed_owner_user_id", usageAttributionUserID(completion.Call.Key, completion.Call.Project))
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"model", completion.Call.Model.Name)
	attributes = appendStringAttribute(attributes, attrTraceMetadata+"stream", strconv.FormatBool(completion.Call.Stream))
	if completion.Call.Affinity != nil {
		// The affinity key is an HMAC of a session or user identifier, and which of
		// the two it is depends on the mode. Both are reported so neither is mistaken
		// for the other.
		attributes = appendStringAttribute(attributes, attrTraceMetadata+"affinity_kind", completion.Call.Affinity.Kind)
		attributes = appendStringAttribute(attributes, attrTraceMetadata+"affinity_key_hash", completion.Call.Affinity.KeyHash)
	}
	return attributes
}

// sessionID reports an affinity key as a Langfuse session only when the mode makes
// it unambiguously one. Cache locality keys can be derived from a user identifier
// rather than a session, and labelling those as sessions would silently merge every
// concurrent conversation of a user into one.
func sessionID(affinity *RequestAffinity) string {
	if affinity == nil || affinity.Kind != AffinityKindCodexSession {
		return ""
	}
	return affinity.KeyHash
}

func traceName(completion GatewayCallCompletion) string {
	if model := strings.TrimSpace(completion.Call.Model.Name); model != "" {
		return model
	}
	return "gateway request"
}

func generationName(attempt RouteAttempt) string {
	if model := strings.TrimSpace(attempt.Selection.ProviderModel); model != "" {
		return model
	}
	return "generation"
}

// traceTags labels the lifecycle so playground traffic can be excluded from cost
// analysis: it runs through the same routing and is otherwise indistinguishable
// from a customer request.
func traceTags(completion GatewayCallCompletion) []string {
	tags := []string{"tokenhub", string(completion.Kind)}
	if completion.Call.Stream {
		tags = append(tags, "stream")
	}
	return tags
}

func observationLevel(statusCode int) string {
	if statusCode >= 400 {
		return observationLevelError
	}
	return observationLevelDefault
}

// usageDetails renders mutually exclusive token buckets.
//
// TokenHub's counters are nested, as its own documentation puts it: "input and
// output totals already contain their detail categories". Prompt tokens include the
// cached, cache-write and audio input tokens; completion tokens include the
// reasoning, audio and prediction tokens. Langfuse stores usage_details verbatim and
// sums the buckets, so the nesting has to be unwound here or every request reports
// more tokens than it used.
func usageDetails(usage Usage) string {
	details := map[string]int64{}
	inputRemainder := splitTokens(details, usage.PromptTokens, []tokenBucket{
		{"input_cached", usage.CachedInputTokens},
		{"input_cache_write", usage.CacheWriteInputTokens},
		{"input_audio", usage.InputAudioTokens},
	})
	outputRemainder := splitTokens(details, usage.CompletionTokens, []tokenBucket{
		{"output_reasoning", usage.ReasoningOutputTokens},
		{"output_audio", usage.OutputAudioTokens},
		{"output_accepted_prediction", usage.AcceptedPredictionTokens},
		{"output_rejected_prediction", usage.RejectedPredictionTokens},
	})
	setPositive(details, "input", inputRemainder)
	setPositive(details, "output", outputRemainder)
	if len(details) == 0 {
		return ""
	}
	setPositive(details, "total", usage.TotalTokens)
	encoded, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(encoded)
}

type tokenBucket struct {
	name  string
	count int64
}

// splitTokens allocates detail buckets out of a total and returns what is left.
//
// Each bucket is capped by what remains, so the buckets always sum to exactly the
// total no matter what an upstream reports. Without the cap, a provider whose detail
// counts exceed the total it also reported would inflate everything Langfuse sums.
func splitTokens(details map[string]int64, total int64, buckets []tokenBucket) int64 {
	remaining := clampNonNegative(total)
	for _, bucket := range buckets {
		count := clampNonNegative(bucket.count)
		if count > remaining {
			count = remaining
		}
		setPositive(details, bucket.name, count)
		remaining -= count
	}
	return remaining
}

// costDetails reports what the request is billed at.
//
// Usage.ProviderCostUSD, the provider's own cost, is deliberately not exported at
// all. TokenHub confines it to the privileged request audit and keeps it out of the
// metrics surface on purpose, so shipping it to a third-party trace backend would
// widen exactly the exposure that decision narrows. It is also computed inside
// FinishCall, which is after this boundary, so it would read as zero here anyway.
func costDetails(usage Usage) string {
	if usage.CostUSD <= 0 {
		return ""
	}
	encoded, err := json.Marshal(map[string]float64{"total": usage.CostUSD})
	if err != nil {
		return ""
	}
	return string(encoded)
}

func setPositive(details map[string]int64, key string, value int64) {
	if value > 0 {
		details[key] = value
	}
}

func clampNonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

// traceIDForRequest derives a stable trace ID from the gateway request ID, so the
// x-request-id returned to a client is enough to find the trace.
func traceIDForRequest(requestID string) trace.TraceID {
	digest := sha256.Sum256([]byte("tokenhub-trace:" + requestID))
	var id trace.TraceID
	copy(id[:], digest[:len(id)])
	if !id.IsValid() {
		// An all-zero ID is rejected by the SDK. Reaching this needs a SHA-256
		// preimage of sixteen zero bytes, but falling back costs nothing.
		id[0] = 1
	}
	return id
}

func spanIDForRequest(requestID string, part string) trace.SpanID {
	digest := sha256.Sum256([]byte(fmt.Sprintf("tokenhub-span:%s:%s", part, requestID)))
	var id trace.SpanID
	copy(id[:], digest[:len(id)])
	if !id.IsValid() {
		id[0] = 1
	}
	return id
}

// tracingFinishedAt is the moment the gateway considered the call complete. It is
// stamped where the call finishes rather than where the span is built, so queueing
// delay in the exporter never inflates the reported latency.
func tracingFinishedAt() time.Time {
	return time.Now().UTC()
}
