package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const (
	imageJobStatusQueued     = "queued"
	imageJobStatusRunning    = "running"
	imageJobStatusCompleted  = "completed"
	imageJobStatusFailed     = "failed"
	imageDownloadTTL         = 24 * time.Hour
	maxGeneratedImageBytes   = 64 << 20
	maxImageEditRequestBytes = 128 << 20
	maxInputImageBytes       = 50 << 20
	maxImageTextFieldBytes   = 1 << 20
	codexImageModelName      = "codex-gpt-image-2"
	openAIImageModelName     = "gpt-image-2"
)

type imageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

type uploadedImage struct {
	role string
	data []byte
}

type imageJobWork struct {
	job       ImageJob
	call      CallContext
	clientIP  string
	userAgent string
	done      chan struct{}
}

type imageRunResult struct {
	data          []byte
	revisedPrompt string
}

func defaultImageStorageDir() string {
	if pathExists("backend/data") {
		return "backend/data/images"
	}
	return "data/images"
}

func (s *Server) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request imageGenerationRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	originator := strings.ToLower(strings.TrimSpace(r.Header.Get("originator")))
	if strings.TrimSpace(request.Model) == openAIImageModelName &&
		(strings.TrimSpace(r.Header.Get("x-codex-image-turn-id")) != "" || strings.HasPrefix(originator, "codex")) {
		request.Model = codexImageModelName
		request.ResponseFormat = "b64_json"
	}
	if err := normalizeImageGenerationRequest(&request); err != nil {
		writeError(w, r, err)
		return
	}
	job, call, atomicAdmission, ok, err := s.createImageJobForRequest(w, r, project, key, request, ImageJob{
		ProjectID: project.ID,
		APIKeyID:  key.ID,
		Status:    imageJobStatusQueued,
		Model:     request.Model,
		Action:    "generate",
		Quality:   request.Quality,
		Size:      request.Size,
	}, request.Prompt)
	if !ok {
		return
	}
	if err != nil {
		if call.RequestID != "" && !atomicAdmission {
			s.store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusInternalServerError, "image_job_create_failed", s.clientIP(r), r.UserAgent())
		}
		httpErr := AsHTTPError(err)
		if httpErr.Code == "internal_error" {
			httpErr = NewHTTPError(http.StatusInternalServerError, "image_job_create_failed", err.Error())
		}
		requestID := call.RequestID
		if requestID == "" {
			requestID = s.store.RecordRejectedRequest(project, key, request.Model, false, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		}
		w.Header().Set("x-request-id", requestID)
		s.recordRequestPayload(requestID, imageAuditRequest(ImageJob{RequestID: requestID, Model: request.Model, Action: "generate", Quality: request.Quality, Size: request.Size}), auditErrorPayload(err, requestID))
		writeError(w, r, httpErr)
		return
	}
	work := imageJobWork{
		job:       job,
		call:      call,
		clientIP:  s.clientIP(r),
		userAgent: r.UserAgent(),
	}
	if !prefersAsyncImageResponse(r) {
		work.done = make(chan struct{})
	}
	if err := s.enqueueImageJob(work); err != nil {
		httpErr := AsHTTPError(err)
		s.store.FinishCall(call, RouteSelection{}, Usage{}, httpErr.Status, httpErr.Code, work.clientIP, work.userAgent)
		s.failImageJob(job, httpErr.Code, httpErr.Message)
		s.recordRequestPayload(call.RequestID, imageAuditRequest(job), auditErrorPayload(err, call.RequestID))
		writeError(w, r, err)
		return
	}
	if work.done == nil {
		w.Header().Set("location", "/v1/image-jobs/"+job.ID)
		w.Header().Set("x-request-id", call.RequestID)
		writeJSON(w, http.StatusAccepted, s.imageJobResponse(r, job))
		return
	}

	<-work.done
	job, _ = s.store.GetImageJob(job.ID)
	if job.Status != imageJobStatusCompleted {
		writeError(w, r, NewHTTPError(imageJobErrorStatus(job.ErrorCode), firstNonEmpty(job.ErrorCode, "image_generation_failed"), firstNonEmpty(job.ErrorMessage, "Image generation failed")))
		return
	}
	w.Header().Set("x-request-id", job.RequestID)
	response, err := s.imageGenerationResponse(r, job, request.ResponseFormat)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "image_asset_read_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("content-type")), "multipart/form-data") {
		writeError(w, r, NewHTTPError(http.StatusUnsupportedMediaType, "invalid_content_type", "Image edits require multipart/form-data"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImageEditRequestBytes)
	multipartReader, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", err.Error()))
		return
	}
	fields := make(map[string]string)
	inputs := make([]uploadedImage, 0, 1)
	var mask *uploadedImage
	for {
		part, partErr := multipartReader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", partErr.Error()))
			return
		}
		name := part.FormName()
		switch name {
		case "image", "image[]":
			data, readErr := readUploadedImagePart(part)
			_ = part.Close()
			if readErr != nil {
				writeError(w, r, readErr)
				return
			}
			inputs = append(inputs, uploadedImage{role: "input", data: data})
		case "mask":
			data, readErr := readUploadedImagePart(part)
			_ = part.Close()
			if readErr != nil {
				writeError(w, r, readErr)
				return
			}
			if mask != nil {
				writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", "Only one mask is supported"))
				return
			}
			mask = &uploadedImage{role: "mask", data: data}
		default:
			data, readErr := io.ReadAll(io.LimitReader(part, maxImageTextFieldBytes+1))
			_ = part.Close()
			if readErr != nil || len(data) > maxImageTextFieldBytes {
				writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", "Multipart text field is too large or unreadable"))
				return
			}
			fields[name] = string(data)
		}
	}
	request := imageGenerationRequest{
		Model: fields["model"], Prompt: fields["prompt"], Quality: fields["quality"],
		Size: fields["size"], ResponseFormat: fields["response_format"],
	}
	if rawN := strings.TrimSpace(fields["n"]); rawN != "" {
		request.N, err = strconv.Atoi(rawN)
		if err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_image_count", "n must be an integer"))
			return
		}
	}
	if err := normalizeImageGenerationRequest(&request); err != nil {
		writeError(w, r, err)
		return
	}
	if len(inputs) == 0 {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "missing_image", "At least one image is required"))
		return
	}
	if mask != nil && request.Model == codexImageModelName {
		writeError(w, r, NewHTTPError(http.StatusNotImplemented, "image_mask_not_supported", "Masks are not supported through Codex subscription accounts; use reference-image editing without a mask"))
		return
	}
	if mask != nil {
		inputs = append(inputs, *mask)
	}
	job, call, atomicAdmission, ok, err := s.createImageJobForRequest(w, r, project, key, request, ImageJob{
		ProjectID: project.ID,
		APIKeyID:  key.ID,
		Status:    imageJobStatusQueued,
		Model:     request.Model,
		Action:    "edit",
		Quality:   request.Quality,
		Size:      request.Size,
	}, request.Prompt)
	if !ok {
		return
	}
	if err != nil {
		if call.RequestID != "" && !atomicAdmission {
			s.store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusInternalServerError, "image_job_create_failed", s.clientIP(r), r.UserAgent())
		}
		httpErr := AsHTTPError(err)
		if httpErr.Code == "internal_error" {
			httpErr = NewHTTPError(http.StatusInternalServerError, "image_job_create_failed", err.Error())
		}
		requestID := call.RequestID
		if requestID == "" {
			requestID = s.store.RecordRejectedRequest(project, key, request.Model, false, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		}
		w.Header().Set("x-request-id", requestID)
		s.recordRequestPayload(requestID, imageAuditRequest(ImageJob{RequestID: requestID, Model: request.Model, Action: "edit", Quality: request.Quality, Size: request.Size}), auditErrorPayload(err, requestID))
		writeError(w, r, httpErr)
		return
	}
	for index, input := range inputs {
		asset, saveErr := s.saveImageAsset(job, input.data, input.role, index+1)
		if saveErr == nil {
			_, saveErr = s.store.CreateImageAsset(asset)
		}
		if saveErr != nil {
			if asset.RelativePath != "" {
				if fullPath, pathErr := s.imageAssetPath(asset.RelativePath); pathErr == nil {
					_ = os.Remove(fullPath)
				}
			}
			s.failImageJob(job, "image_input_storage_failed", saveErr.Error())
			s.store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusInternalServerError, "image_input_storage_failed", s.clientIP(r), r.UserAgent())
			s.recordRequestPayload(call.RequestID, imageAuditRequest(job), auditErrorPayload(saveErr, call.RequestID))
			writeError(w, r, NewHTTPError(http.StatusInternalServerError, "image_input_storage_failed", saveErr.Error()))
			return
		}
	}
	work := imageJobWork{
		job:       job,
		call:      call,
		clientIP:  s.clientIP(r),
		userAgent: r.UserAgent(),
	}
	if !prefersAsyncImageResponse(r) {
		work.done = make(chan struct{})
	}
	if err := s.enqueueImageJob(work); err != nil {
		httpErr := AsHTTPError(err)
		s.store.FinishCall(call, RouteSelection{}, Usage{}, httpErr.Status, httpErr.Code, work.clientIP, work.userAgent)
		s.failImageJob(job, httpErr.Code, httpErr.Message)
		s.recordRequestPayload(call.RequestID, imageAuditRequest(job), auditErrorPayload(err, call.RequestID))
		writeError(w, r, err)
		return
	}
	if work.done == nil {
		w.Header().Set("location", "/v1/image-jobs/"+job.ID)
		w.Header().Set("x-request-id", call.RequestID)
		writeJSON(w, http.StatusAccepted, s.imageJobResponse(r, job))
		return
	}
	<-work.done
	job, _ = s.store.GetImageJob(job.ID)
	if job.Status != imageJobStatusCompleted {
		writeError(w, r, NewHTTPError(imageJobErrorStatus(job.ErrorCode), firstNonEmpty(job.ErrorCode, "image_generation_failed"), firstNonEmpty(job.ErrorMessage, "Image generation failed")))
		return
	}
	w.Header().Set("x-request-id", job.RequestID)
	response, err := s.imageGenerationResponse(r, job, request.ResponseFormat)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "image_asset_read_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleImageJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	s.handleImageJobGet(w, r)
}

