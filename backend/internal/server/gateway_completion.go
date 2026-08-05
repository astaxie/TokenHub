package server

import (
	"context"
	"net/http"
	"time"
)

// CompletionKind names the lifecycle that produced a completion.
//
// Traces carry it so synthetic traffic stays separable from the gateway traffic
// operators bill against: playground calls run through the same routing and would
// otherwise be indistinguishable from a customer request in cost analysis.
type CompletionKind string

const (
	// CompletionKindRouted is a client request that reached routing.
	CompletionKindRouted CompletionKind = "routed"
	// CompletionKindRejected is a client request refused before any provider was
	// chosen, by quota or admission control. It has no route and no usage.
	CompletionKindRejected CompletionKind = "rejected"
	// CompletionKindPlayground is admin console traffic.
	CompletionKindPlayground CompletionKind = "playground"
)

// GatewayCallCompletion is one finished gateway call, described once for every
// consumer of that event: quota persistence, request logs, metrics and tracing.
//
// It deliberately holds no *http.Request. Client attribution is captured as plain
// values because a completion can outlive its request — image jobs finish on a
// worker goroutine long after the client connection is gone.
type GatewayCallCompletion struct {
	Kind     CompletionKind
	Call     CallContext
	Route    RouteSelection
	Usage    Usage
	Attempts []RouteAttempt

	StatusCode int
	ErrorCode  string
	// ErrorMessage is the client-facing message for the failure. It is treated as
	// payload rather than as a label: upstream errors can embed response bodies,
	// URLs and account identifiers.
	ErrorMessage string

	ClientIP  string
	UserAgent string

	// FinishedAt is when the gateway considered the call complete. It is stamped at
	// the completion boundary rather than where a span is built, so queueing delay in
	// the exporter never inflates the reported latency.
	FinishedAt time.Time

	// RequestPayload and ResponsePayload are the raw bodies handed in by a handler.
	// They are rendered once at the completion boundary and then cleared, so nothing
	// downstream holds a reference to a multi-megabyte request graph.
	RequestPayload  any
	ResponsePayload any

	// RequestBody and ResponseBody are the redacted, truncated renderings of the
	// payloads above: the exact text written to the payload log. Tracing reuses them
	// rather than re-rendering, which both removes a duplicate marshal of the whole
	// body and bounds what an export queue can retain.
	//
	// They are left empty when payload capture is off, so a payload that must not
	// leave the process is never even queued.
	RequestBody  string
	ResponseBody string
}

// TraceEmitter receives finished gateway calls.
//
// Implementations must not block: EmitGatewayCall runs on the request goroutine,
// for non-streaming handlers even before the response has been written, so any
// queueing, batching or network work belongs behind it rather than inside it.
type TraceEmitter interface {
	EmitGatewayCall(completion GatewayCallCompletion)
	Shutdown(ctx context.Context) error
}

// finishCall records one finished gateway call everywhere it needs to be recorded.
//
// Having a single funnel is what makes tracing correct rather than merely
// convenient: Langfuse v4 creates a duplicate observation for a re-ingested span ID,
// so "exactly once per call" is a correctness requirement and not a tidiness one.
func (s *Server) finishCall(completion GatewayCallCompletion) {
	if completion.Kind == "" {
		completion.Kind = CompletionKindRouted
	}
	if completion.FinishedAt.IsZero() {
		completion.FinishedAt = tracingFinishedAt()
	}
	// Price here rather than leaving it to the store. priceUsage runs inside
	// FinishCall but its result never leaves that function, so anything observing
	// the caller's Usage afterwards would report a zero cost. priceUsage is pure and
	// idempotent, which makes the store's own call a no-op rather than a conflict.
	completion.Usage = priceUsage(completion.Call.Model, completion.Usage)
	// Thread the attempt outcomes into the call context so the single metrics
	// observation point can report per-candidate counts and upstream latency.
	// Only the bounded outcome fields are consumed there, so the upstream error
	// text stays out of this path the same way it stays out of traces.
	completion.Call.RouteAttempts = attemptsWithoutErrorText(completion.Attempts)
	s.store.RecordRouteAttempts(completion.Call.RequestID, completion.Attempts)
	switch completion.Kind {
	case CompletionKindPlayground:
		s.store.RecordPlaygroundRequest(completion.Call, completion.Route, completion.StatusCode, completion.ErrorCode, completion.ClientIP, completion.UserAgent)
	case CompletionKindRejected:
		// RecordRejectedRequest already ran. It mints the request ID the caller has to
		// echo back to the client, so it cannot be deferred to here.
	default:
		s.store.FinishCall(completion.Call, completion.Route, completion.Usage, completion.StatusCode, completion.ErrorCode, completion.ClientIP, completion.UserAgent)
	}
	// Rendered once here and shared. recordRequestPayload used to marshal, redact and
	// re-marshal the whole body on its own; tracing doing the same again would double
	// that cost and, worse, keep the raw graph alive for as long as an export queue
	// held the completion.
	requestBody, requestTruncated := auditPayloadBody(completion.RequestPayload)
	responseBody, responseTruncated := auditPayloadBody(completion.ResponsePayload)
	s.store.RecordRequestPayload(completion.Call.RequestID, requestBody, requestTruncated, responseBody, responseTruncated)
	completion.RequestPayload, completion.ResponsePayload = nil, nil
	if s.config.TracingCapturePayloads {
		completion.RequestBody = requestBody
		completion.ResponseBody = responseBody
	} else {
		// Cleared rather than filtered later: content that must not leave the process
		// should not sit in an export queue in the first place. Upstream error text is
		// payload for the same reason the bodies are — it can embed a response body, a
		// URL or an account identifier.
		completion.ErrorMessage = ""
		completion.Attempts = attemptsWithoutErrorText(completion.Attempts)
	}
	s.emitGatewayTrace(completion)
}

