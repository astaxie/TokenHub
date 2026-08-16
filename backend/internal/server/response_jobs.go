package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

var errResponseJobCancelled = errors.New("response job cancelled")

type responseJobEnvelope struct {
	Request   json.RawMessage `json:"request"`
	Headers   http.Header     `json:"headers,omitempty"`
	ClientIP  string          `json:"client_ip,omitempty"`
	UserAgent string          `json:"user_agent,omitempty"`
}

func (s *Server) handleResponseJob(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/responses/compact" {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	id, cancel, ok := responseJobPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
		return
	}
	if !cancel && r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if cancel && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	s.serveResponseJob(w, r, id, cancel)
}

func (s *Server) handleResponseJobGet(w http.ResponseWriter, r *http.Request) {
	id, cancel, ok := responseJobPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
		return
	}
	if cancel {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	s.serveResponseJob(w, r, id, false)
}

func responseJobGetHeadFallback(w http.ResponseWriter, r *http.Request) {
	_, cancel, ok := responseJobPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
		return
	}
	if cancel {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	jsonMethodNotAllowed(http.MethodGet)(w, r)
}

func (s *Server) handleResponseJobCancel(w http.ResponseWriter, r *http.Request) {
	id, cancel, ok := responseJobPath(r)
	if !ok || !cancel {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
		return
	}
	s.serveResponseJob(w, r, id, true)
}

func responseJobPath(r *http.Request) (string, bool, bool) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/responses/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "cancel") {
		return "", false, false
	}
	return parts[0], len(parts) == 2, true
}

func (s *Server) serveResponseJob(w http.ResponseWriter, r *http.Request, id string, cancel bool) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	expired, _ := s.store.ExpireResponseJobs()
	s.metrics.ObserveResponseJobTerminalCount(responseJobStatusExpired, "response_expired", expired)
	job, ok := s.authorizedResponseJob(project, key, id)
	if !ok || job.Status == responseJobStatusExpired {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
		return
	}
	if cancel {
		wasTerminal := responseJobTerminal(job.Status)
		resultTTL := time.Duration(s.config.ResponseResultTTLSeconds) * time.Second
		job, ok, err = s.store.CancelResponseJob(job.ID, "api_key:"+key.ID, resultTTL)
		if err != nil {
			writeError(w, r, NewHTTPError(http.StatusInternalServerError, "response_cancel_failed", "Response job could not be cancelled"))
			return
		}
		if !ok {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "response_not_found", "Response job not found"))
			return
		}
		if !wasTerminal && job.Status == responseJobStatusCancelled {
			s.metrics.ObserveResponseJobTerminal(job.Status, job.ErrorCode, job.StartedAt, job.CompletedAt)
		}
		s.observeResponseQueueDepth()
	}
	if err := s.writeResponseJob(w, r, job); err != nil {
		writeError(w, r, err)
	}
}

func (s *Server) authorizedResponseJob(project Project, key APIKey, id string) (ResponseJob, bool) {
	job, ok, err := s.store.GetResponseJob(id)
	if err != nil || !ok {
		return ResponseJob{}, false
	}
	if job.ProjectID != project.ID || job.APIKeyID != key.ID {
		return ResponseJob{}, false
	}
	if job.AttributedUserID != usageAttributionUserID(key, project) {
		return ResponseJob{}, false
	}
	if !keyCanAccessModel(s.store, key, job.Model) {
		return ResponseJob{}, false
	}
	return job, true
}

func (s *Server) writeResponseJob(w http.ResponseWriter, r *http.Request, job ResponseJob) error {
	if job.Status == responseJobStatusSucceeded {
		_, resultJSON, err := s.store.LoadResponseJobPayload(job.ID)
		if err != nil {
			return NewHTTPError(http.StatusInternalServerError, "response_result_unreadable", "Response result could not be read")
		}
		job.ResultJSON = resultJSON
	}
	w.Header().Set("x-request-id", firstNonEmpty(job.RequestID, job.ID))
	writeJSON(w, http.StatusOK, responseJobAPIObject(job))
	return nil
}