func (s *Server) handleImageJobGet(w http.ResponseWriter, r *http.Request) {
	project, _, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/image-jobs/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "image_job_not_found", "Image job not found"))
		return
	}
	job, ok := s.store.GetImageJob(id)
	if !ok || job.ProjectID != project.ID {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "image_job_not_found", "Image job not found"))
		return
	}
	writeJSON(w, http.StatusOK, s.imageJobResponse(r, job))
}

func (s *Server) handleAdminImageJobs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 1000"))
			return
		}
		limit = parsed
	}
	query := ImageJobAuditQuery{Limit: limit, Global: s.canViewGlobalOperations(user)}
	if !query.Global {
		if normalizeAdminRole(user.Role) == "team_leader" {
			query.ProjectIDs = trueMapKeys(s.visibleProjectIDSet(user))
		}
		query.APIKeyIDs = trueMapKeys(s.visibleAPIKeyIDSet(user))
	}
	jobs := s.store.ListImageJobsForAudit(query)
	data := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		item := s.imageJobResponse(r, job)
		assets := s.store.ListImageAssets(job.ID)
		allAssets := make([]map[string]any, 0, len(assets))
		for _, asset := range assets {
			url, expires := s.imageAssetURL(r, asset)
			allAssets = append(allAssets, map[string]any{
				"asset_id": asset.ID, "role": asset.Role, "url": url, "url_expires_at": expires,
				"content_type": asset.ContentType, "bytes": asset.ByteSize, "sha256": asset.SHA256,
			})
		}
		item["assets"] = allAssets
		data = append(data, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleImageAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	s.handleImageAssetGet(w, r)
}