// finishRoutedCall completes a call while its request is still in hand, taking the
// client attribution from it.
func (s *Server) finishRoutedCall(r *http.Request, completion GatewayCallCompletion) {
	completion.ClientIP = s.clientIP(r)
	completion.UserAgent = r.UserAgent()
	s.finishCall(completion)
}

// finishSuccessfulRoutedCall completes a call that reached a provider and came back
// with a response. It is the shape almost every handler needs, so it exists to keep
// those handlers reading as one statement each.
func (s *Server) finishSuccessfulRoutedCall(r *http.Request, routed RoutedCall, route RouteSelection, usage Usage, attempts []RouteAttempt, requestPayload any, responsePayload any) {
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      http.StatusOK,
		RequestPayload:  requestPayload,
		ResponsePayload: responsePayload,
	})
}

// finishFailedRoutedCall completes a call whose routing never produced a usable
// response. The reported route is the last candidate attempted, so the failure is
// attributed to something rather than to nothing.
func (s *Server) finishFailedRoutedCall(r *http.Request, routed RoutedCall, attempts []RouteAttempt, usage Usage, err error, requestPayload any) {
	httpErr := AsHTTPError(err)
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           lastAttemptRoute(attempts),
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      httpErr.Status,
		ErrorCode:       httpErr.Code,
		ErrorMessage:    httpErr.Message,
		RequestPayload:  requestPayload,
		ResponsePayload: auditErrorPayload(err, routed.Call.RequestID),
	})
}

// finishRejectedCall records a request refused before routing and returns the
// request ID minted for it, which the caller echoes back to the client.
//
// admittedAt is when the request was accepted for routing, so the span covers the
// admission work that led to the refusal instead of collapsing to zero width.
func (s *Server) finishRejectedCall(r *http.Request, admittedAt time.Time, project Project, key APIKey, model string, stream bool, err error, requestPayload any) string {
	httpErr := AsHTTPError(err)
	requestID := s.store.RecordRejectedRequest(project, key, model, stream, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
	s.finishRoutedCall(r, GatewayCallCompletion{
		Kind: CompletionKindRejected,
		Call: CallContext{
			RequestID: requestID,
			Project:   project,
			Key:       key,
			// The model name is unvalidated client input here — this path is reached
			// precisely when a request is refused, including for naming a model that
			// does not exist. It is safe as a span attribute, which has no series
			// cardinality, but must never become a metric label.
			Model:     Model{Name: model},
			Stream:    stream,
			StartedAt: admittedAt,
		},
		StatusCode:      httpErr.Status,
		ErrorCode:       httpErr.Code,
		ErrorMessage:    httpErr.Message,
		RequestPayload:  requestPayload,
		ResponsePayload: auditErrorPayload(err, requestID),
	})
	return requestID
}

// attemptsWithoutErrorText copies the attempts with their upstream error text
// removed. It copies rather than editing in place because the caller's slice has
// already been persisted with the full text, which stays inside TokenHub.
func attemptsWithoutErrorText(attempts []RouteAttempt) []RouteAttempt {
	if len(attempts) == 0 {
		return attempts
	}
	stripped := make([]RouteAttempt, len(attempts))
	copy(stripped, attempts)
	for index := range stripped {
		stripped[index].Error = ""
	}
	return stripped
}

// errorMessageOrEmpty returns the client-facing message for err, or the empty string
// when the call succeeded.
func errorMessageOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return AsHTTPError(err).Message
}

// emitGatewayTrace reports a completion to the configured trace emitter. A nil
// emitter is the off state, which is the default.
func (s *Server) emitGatewayTrace(completion GatewayCallCompletion) {
	if s.traceEmitter == nil {
		return
	}
	s.traceEmitter.EmitGatewayCall(completion)
}