func responseJobAPIObject(job ResponseJob) map[string]any {
	if job.Status == responseJobStatusSucceeded && len(job.ResultJSON) > 0 {
		var result map[string]any
		if json.Unmarshal(job.ResultJSON, &result) == nil && result != nil {
			result["id"] = job.ID
			result["object"] = "response"
			result["status"] = "completed"
			result["background"] = true
			return result
		}
	}
	status := map[string]string{
		responseJobStatusQueued:    "queued",
		responseJobStatusRunning:   "in_progress",
		responseJobStatusSucceeded: "completed",
		responseJobStatusFailed:    "failed",
		responseJobStatusCancelled: "cancelled",
	}[job.Status]
	if job.CancelRequestedAt != nil && job.Status == responseJobStatusRunning {
		status = "cancelled"
		job.ErrorCode = "response_cancelled"
		job.ErrorMessage = "Response job was cancelled"
	}
	response := map[string]any{
		"id":         job.ID,
		"object":     "response",
		"created_at": job.CreatedAt.Unix(),
		"status":     status,
		"background": true,
		"model":      job.Model,
		"output":     []any{},
		"error":      nil,
	}
	if job.CompletedAt != nil {
		response["completed_at"] = job.CompletedAt.Unix()
	}
	if job.ErrorCode != "" {
		response["error"] = map[string]any{"code": job.ErrorCode, "message": job.ErrorMessage}
	}
	return response
}

func responseJobRequestHeaders(incoming http.Header) http.Header {
	allowed := append([]string{}, codexRequestHeaderAllowlist...)
	allowed = append(allowed, "user-agent", "version", "originator", "openai-beta", "x-tokenhub-session-id")
	result := make(http.Header)
	for _, name := range allowed {
		for _, value := range incoming.Values(name) {
			if value = boundedHeaderValue(value); value != "" {
				result.Add(name, value)
			}
		}
	}
	return result
}

func responseJobExecutionRequest(request ResponsesRequest) ResponsesRequest {
	request.Stream = false
	request.Background = false
	if request.raw != nil {
		request.raw = cloneRawJSON(request.raw, len(request.raw))
		delete(request.raw, "stream")
		delete(request.raw, "background")
	}
	return request
}

func (s *Server) submitResponseJob(w http.ResponseWriter, r *http.Request, project Project, key APIKey, request ResponsesRequest) {
	if request.Stream {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "background_stream_not_supported", "Background Responses do not support stream=true"))
		return
	}
	if !keyCanAccessModel(s.store, key, request.Model) {
		writeError(w, r, ErrModelNotAllowed)
		return
	}
	queued, err := s.store.CountOutstandingResponseJobs()
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "response_queue_unavailable", "Response queue is unavailable"))
		return
	}
	if queued >= int64(s.config.ResponseMaxQueuedJobs) {
		writeError(w, r, NewHTTPError(http.StatusServiceUnavailable, "response_queue_full", "Response queue is full"))
		return
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", "Response request could not be serialized"))
		return
	}
	envelopeJSON, err := json.Marshal(responseJobEnvelope{
		Request:   requestJSON,
		Headers:   responseJobRequestHeaders(r.Header),
		ClientIP:  s.clientIP(r),
		UserAgent: boundedHeaderValue(r.UserAgent()),
	})
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", "Response request could not be serialized"))
		return
	}
	job, err := s.store.CreateResponseJob(ResponseJob{
		ID:               NewID("resp"),
		ProjectID:        project.ID,
		APIKeyID:         key.ID,
		AttributedUserID: usageAttributionUserID(key, project),
		Status:           responseJobStatusQueued,
		Phase:            responseJobPhaseQueued,
		Model:            request.Model,
	}, envelopeJSON)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "response_job_create_failed", "Response job could not be persisted"))
		return
	}
	s.observeResponseQueueDepth()
	w.Header().Set("x-request-id", job.ID)
	writeJSON(w, http.StatusOK, responseJobAPIObject(job))
	s.startResponseWorkers()
}

func (s *Server) startResponseWorkers() {
	s.responseWorkerStart.Do(func() {
		for index := 0; index < s.config.ResponseWorkerConcurrency; index++ {
			s.responseWorkerGroup.Add(1)
			go s.responseWorker(index)
		}
	})
}