func (s *Server) handleImageAssetGet(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/image-assets/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "content" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "image_asset_not_found", "Image asset not found"))
		return
	}
	asset, ok := s.store.GetImageAsset(parts[0])
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "image_asset_not_found", "Image asset not found"))
		return
	}
	expires, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil || time.Now().Unix() > expires || !s.validImageAssetSignature(asset, expires, r.URL.Query().Get("signature")) {
		writeError(w, r, NewHTTPError(http.StatusForbidden, "image_url_expired", "Image URL is invalid or expired"))
		return
	}
	fullPath, err := s.imageAssetPath(asset.RelativePath)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "image_asset_path_invalid", err.Error()))
		return
	}
	file, err := os.Open(fullPath)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "image_asset_not_found", "Image asset not found"))
		return
	}
	defer file.Close()
	w.Header().Set("content-type", asset.ContentType)
	w.Header().Set("content-length", strconv.FormatInt(asset.ByteSize, 10))
	w.Header().Set("cache-control", "private, max-age=86400")
	w.Header().Set("content-disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (s *Server) startImageCall(w http.ResponseWriter, r *http.Request, project Project, key APIKey, request imageGenerationRequest) (CallContext, bool) {
	call, err := s.store.StartCall(s.imageContext, project, key, request.Model, EstimateTextTokens(request.Prompt))
	if err != nil {
		httpErr := AsHTTPError(err)
		requestID := s.store.RecordRejectedRequest(project, key, request.Model, false, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		w.Header().Set("x-request-id", requestID)
		s.recordRequestPayload(requestID, map[string]any{
			"model": request.Model, "action": "image", "quality": request.Quality, "size": request.Size,
		}, auditErrorPayload(err, requestID))
		writeError(w, r, err)
		return CallContext{}, false
	}
	w.Header().Set("x-request-id", call.RequestID)
	writeRateLimitHeaders(w.Header(), call.RateLimitHeaders)
	return call, true
}

func (s *Server) createImageJobForRequest(w http.ResponseWriter, r *http.Request, project Project, key APIKey, request imageGenerationRequest, job ImageJob, prompt string) (ImageJob, CallContext, bool, bool, error) {
	if atomicStore, ok := s.store.(*GormStore); ok {
		persisted, call, err := atomicStore.CreateImageJobWithAdmission(s.imageContext, project, key, request.Model, EstimateTextTokens(prompt), job, prompt)
		if err == nil {
			w.Header().Set("x-request-id", call.RequestID)
			writeRateLimitHeaders(w.Header(), call.RateLimitHeaders)
		}
		return persisted, call, true, true, err
	}
	call, ok := s.startImageCall(w, r, project, key, request)
	if !ok {
		return ImageJob{}, CallContext{}, false, false, nil
	}
	persisted, err := s.store.CreateImageJob(imageJobWithAdmission(job, call), prompt)
	return persisted, call, false, true, err
}

func imageJobWithAdmission(job ImageJob, call CallContext) ImageJob {
	job.TokenLimitBucket = call.TokenLimitBucket
	job.MinuteRequestHeld = call.MinuteRequestHeld
	job.UserQuotaEnabled = call.UserQuotaEnabled
	job.UserMinuteRequestHeld = call.UserMinuteRequestHeld
	job.UserTokenLimitBucket = call.UserTokenLimitBucket
	job.RedisBillingAdmitted = call.RedisBillingAdmitted
	job.RedisKeyLeaseHeld = call.RedisKeyLeaseHeld
	job.RedisUserLeaseHeld = call.RedisUserLeaseHeld
	job.ReservedTokens = call.ReservedTokens
	if !call.StartedAt.IsZero() {
		admittedAt := call.StartedAt
		job.AdmittedAt = &admittedAt
	}
	return job
}

func (s *Server) startImageWorkers() {
	s.imageWorkerStart.Do(func() {
		for index := 0; index < s.config.ImageWorkerConcurrency; index++ {
			s.imageWorkerGroup.Add(1)
			go func() {
				defer s.imageWorkerGroup.Done()
				for {
					select {
					case <-s.imageContext.Done():
						return
					case work := <-s.imageQueue:
						s.processImageJob(work)
					}
				}
			}()
		}
	})
}

func (s *Server) enqueueImageJob(work imageJobWork) error {
	s.startImageWorkers()
	select {
	case <-s.imageContext.Done():
		return NewHTTPError(http.StatusServiceUnavailable, "image_worker_stopped", "Image generation worker is stopped")
	case s.imageQueue <- work:
		return nil
	default:
		return NewHTTPError(http.StatusServiceUnavailable, "image_queue_full", "Image generation queue is full")
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	// The instance heartbeat stays published until every database worker has
	// stopped: contract preflight must keep seeing this instance through the
	// drain, on every exit path.
	if s.stopHeartbeat != nil {
		defer s.stopHeartbeat()
	}
	// Traces are flushed last, once every producer of completions has stopped.
	// Deferring it also makes the flush survive the early returns below: failing to
	// drain the image queue is bad, failing to drain it and silently discarding
	// every buffered trace is worse.
	defer s.shutdownTracing()
	s.responseWorkerStop.Do(func() {
		s.responseCancel()
	})
	responseWorkersDone := make(chan struct{})
	go func() {
		s.responseWorkerGroup.Wait()
		close(responseWorkersDone)
	}()
	select {
	case <-responseWorkersDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.billing != nil {
		if err := s.billing.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.reconciliation != nil {
		if err := s.reconciliation.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.credentialRefresh != nil {
		if err := s.credentialRefresh.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.payloadRetention != nil {
		if err := s.payloadRetention.Shutdown(ctx); err != nil {
			return err
		}
	}
	if s.pluginBackgroundRunner != nil {
		if err := s.pluginBackgroundRunner.Shutdown(ctx); err != nil {
			return err
		}
	}
	s.imageWorkerStop.Do(func() {
		s.imageCancel()
	})
	waited := make(chan struct{})
	go func() {
		s.imageWorkerGroup.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		select {
		case work := <-s.imageQueue:
			s.store.FinishCall(work.call, RouteSelection{}, Usage{}, http.StatusServiceUnavailable, "image_worker_stopped", work.clientIP, work.userAgent)
			s.failImageJob(work.job, "image_worker_stopped", "Image generation stopped because the server shut down")
			if work.done != nil {
				close(work.done)
			}
		default:
			if _, err := s.store.FailUnfinishedImageJobs("image_worker_stopped", "Image generation stopped because the server shut down"); err != nil {
				return err
			}
			return nil
		}
	}
}

func (s *Server) processImageJob(work imageJobWork) {
	if work.done != nil {
		defer close(work.done)
	}
	job, claimed, err := s.store.ClaimImageJob(work.job.ID)
	if err != nil {
		s.finishImageJobFailure(work, work.job, RouteSelection{}, Usage{}, http.StatusInternalServerError, "image_job_claim_failed", err.Error())
		return
	}
	if !claimed {
		s.store.FinishCall(work.call, RouteSelection{}, Usage{}, http.StatusConflict, "image_job_not_queued", work.clientIP, work.userAgent)
		return
	}
	ctx := s.imageContext
	if work.call.requestContext != nil {
		ctx = work.call.requestContext
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.config.ImageJobTimeoutSeconds)*time.Second)
	defer cancel()
	routes, routeErr := s.imageRouteCandidates(job.Model)
	if routeErr != nil {
		routeErr = s.annotateRoutingPolicyForCandidateError(&work.call, routeErr)
		httpErr := AsHTTPError(routeErr)
		s.finishImageJobFailure(work, job, RouteSelection{}, Usage{}, httpErr.Status, httpErr.Code, httpErr.Message)
		return
	}
	routes, resolution, routeErr := s.resolveScopedRoutingPolicy(work.call, routes)
	applyRoutingPolicyResolution(&work.call, resolution)
	if routeErr != nil {
		httpErr := AsHTTPError(routeErr)
		s.finishImageJobFailure(work, job, RouteSelection{}, Usage{}, httpErr.Status, httpErr.Code, httpErr.Message)
		return
	}
	routes = s.planRouteOrderWithContext(ctx, work.call, routes)
	if job.Model == codexImageModelName {
		routes = s.filterAndPrioritizeCodexImageRoutes(routes)
	}
	if len(routes) == 0 {
		err = NewHTTPError(http.StatusServiceUnavailable, "image_provider_unavailable", "No image provider route is available")
		s.finishImageJobFailure(work, job, RouteSelection{}, Usage{}, http.StatusServiceUnavailable, "image_provider_unavailable", errorMessage(err))
		return
	}

	routed := RoutedCall{Call: work.call, Routes: routes}
	result, route, usage, attempts, invokeErr := executeRoutedWithStore(ctx, s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (imageRunResult, Usage, error) {
		release := func() {}
		if job.Model == codexImageModelName {
			var err error
			release, err = s.acquireImageAccount(ctx, routeResourceID(route))
			if err != nil {
				return imageRunResult{}, Usage{}, err
			}
		}
		defer release()
		runner := s.imageRunner
		if runner == nil {
			runner = s.imageRunnerForRoute(job, route)
		}
		imageBytes, revisedPrompt, responseUsage, err := runner(ctx, route, job)
		if err != nil {
			return imageRunResult{}, responseUsage, err
		}
		return imageRunResult{data: imageBytes, revisedPrompt: revisedPrompt}, responseUsage, nil
	})
	s.store.RecordRouteAttempts(work.call.RequestID, attempts)
	// Thread the attempt outcomes into the call context so the shared observation
	// point reports per-candidate counts and upstream latency for image jobs the
	// same way it does for chat and embedding calls. Both completion paths below
	// reach observeGatewayCall through call, which carries these attempts.
	work.call.RouteAttempts = attempts
	if invokeErr != nil {
		if errors.Is(invokeErr, context.DeadlineExceeded) {
			message := fmt.Sprintf("Image generation exceeded the configured %d second timeout", s.config.ImageJobTimeoutSeconds)
			s.finishImageJobFailure(work, job, route, usage, http.StatusGatewayTimeout, "image_generation_timeout", message)
			return
		}
		httpErr := AsHTTPError(invokeErr)
		s.finishImageJobFailure(work, job, route, usage, httpErr.Status, httpErr.Code, httpErr.Message)
		return
	}

	asset, err := s.saveImageAsset(job, result.data, "output", 1)
	if err != nil {
		s.finishImageJobFailure(work, job, route, usage, http.StatusInternalServerError, "image_storage_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	job.Status = imageJobStatusCompleted
	job.ProviderID = route.Provider.ID
	job.ProviderResourceID = routeResourceID(route)
	job.ProviderModel = route.ProviderModel
	job.UpstreamRequestID = usage.UpstreamRequestID
	job.InputTokens = usage.PromptTokens
	job.CachedInputTokens = usage.CachedInputTokens
	job.OutputTokens = usage.CompletionTokens
	job.TotalTokens = usage.TotalTokens
	job.CompletedAt = &now
	if err := s.store.CompleteImageJob(work.call, job, result.revisedPrompt, asset, route, usage, work.clientIP, work.userAgent); err != nil {
		if fullPath, pathErr := s.imageAssetPath(asset.RelativePath); pathErr == nil {
			_ = os.Remove(fullPath)
		}
		s.finishImageJobFailure(work, job, route, usage, http.StatusInternalServerError, "image_job_complete_failed", err.Error())
		s.recordRequestPayload(work.call.RequestID, imageAuditRequest(job), auditErrorPayload(err, work.call.RequestID))
		return
	}
	job.RevisedPrompt = result.revisedPrompt
	s.recordRequestPayload(work.call.RequestID, imageAuditRequest(job), map[string]any{"image_job_id": job.ID, "status": job.Status})
}

func (s *Server) acquireImageAccount(ctx context.Context, resourceID string) (func(), error) {
	if strings.TrimSpace(resourceID) == "" {
		return func() {}, nil
	}
	s.imageAccountMu.Lock()
	slot := s.imageAccountSlots[resourceID]
	if slot == nil {
		slot = make(chan struct{}, 1)
		s.imageAccountSlots[resourceID] = slot
	}
	s.imageAccountMu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) finishImageJobFailure(work imageJobWork, job ImageJob, route RouteSelection, usage Usage, status int, code string, message string) {
	s.store.FinishCall(work.call, route, usage, status, code, work.clientIP, work.userAgent)
	s.failImageJob(job, code, message)
	s.recordRequestPayload(work.call.RequestID, imageAuditRequest(job), map[string]any{
		"image_job_id": job.ID,
		"status":       imageJobStatusFailed,
		"error":        map[string]any{"code": code, "message": message},
	})
}

func imageAuditRequest(job ImageJob) map[string]any {
	return map[string]any{
		"image_job_id": job.ID,
		"model":        job.Model,
		"action":       job.Action,
		"quality":      job.Quality,
		"size":         job.Size,
	}
}

func (s *Server) failImageJob(job ImageJob, code string, message string) {
	now := time.Now().UTC()
	job.Status = imageJobStatusFailed
	job.ErrorCode = firstNonEmpty(strings.TrimSpace(code), "image_generation_failed")
	job.ErrorMessage = strings.TrimSpace(message)
	job.CompletedAt = &now
	job.RedisBillingAdmitted = false
	job.RedisKeyLeaseHeld = false
	job.RedisUserLeaseHeld = false
	_ = s.store.UpdateImageJob(job, "")
}

func (s *Server) saveImageAsset(job ImageJob, data []byte, role string, ordinal int) (ImageAsset, error) {
	contentType := http.DetectContentType(data)
	// No fallback value: the default branch rejects anything unrecognised, so every
	// path that reaches the filename below has set a real extension.
	var extension string
	switch contentType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	default:
		return ImageAsset{}, fmt.Errorf("unsupported generated image content type %q", contentType)
	}
	date := job.CreatedAt.UTC()
	if role != "input" && role != "mask" && role != "output" {
		return ImageAsset{}, fmt.Errorf("unsupported image asset role %q", role)
	}
	if ordinal < 1 {
		ordinal = 1
	}
	relative := filepath.Join(job.ProjectID, date.Format("2006"), date.Format("01"), job.ID, fmt.Sprintf("%s-%03d%s", role, ordinal, extension))
	fullPath, err := s.imageAssetPath(relative)
	if err != nil {
		return ImageAsset{}, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return ImageAsset{}, err
	}
	temp, err := os.CreateTemp(filepath.Dir(fullPath), ".image-*")
	if err != nil {
		return ImageAsset{}, err
	}
	tempName := temp.Name()
	// Best effort: on the success path the file has already been renamed away, so a
	// failure here means there is nothing left to remove.
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return ImageAsset{}, err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return ImageAsset{}, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return ImageAsset{}, err
	}
	if err := temp.Close(); err != nil {
		return ImageAsset{}, err
	}
	if err := os.Rename(tempName, fullPath); err != nil {
		return ImageAsset{}, err
	}
	sum := sha256.Sum256(data)
	return ImageAsset{
		JobID:        job.ID,
		ProjectID:    job.ProjectID,
		Role:         role,
		RelativePath: relative,
		ContentType:  contentType,
		ByteSize:     int64(len(data)),
		SHA256:       hex.EncodeToString(sum[:]),
	}, nil
}

func readUploadedImagePart(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxInputImageBytes+1))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_input_image", err.Error())
	}
	if len(data) == 0 || len(data) > maxInputImageBytes {
		return nil, NewHTTPError(http.StatusRequestEntityTooLarge, "input_image_too_large", "Each input image must be 50 MB or smaller")
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/webp":
		return data, nil
	default:
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_input_image", "Input images must be PNG, JPEG, or WebP")
	}
}

func (s *Server) imageAssetPath(relative string) (string, error) {
	root, err := filepath.Abs(s.imageStorageDir)
	if err != nil {
		return "", err
	}
	fullPath, err := filepath.Abs(filepath.Join(root, filepath.Clean(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("image asset path escapes storage root")
	}
	return fullPath, nil
}

func decodeGeneratedImage(encoded string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode image result: %w", err)
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("image result is empty")
	}
	if len(decoded) > maxGeneratedImageBytes {
		return nil, fmt.Errorf("image result exceeds %d bytes", maxGeneratedImageBytes)
	}
	if _, format, err := image.Decode(bytes.NewReader(decoded)); err != nil {
		return nil, fmt.Errorf("decode image result: %w", err)
	} else if format != "png" && format != "jpeg" && format != "webp" {
		return nil, fmt.Errorf("image result must be PNG, JPEG, or WebP")
	}
	return decoded, nil
}

func (s *Server) imageJobResponse(r *http.Request, job ImageJob) map[string]any {
	response := map[string]any{
		"id":         job.ID,
		"object":     "image.job",
		"status":     job.Status,
		"model":      job.Model,
		"action":     firstNonEmpty(job.Action, "generate"),
		"prompt":     job.Prompt,
		"created_at": job.CreatedAt.Unix(),
	}
	if job.RequestID != "" {
		response["request_id"] = job.RequestID
	}
	if job.RevisedPrompt != "" {
		response["revised_prompt"] = job.RevisedPrompt
	}
	if job.StartedAt != nil {
		response["started_at"] = job.StartedAt.Unix()
	}
	if job.CompletedAt != nil {
		response["completed_at"] = job.CompletedAt.Unix()
	}
	if job.ErrorCode != "" {
		response["error"] = map[string]any{"code": job.ErrorCode, "message": job.ErrorMessage}
	}
	if job.TotalTokens > 0 {
		response["usage"] = map[string]any{
			"input_tokens":        job.InputTokens,
			"cached_input_tokens": job.CachedInputTokens,
			"output_tokens":       job.OutputTokens,
			"total_tokens":        job.TotalTokens,
		}
	}
	assets := s.store.ListImageAssets(job.ID)
	if len(assets) > 0 {
		data := make([]map[string]any, 0, len(assets))
		inputCount := 0
		for _, asset := range assets {
			if asset.Role == "input" {
				inputCount++
				continue
			}
			if asset.Role != "output" {
				continue
			}
			url, expires := s.imageAssetURL(r, asset)
			data = append(data, map[string]any{
				"asset_id":       asset.ID,
				"url":            url,
				"url_expires_at": expires,
				"content_type":   asset.ContentType,
				"bytes":          asset.ByteSize,
				"sha256":         asset.SHA256,
			})
		}
		if len(data) > 0 {
			response["data"] = data
		}
		if inputCount > 0 {
			response["input_image_count"] = inputCount
		}
	}
	return response
}

func (s *Server) imageGenerationResponse(r *http.Request, job ImageJob, responseFormat string) (map[string]any, error) {
	data := make([]map[string]any, 0, 1)
	for _, asset := range s.store.ListImageAssets(job.ID) {
		if asset.Role != "output" {
			continue
		}
		item := map[string]any{"asset_id": asset.ID}
		if job.RevisedPrompt != "" {
			item["revised_prompt"] = job.RevisedPrompt
		}
		if responseFormat == "b64_json" {
			fullPath, err := s.imageAssetPath(asset.RelativePath)
			if err != nil {
				return nil, err
			}
			raw, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, err
			}
			item["b64_json"] = base64.StdEncoding.EncodeToString(raw)
		} else {
			url, _ := s.imageAssetURL(r, asset)
			item["url"] = url
		}
		data = append(data, item)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image job %s has no output asset", job.ID)
	}
	return map[string]any{
		"created": job.CreatedAt.Unix(),
		"data":    data,
		"job_id":  job.ID,
		"usage": map[string]any{
			"input_tokens":  job.InputTokens,
			"output_tokens": job.OutputTokens,
			"total_tokens":  job.TotalTokens,
		},
	}, nil
}

func (s *Server) imageAssetURL(r *http.Request, asset ImageAsset) (string, int64) {
	expires := time.Now().Add(imageDownloadTTL).Unix()
	signature := s.imageAssetSignature(asset, expires)
	baseURL := strings.TrimRight(strings.TrimSpace(s.config.PublicBaseURL), "/")
	if baseURL == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("x-forwarded-proto"), "https") {
			scheme = "https"
		}
		baseURL = scheme + "://" + r.Host
	}
	return fmt.Sprintf("%s/v1/image-assets/%s/content?expires=%d&signature=%s", baseURL, asset.ID, expires, signature), expires
}