func (s *Server) responseWorker(index int) {
	defer s.responseWorkerGroup.Done()
	poll := time.Duration(s.config.ResponsePollIntervalMillis) * time.Millisecond
	leaseTTL := time.Duration(s.config.ResponseLeaseTTLSeconds) * time.Second
	resultTTL := time.Duration(s.config.ResponseResultTTLSeconds) * time.Second
	owner := s.responseInstanceID + ":" + NewID("slot")
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-s.responseContext.Done():
			return
		case <-timer.C:
		}
		requeued, failed, cancelled, err := s.store.RecoverResponseJobs(resultTTL)
		if err != nil {
			log.Printf("[tokenhub] response job recovery failed worker=%d: %v", index, err)
		}
		s.metrics.ObserveResponseJobRecovery("requeued", requeued)
		s.metrics.ObserveResponseJobRecovery("failed_unsafe", failed)
		s.metrics.ObserveResponseJobRecovery("cancelled_unsafe", cancelled)
		s.metrics.ObserveResponseJobTerminalCount(responseJobStatusFailed, "response_execution_lost", failed)
		s.metrics.ObserveResponseJobTerminalCount(responseJobStatusCancelled, "response_cancelled_worker_lost", cancelled)
		expired, err := s.store.ExpireResponseJobs()
		if err != nil {
			log.Printf("[tokenhub] response job expiry failed worker=%d: %v", index, err)
		}
		s.metrics.ObserveResponseJobTerminalCount(responseJobStatusExpired, "response_expired", expired)
		select {
		case <-s.responseContext.Done():
			return
		default:
		}
		job, claimed, err := s.store.ClaimResponseJob(owner, leaseTTL, resultTTL)
		if err != nil {
			log.Printf("[tokenhub] response job claim failed worker=%d: %v", index, err)
			timer.Reset(poll)
			continue
		}
		if !claimed {
			s.observeResponseQueueDepth()
			timer.Reset(poll)
			continue
		}
		if job.StartedAt != nil {
			s.metrics.ObserveResponseJobClaim(job.CreatedAt, *job.StartedAt)
		}
		s.observeResponseQueueDepth()
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		s.processResponseJob(job, owner, leaseTTL, resultTTL)
		timer.Reset(0)
	}
}