func (s *Server) imageAssetSignature(asset ImageAsset, expires int64) string {
	key := strings.TrimSpace(s.config.SecretKey)
	if key == "" {
		key = "dev_tokenhub_secret_key"
	}
	derived := hmac.New(sha256.New, []byte(key))
	_, _ = derived.Write([]byte("tokenhub-image-download-v1"))
	mac := hmac.New(sha256.New, derived.Sum(nil))
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%d", asset.ID, asset.ProjectID, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validImageAssetSignature(asset ImageAsset, expires int64, signature string) bool {
	expected, err := hex.DecodeString(s.imageAssetSignature(asset, expires))
	if err != nil {
		return false
	}
	actual, err := hex.DecodeString(strings.TrimSpace(signature))
	return err == nil && hmac.Equal(expected, actual)
}

func prefersAsyncImageResponse(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("prefer")), "respond-async") ||
		strings.EqualFold(strings.TrimSpace(r.Header.Get("x-tokenhub-async")), "true")
}

func normalizedImageOption(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func normalizeImageGenerationRequest(request *imageGenerationRequest) error {
	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" {
		request.Model = codexImageModelName
	}
	if request.Model != codexImageModelName && request.Model != openAIImageModelName {
		return NewHTTPError(http.StatusBadRequest, "unsupported_image_model", "Only codex-gpt-image-2 and gpt-image-2 are supported")
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		return NewHTTPError(http.StatusBadRequest, "missing_prompt", "prompt is required")
	}
	if request.N == 0 {
		request.N = 1
	}
	if request.N != 1 {
		return NewHTTPError(http.StatusBadRequest, "unsupported_image_count", "Image generation currently supports n=1")
	}
	request.Quality = normalizedImageOption(request.Quality, "auto")
	if request.Quality != "auto" && request.Quality != "low" && request.Quality != "medium" && request.Quality != "high" {
		return NewHTTPError(http.StatusBadRequest, "invalid_quality", "quality must be auto, low, medium, or high")
	}
	request.Size = normalizedImageOption(request.Size, "auto")
	if !validGPTImage2Size(request.Size) {
		return NewHTTPError(http.StatusBadRequest, "invalid_size", "size must be auto or a valid gpt-image-2 WIDTHxHEIGHT value")
	}
	request.ResponseFormat = normalizedImageOption(request.ResponseFormat, "url")
	if request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return NewHTTPError(http.StatusBadRequest, "invalid_response_format", "response_format must be url or b64_json")
	}
	return nil
}

func (s *Server) imageRunnerForRoute(job ImageJob, route RouteSelection) func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
	if job.Model == codexImageModelName {
		return s.executeCodexSubscriptionImage
	}
	if _, ok := resolveTypedAdapter[ProviderImageGenerator](s.adapterRegistry, route.Provider.Type); ok {
		return s.executeProviderImage
	}
	if route.Provider.Type == ProviderOpenAI {
		return s.executeOpenAIImage
	}
	return s.executeUnsupportedProviderImage
}

func (s *Server) executeUnsupportedProviderImage(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
	return nil, "", Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support image generation")
}

func (s *Server) executeProviderImage(ctx context.Context, route RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
	prepared, err := s.prepareRouteForUpstream(ctx, route)
	if err != nil {
		return nil, "", Usage{}, err
	}
	adapter, ok := resolveTypedAdapter[ProviderImageGenerator](s.adapterRegistry, prepared.Provider.Type)
	if !ok {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support image generation")
	}
	request, err := s.providerImageGenerationRequest(prepared, job)
	if err != nil {
		return nil, "", Usage{}, err
	}
	return adapter.GenerateImage(ctx, prepared.Provider, prepared.ProviderModel, request)
}

func (s *Server) providerImageGenerationRequest(route RouteSelection, job ImageJob) (ProviderImageGenerationRequest, error) {
	request := ProviderImageGenerationRequest{
		Action:         job.Action,
		Model:          firstNonEmpty(strings.TrimSpace(route.ProviderModel), job.Model),
		Prompt:         job.Prompt,
		Quality:        normalizedImageOption(job.Quality, "auto"),
		Size:           normalizedImageOption(job.Size, "auto"),
		ResponseFormat: "b64_json",
	}
	for _, asset := range s.store.ListImageAssets(job.ID) {
		if asset.Role != "input" && asset.Role != "mask" {
			continue
		}
		path, err := s.imageAssetPath(asset.RelativePath)
		if err != nil {
			return ProviderImageGenerationRequest{}, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return ProviderImageGenerationRequest{}, err
		}
		request.Images = append(request.Images, ProviderImageInput{
			Role:        asset.Role,
			ContentType: asset.ContentType,
			DataBase64:  base64.StdEncoding.EncodeToString(raw),
		})
	}
	return request, nil
}