func (s *Server) processResponseJob(job ResponseJob, owner string, leaseTTL time.Duration, resultTTL time.Duration) {
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}
	var envelope responseJobEnvelope
	if err := json.Unmarshal(job.RequestJSON, &envelope); err != nil {
		s.finalizeResponseJob(job, owner, CallContext{}, RouteSelection{}, Usage{}, nil, http.StatusInternalServerError, "response_payload_invalid", "Persisted response request is invalid", nil, resultTTL)
		return
	}
	job.ClientIP = envelope.ClientIP
	job.UserAgent = envelope.UserAgent
	var request ResponsesRequest
	if err := json.Unmarshal(envelope.Request, &request); err != nil {
		s.finalizeResponseJob(job, owner, CallContext{}, RouteSelection{}, Usage{}, nil, http.StatusInternalServerError, "response_payload_invalid", "Persisted response request is invalid", nil, resultTTL)
		return
	}
	request = responseJobExecutionRequest(request)

	heartbeat := startLeaseHeartbeat(s.responseContext, leaseTTL, leaseTTL, func(context.Context) (time.Duration, bool, error) {
		return s.store.RenewResponseJobLease(job.ID, owner, job.LeaseEpoch, leaseTTL)
	})
	watchContext, stopWatch := context.WithCancelCause(heartbeat.ctx)
	watchDone := make(chan struct{})
	go s.watchResponseJobCancellation(watchContext, stopWatch, watchDone, job.ID)
	defer func() {
		stopWatch(context.Canceled)
		<-watchDone
		_ = stopLeaseHeartbeat(heartbeat)
	}()
	ctx, cancel := context.WithTimeout(watchContext, time.Duration(s.config.ResponseJobTimeoutSeconds)*time.Second)
	defer cancel()
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}

	project, projectOK := s.store.GetProject(job.ProjectID)
	key, keyOK := responseJobAPIKey(s.store, job.ProjectID, job.APIKeyID)
	if !projectOK || !keyOK || usageAttributionUserID(key, project) != job.AttributedUserID {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		s.finalizeResponseJob(job, owner, CallContext{}, RouteSelection{}, Usage{}, nil, http.StatusUnauthorized, "response_authorization_lost", "Response job authorization is no longer valid", request, resultTTL)
		return
	}
	if !responseJobAuthorizationValid(project, key, envelope.ClientIP) {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		rejectedAt := time.Now()
		requestID := s.store.RecordRejectedRequest(project, key, request.Model, false, http.StatusUnauthorized, "response_authorization_lost", envelope.ClientIP, envelope.UserAgent)
		rejectedCall := CallContext{RequestID: requestID, Project: project, Key: key, Model: Model{Name: request.Model}, StartedAt: rejectedAt, measuredAt: rejectedAt}
		s.finalizeResponseJob(job, owner, rejectedCall, RouteSelection{}, Usage{}, nil, http.StatusUnauthorized, "response_authorization_lost", "Response job authorization is no longer valid", request, resultTTL)
		return
	}
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}
	call, retained, err := s.store.AdmitResponseJob(ctx, job.ID, owner, job.LeaseEpoch, key, request.Model, requestTokenReservation(request))
	if err != nil {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		httpErr := AsHTTPError(err)
		rejectedAt := time.Now()
		requestID := s.store.RecordRejectedRequest(project, key, request.Model, false, httpErr.Status, httpErr.Code, envelope.ClientIP, envelope.UserAgent)
		rejectedCall := CallContext{RequestID: requestID, Project: project, Key: key, Model: Model{Name: request.Model}, StartedAt: rejectedAt, measuredAt: rejectedAt}
		s.finalizeResponseJob(job, owner, rejectedCall, RouteSelection{}, Usage{}, nil, httpErr.Status, httpErr.Code, httpErr.Message, request, resultTTL)
		return
	}
	if !retained {
		return
	}
	call.Stream = false
	job.RequestID = call.RequestID
	// The admitted call context is cancelled if the API-key concurrency lease can
	// no longer be renewed. All expensive work and the provider invocation must
	// inherit it, otherwise another replica could admit overlapping work after the
	// lease expires. Cleanup is safe after normal finalization and essential on
	// every early-return path before durable settlement.
	defer s.store.ReleaseResponseJobAdmission(call.RequestID)
	if call.requestContext != nil {
		ctx = call.requestContext
	}
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}

	decision, err := s.evaluateOutboundGuardrails(ctx, call.Project.ID, responsesGuardrailTargets(&request))
	auditPayload := guardrailRequestAuditPayload(request.Model, decision, request)
	if err != nil {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		httpErr := AsHTTPError(err)
		s.finalizeResponseJob(job, owner, call, RouteSelection{}, Usage{}, nil, httpErr.Status, httpErr.Code, httpErr.Message, auditPayload, resultTTL)
		return
	}
	routed, err := s.prepareAdmittedRoutedCall(ctx, call, request.Model)
	if err != nil {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		httpErr := AsHTTPError(err)
		s.finalizeResponseJob(job, owner, routed.Call, RouteSelection{}, Usage{}, nil, httpErr.Status, httpErr.Code, httpErr.Message, auditPayload, resultTTL)
		return
	}
	routed.Routes = s.routesWithAdapterCapability(routed.Routes, AdapterCapabilityResponses)
	if len(routed.Routes) == 0 {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		s.finalizeResponseJob(job, owner, routed.Call, RouteSelection{}, Usage{}, nil, http.StatusNotImplemented, "provider_capability_not_supported", "Responses are not supported", auditPayload, resultTTL)
		return
	}
	if err := s.applyResponseJobAffinity(&routed, key, envelope.Headers, request); err != nil {
		if s.stopResponseJobForShutdown(job, owner, resultTTL) {
			return
		}
		httpErr := AsHTTPError(err)
		s.finalizeResponseJob(job, owner, routed.Call, RouteSelection{}, Usage{}, nil, httpErr.Status, httpErr.Code, httpErr.Message, auditPayload, resultTTL)
		return
	}
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}
	retained, err = s.store.MarkResponseJobPhase(job.ID, owner, job.LeaseEpoch, responseJobPhaseDispatched, call.RequestID)
	if err != nil || !retained {
		// A failed dispatch CAS means this worker no longer has confirmed authority
		// to settle the call. Recovery or the cancellation owner will perform the
		// single durable settlement; calling the generic FinishCall path here would
		// duplicate quota reconciliation and plaintext payload auditing.
		return
	}
	response, route, usage, attempts, invokeErr := s.executeRoutedResponsesContext(ctx, envelope.Headers, routed, request)
	if invokeErr == nil {
		resultJSON, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			s.finalizeResponseJob(job, owner, routed.Call, route, usage, attempts, http.StatusInternalServerError, "response_result_invalid", "Response result could not be serialized", auditPayload, resultTTL)
			return
		}
		settled := s.finalizeResponseJob(job, owner, routed.Call, route, usage, attempts, http.StatusOK, "", "", auditPayload, resultTTL, resultJSON)
		if settled {
			s.store.MarkRouteUsed(route.Route.ID)
			s.store.MarkProviderResourceUsed(routeResourceID(route))
		}
		return
	}

	current, _, _ := s.store.GetResponseJob(job.ID)
	if s.stopResponseJobForShutdown(job, owner, resultTTL) {
		return
	}
	if current.Status == responseJobStatusCancelled || errors.Is(context.Cause(watchContext), errResponseJobCancelled) {
		s.finalizeResponseJob(job, owner, routed.Call, lastAttemptRoute(attempts), usage, attempts, 499, "response_cancelled", "Response job was cancelled", auditPayload, resultTTL)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.finalizeResponseJob(job, owner, routed.Call, lastAttemptRoute(attempts), usage, attempts, http.StatusGatewayTimeout, "response_job_timeout", "Response job timed out", auditPayload, resultTTL)
		return
	}
	if errors.Is(context.Cause(ctx), ErrCoordinationLeaseLost) || errors.Is(context.Cause(heartbeat.ctx), ErrCoordinationLeaseLost) {
		s.finalizeResponseJob(job, owner, routed.Call, lastAttemptRoute(attempts), usage, attempts, http.StatusInternalServerError, "response_execution_lost", "Background response execution lost coordination ownership", auditPayload, resultTTL)
		return
	}
	httpErr := AsHTTPError(invokeErr)
	s.finalizeResponseJob(job, owner, routed.Call, lastAttemptRoute(attempts), usage, attempts, httpErr.Status, httpErr.Code, httpErr.Message, auditPayload, resultTTL)
}