func (s *Server) imageRouteCandidates(model string) ([]RouteSelection, error) {
	if model == codexImageModelName {
		routes, err := s.store.SelectRouteCandidates(codexImageModelName)
		if err != nil {
			return nil, err
		}
		filtered := make([]RouteSelection, 0, len(routes))
		for _, route := range routes {
			if route.Provider.Type == ProviderOpenAICodex &&
				route.ProviderModel == codexImageUpstreamModel &&
				route.Resource != nil && isOpenAIAccountResource(route.Resource.ResourceType) {
				filtered = append(filtered, route)
			}
		}
		if len(filtered) == 0 {
			return nil, ErrProviderMissing
		}
		return filtered, nil
	}
	routes, err := s.store.SelectRouteCandidates(openAIImageModelName)
	if err != nil {
		return nil, err
	}
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if route.Provider.Type != ProviderOpenAICodex {
			filtered = append(filtered, route)
		}
	}
	return s.routesWithAdapterCapability(filtered, AdapterCapabilityImageGenerate), nil
}

func validGPTImage2Size(value string) bool {
	if value == "auto" {
		return true
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return false
	}
	width, errWidth := strconv.Atoi(parts[0])
	height, errHeight := strconv.Atoi(parts[1])
	if errWidth != nil || errHeight != nil || width <= 0 || height <= 0 {
		return false
	}
	if width > 3840 || height > 3840 || width%16 != 0 || height%16 != 0 {
		return false
	}
	short, long := width, height
	if short > long {
		short, long = long, short
	}
	pixels := int64(width) * int64(height)
	return long <= short*3 && pixels >= 655360 && pixels <= 8294400
}