func (s *Server) stopResponseJobForShutdown(job ResponseJob, owner string, resultTTL time.Duration) bool {
	if s.responseContext.Err() == nil {
		return false
	}
	status, retained, err := s.store.ShutdownResponseJob(job.ID, owner, job.LeaseEpoch, resultTTL)
	if err != nil {
		log.Printf("[tokenhub] failed to settle response job during shutdown id=%s: %v", job.ID, err)
		return true
	}
	if !retained {
		return true
	}
	switch status {
	case responseJobStatusQueued:
		s.metrics.ObserveResponseJobRecovery("shutdown_requeued", 1)
	case responseJobStatusFailed:
		s.metrics.ObserveResponseJobTerminalCount(responseJobStatusFailed, "response_execution_lost", 1)
	case responseJobStatusCancelled:
		s.metrics.ObserveResponseJobTerminalCount(responseJobStatusCancelled, "response_cancelled_worker_lost", 1)
	}
	s.observeResponseQueueDepth()
	return true
}

func responseJobAPIKey(store Store, projectID string, keyID string) (APIKey, bool) {
	for _, key := range store.ListProjectKeys(projectID) {
		if key.ID == keyID {
			return key, true
		}
	}
	return APIKey{}, false
}

func responseJobAuthorizationValid(project Project, key APIKey, clientIP string) bool {
	if project.Status != StatusActive {
		return false
	}
	now := time.Now().UTC()
	if key.Status == StatusDisabled {
		return false
	}
	if key.Status == StatusRevoked && (key.GraceUntil == nil || !now.Before(*key.GraceUntil)) {
		return false
	}
	if key.ExpiresAt != nil && now.After(*key.ExpiresAt) {
		return false
	}
	return len(key.IPAllowlist) == 0 || ipAllowed(clientIP, key.IPAllowlist)
}

func (s *Server) watchResponseJobCancellation(ctx context.Context, cancel context.CancelCauseFunc, done chan<- struct{}, id string) {
	defer close(done)
	interval := time.Duration(s.config.ResponsePollIntervalMillis) * time.Millisecond
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, ok, err := s.store.GetResponseJob(id)
			if err == nil && ok && (job.Status == responseJobStatusCancelled || job.CancelRequestedAt != nil) {
				cancel(errResponseJobCancelled)
				return
			}
		}
	}
}

func (s *Server) applyResponseJobAffinity(routed *RoutedCall, key APIKey, headers http.Header, request ResponsesRequest) error {
	affinity, err := resolveCodexSessionAffinity(s.config.SecretKey, key.ID, headers, request)
	if err != nil {
		return err
	}
	if affinity != nil && routesContainAdapterType(routed.Routes, ProviderOpenAICodex) {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
		return nil
	}
	affinity, err = s.responsesCacheLocalityAffinity(key.ID, headers, request)
	if err != nil {
		return err
	}
	if affinity != nil {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
	}
	return nil
}

func (s *Server) finalizeResponseJob(job ResponseJob, owner string, call CallContext, route RouteSelection, usage Usage, attempts []RouteAttempt, statusCode int, errorCode string, errorMessage string, requestPayload any, resultTTL time.Duration, resultJSON ...[]byte) bool {
	status := responseJobStatusSucceeded
	if statusCode == 499 || errorCode == "response_cancelled" {
		status = responseJobStatusCancelled
	} else if statusCode < 200 || statusCode >= 300 || errorCode != "" {
		status = responseJobStatusFailed
	}
	var payload []byte
	if len(resultJSON) > 0 {
		payload = resultJSON[0]
	}
	errorMessage = responseJobSafeErrorMessage(errorCode, errorMessage)
	call.RouteAttempts = attemptsWithoutErrorText(attempts)
	finishedJob, settled, err := s.store.FinalizeResponseJob(call, job.ID, owner, job.LeaseEpoch, status, payload, route, usage, statusCode, errorCode, errorMessage, responseJobClientIP(job, requestPayload), responseJobUserAgent(job, requestPayload), resultTTL)
	if err != nil {
		log.Printf("[tokenhub] failed to finalize response job id=%s: %v", job.ID, err)
		return false
	}
	if !settled {
		return false
	}
	if finishedJob.Status == responseJobStatusCancelled {
		statusCode = 499
		errorCode = "response_cancelled"
		errorMessage = "Response job was cancelled"
	}
	s.metrics.ObserveResponseJobTerminal(finishedJob.Status, finishedJob.ErrorCode, finishedJob.StartedAt, finishedJob.CompletedAt)
	s.observeResponseQueueDepth()
	if call.RequestID == "" {
		return true
	}
	// Background request and response bodies have a bounded encrypted retention
	// policy on ResponseJob. Never duplicate them into the plaintext audit tables
	// or trace exporter, where that retention policy cannot be enforced. Route
	// attempts retain operational metadata but not raw upstream error text.
	safeAttempts := attemptsWithoutErrorText(attempts)
	s.store.RecordRouteAttempts(call.RequestID, safeAttempts)
	completion := GatewayCallCompletion{
		Call:         call,
		Route:        route,
		Usage:        priceUsage(call.Model, usage),
		Attempts:     safeAttempts,
		StatusCode:   statusCode,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		FinishedAt:   tracingFinishedAt(),
	}
	s.emitGatewayTrace(completion)
	return true
}

func responseJobSafeErrorMessage(code string, message string) string {
	if code == "" {
		return ""
	}
	switch code {
	case "response_cancelled", "response_job_timeout", "response_payload_invalid", "response_result_invalid",
		"response_authorization_lost", "provider_capability_not_supported", "response_execution_lost",
		"response_cancelled_worker_lost",
		"rate_limit_exceeded", "quota_exceeded", "budget_exceeded", "model_not_allowed":
		return message
	default:
		return "Background response execution failed"
	}
}

// Client attribution lives inside the encrypted envelope and is copied only into
// transient worker memory. It is never persisted in a plaintext job column.
func responseJobClientIP(job ResponseJob, _ any) string  { return job.ClientIP }
func responseJobUserAgent(job ResponseJob, _ any) string { return job.UserAgent }

func (s *Server) observeResponseQueueDepth() {
	if s.metrics == nil {
		return
	}
	depth, err := s.store.CountQueuedResponseJobs()
	if err == nil {
		s.metrics.SetResponseQueueDepth(depth)
	}
}