func (s *Server) filterAndPrioritizeCodexImageRoutes(routes []RouteSelection) []RouteSelection {
	supported := make([]RouteSelection, 0, len(routes))
	recoveryDue := make([]RouteSelection, 0, len(routes))
	unknown := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		capability := ""
		if route.Resource != nil {
			capability = strings.TrimSpace(route.Resource.Options[codexImageCapabilityOption])
		}
		switch capability {
		case codexImageCapabilityUnsupported:
			checkedAt, err := time.Parse(time.RFC3339Nano, route.Resource.Options[codexImageCapabilityCheckedAtOption])
			retryAfter := time.Duration(s.config.ImageCapabilityRetrySecs) * time.Second
			if err == nil && retryAfter > 0 && time.Since(checkedAt) < retryAfter {
				continue
			}
			recoveryDue = append(recoveryDue, route)
		case codexImageCapabilitySupported:
			supported = append(supported, route)
		default:
			unknown = append(unknown, route)
		}
	}
	prioritized := make([]RouteSelection, 0, len(supported)+len(recoveryDue)+len(unknown))
	if len(recoveryDue) > 0 {
		prioritized = append(prioritized, recoveryDue[0])
	}
	prioritized = append(prioritized, supported...)
	prioritized = append(prioritized, unknown...)
	if len(recoveryDue) > 1 {
		prioritized = append(prioritized, recoveryDue[1:]...)
	}
	return prioritized
}

func imageJobErrorStatus(code string) int {
	switch code {
	case "codex_image_forbidden", "model_not_allowed":
		return http.StatusForbidden
	case "codex_image_account_unavailable", "image_provider_unavailable", "provider_unavailable":
		return http.StatusServiceUnavailable
	case "codex_account_auth_failed":
		return http.StatusUnauthorized
	case "codex_rate_limited", "codex_quota_exhausted":
		return http.StatusTooManyRequests
	case "image_generation_timeout":
		return http.StatusGatewayTimeout
	case "missing_prompt", "invalid_input_image", "image_mask_not_supported":
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}
