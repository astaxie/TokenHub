package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	store    Store
	adapters map[string]ProviderAdapter
	mux      *http.ServeMux
	config   Config
}

func New(store Store) *Server {
	return NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
}

func NewWithConfig(store Store, config Config) *Server {
	client := &http.Client{Timeout: 120 * time.Second}
	openai := OpenAICompatibleAdapter{Client: client}
	s := &Server{
		store: store,
		adapters: map[string]ProviderAdapter{
			ProviderMock:             MockAdapter{},
			ProviderOpenAI:           openai,
			ProviderOpenAICompatible: openai,
			"deepseek":               openai,
			"qwen":                   openai,
			"local":                  openai,
			ProviderAzureOpenAI:      AzureOpenAIAdapter{Client: client},
			ProviderAnthropic:        AnthropicAdapter{Client: client},
			ProviderGemini:           GeminiAdapter{Client: client},
		},
		mux:    http.NewServeMux(),
		config: config,
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.cors(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/livez", s.handleLive)
	s.mux.HandleFunc("/readyz", s.handleHealth)
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/v1/models", s.handleModels)
	s.mux.HandleFunc("/v1/models/", s.handleModel)
	s.mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("/v1/responses", s.handleResponses)
	s.mux.HandleFunc("/v1/embeddings", s.handleEmbeddings)

	s.mux.HandleFunc("/api/admin/auth/login", s.handleAdminLogin)
	s.mux.HandleFunc("/api/admin/auth/logout", s.handleAdminLogout)
	s.mux.HandleFunc("/api/admin/auth/me", s.handleAdminMe)
	s.mux.HandleFunc("/api/admin/auth/reset-password", s.handleAdminResetPassword)
	s.mux.HandleFunc("/api/admin/auth/identity-providers", s.handleAdminAuthIdentityProviders)
	s.mux.HandleFunc("/api/admin/auth/oauth/start", s.handleAdminOAuthStart)
	s.mux.HandleFunc("/api/admin/auth/oauth/callback", s.handleAdminOAuthCallback)
	s.mux.HandleFunc("/api/admin/overview", s.handleAdminOverview)
	s.mux.HandleFunc("/api/admin/playground/chat", s.handleAdminPlaygroundChat)
	s.mux.HandleFunc("/api/admin/projects", s.handleAdminProjects)
	s.mux.HandleFunc("/api/admin/projects/", s.handleAdminProjectNested)
	s.mux.HandleFunc("/api/admin/users", s.handleAdminUsers)
	s.mux.HandleFunc("/api/admin/users/import", s.handleAdminUsersImport)
	s.mux.HandleFunc("/api/admin/users/", s.handleAdminUserItem)
	s.mux.HandleFunc("/api/admin/provider-catalog", s.handleAdminProviderCatalog)
	s.mux.HandleFunc("/api/admin/provider-catalog/", s.handleAdminProviderCatalogItem)
	s.mux.HandleFunc("/api/admin/provider-account-oauth/openai/generate-auth-url", s.handleAdminOpenAIAccountOAuthGenerateAuthURL)
	s.mux.HandleFunc("/api/admin/provider-account-oauth/openai/exchange-code", s.handleAdminOpenAIAccountOAuthExchangeCode)
	s.mux.HandleFunc("/api/admin/provider-account-oauth/openai/oauth/callback", s.handleOpenAIAccountOAuthCallback)
	s.mux.HandleFunc("/api/admin/api-keys", s.handleAdminAPIKeys)
	s.mux.HandleFunc("/api/admin/api-keys/", s.handleAdminAPIKeyItem)
	s.mux.HandleFunc("/api/admin/providers", s.handleAdminProviders)
	s.mux.HandleFunc("/api/admin/providers/", s.handleAdminProviderNested)
	s.mux.HandleFunc("/api/admin/provider-resources", s.handleAdminProviderResources)
	s.mux.HandleFunc("/api/admin/provider-resources/", s.handleAdminProviderResourceNested)
	s.mux.HandleFunc("/api/admin/models", s.handleAdminModels)
	s.mux.HandleFunc("/api/admin/models/", s.handleAdminModelItem)
	s.mux.HandleFunc("/api/admin/routing-rules", s.handleAdminRoutes)
	s.mux.HandleFunc("/api/admin/routing-rules/", s.handleAdminRouteItem)
	s.mux.HandleFunc("/api/admin/resources/", s.handleAdminResources)
	s.mux.HandleFunc("/api/admin/sqlite/backups", s.handleAdminSQLiteBackups)
	s.mux.HandleFunc("/api/admin/sqlite/backups/", s.handleAdminSQLiteBackupItem)
	s.mux.HandleFunc("/api/admin/billing/generate", s.handleAdminGenerateBilling)
	s.mux.HandleFunc("/api/admin/export/", s.handleAdminExport)
	s.mux.HandleFunc("/api/admin/usage/summary", s.handleAdminUsageSummary)
	s.mux.HandleFunc("/api/admin/usage/breakdown", s.handleAdminUsageBreakdown)
	s.mux.HandleFunc("/api/admin/usage/timeseries", s.handleAdminUsageTimeseries)
	s.mux.HandleFunc("/api/admin/audit/requests", s.handleAdminRequestLogs)
	s.mux.HandleFunc("/api/admin/audit/requests/", s.handleAdminRequestDetail)
	s.mux.HandleFunc("/api/admin/audit/events", s.handleAdminAuditEvents)
	s.mux.HandleFunc("/api/admin/alerts", s.handleAdminAlerts)
	s.mux.HandleFunc("/api/admin/alerts/", s.handleAdminAlertItem)
	s.mux.HandleFunc("/api/admin/alert-deliveries", s.handleAdminAlertDeliveries)
	s.mux.HandleFunc("/api/admin/approvals", s.handleAdminApprovals)
	s.mux.HandleFunc("/api/admin/approvals/", s.handleAdminApprovalItem)
	s.mux.HandleFunc("/api/admin/system/db-status", s.handleAdminSystemDBStatus)
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "service": "tokenhub-backend"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	models := s.store.AccessibleModels(key)
	data := make([]modelListItem, 0, len(models))
	for _, model := range models {
		data = append(data, buildModelListItem(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	modelID, err := modelIDFromPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, model := range s.store.AccessibleModels(key) {
		if model.Name == modelID || model.ID == modelID {
			writeJSON(w, http.StatusOK, buildModelListItem(model))
			return
		}
	}
	writeError(w, r, NewHTTPError(404, "model_not_found", "Model not found"))
}

func modelIDFromPath(r *http.Request) (string, error) {
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/models/")
	escaped = strings.Trim(escaped, "/")
	if escaped == "" {
		return "", NewHTTPError(404, "model_not_found", "Model not found")
	}
	modelID, err := url.PathUnescape(escaped)
	if err != nil || strings.TrimSpace(modelID) == "" {
		return "", NewHTTPError(400, "invalid_model", "model path parameter is invalid")
	}
	return strings.TrimSpace(modelID), nil
}

type modelListItem struct {
	ID                   string `json:"id"`
	Created              int64  `json:"created"`
	Object               string `json:"object"`
	OwnedBy              string `json:"owned_by,omitempty"`
	InputTokenPricePerM  int64  `json:"input_token_price_per_m"`
	OutputTokenPricePerM int64  `json:"output_token_price_per_m"`
	Title                string `json:"title"`
	Description          string `json:"description"`
	ContextSize          int64  `json:"context_size"`
}

func buildModelListItem(model Model) modelListItem {
	inputPrice := model.InputPriceUSDPer1M
	if inputPrice == 0 && model.EmbeddingPriceUSDPer1M > 0 {
		inputPrice = model.EmbeddingPriceUSDPer1M
	}
	return modelListItem{
		ID:                   model.Name,
		Created:              modelCreatedUnix(model),
		Object:               "model",
		OwnedBy:              "tokenhub",
		InputTokenPricePerM:  modelTokenPricePerM(inputPrice),
		OutputTokenPricePerM: modelTokenPricePerM(model.OutputPriceUSDPer1M),
		Title:                modelTitle(model),
		Description:          modelDescription(model),
		ContextSize:          model.ContextWindow,
	}
}

func modelCreatedUnix(model Model) int64 {
	if model.CreatedAt.IsZero() {
		return 0
	}
	return model.CreatedAt.Unix()
}

func modelTokenPricePerM(priceUSDPer1M float64) int64 {
	if priceUSDPer1M <= 0 {
		return 0
	}
	// JieKou-compatible model listings use integer price units; 1 USD/1M tokens is 10000.
	return int64(math.Round(priceUSDPer1M * 10000))
}

func modelTitle(model Model) string {
	if value := strings.TrimSpace(model.Metadata["title"]); value != "" {
		return value
	}
	return model.Name
}

func modelDescription(model Model) string {
	if value := strings.TrimSpace(model.Metadata["description"]); value != "" {
		return value
	}
	modality := firstNonEmpty(model.Modality, "chat")
	family := firstNonEmpty(model.Family, model.Category, "custom")
	return fmt.Sprintf("TokenHub %s model in the %s family.", modality, family)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req ChatCompletionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}

	routed, ok := s.startRoutedCall(w, r, project, key, req.Model, req)
	if !ok {
		return
	}

	if req.Stream {
		route := routed.Routes[0]
		resourceID := routeResourceID(route)
		leaseID, leaseCtx, err := s.store.CheckProviderResourceCapacity(r.Context(), resourceID)
		if err != nil {
			s.finishFailedRoutedCall(r, routed, []RouteAttempt{{
				Selection: route,
				Status:    AsHTTPError(err).Status,
				ErrorCode: AsHTTPError(err).Code,
				Error:     err.Error(),
			}}, err)
			s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
			writeError(w, r, err)
			return
		}
		preparedRoute, err := s.prepareRouteForUpstream(leaseCtx, route)
		if err != nil {
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			finishProviderResourceAttempt(s.store, resourceID, leaseID, err, Usage{})
			httpErr := AsHTTPError(err)
			s.store.FinishCall(routed.Call, route, Usage{}, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
			s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
			writeError(w, r, err)
			return
		}
		route = preparedRoute
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			finishProviderResourceAttempt(s.store, resourceID, leaseID, err, Usage{})
			httpErr := AsHTTPError(err)
			s.store.FinishCall(routed.Call, route, Usage{}, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
			s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
			writeError(w, r, err)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("x-request-id", routed.Call.RequestID)
		s.writeRouteHeaders(w, routed.Call, route, 1)
		streamReq := req
		allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
		attempts := make([]RouteAttempt, 0, 2)
		streamWriter := &streamWriteTracker{writer: w}
		var usage Usage
		for {
			usage, err = adapter.ChatStream(leaseCtx, route.Provider, route.ProviderModel, streamReq, streamWriter)
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			retryWithoutEffort := allowEffortFallback &&
				streamReq.ReasoningEffort != nil &&
				!streamWriter.Wrote() &&
				isReasoningEffortRejection(err)
			if !retryWithoutEffort {
				finishProviderResourceAttempt(s.store, resourceID, leaseID, err, usage)
			}
			status, code := routeAttemptStatusAndCode(err, retryWithoutEffort)
			attempts = append(attempts, RouteAttempt{
				Selection: route,
				Status:    status,
				ErrorCode: code,
				Error:     errorMessage(err),
			})
			if !retryWithoutEffort {
				break
			}

			streamReq.ReasoningEffort = nil
			if retryErr := s.store.CheckProviderResourceRetryCapacity(leaseCtx, resourceID, leaseID); retryErr != nil {
				s.store.ReleaseProviderResourceCapacity(resourceID, leaseID)
				err = retryErr
				status, code = statusAndCode(retryErr)
				attempts = append(attempts, RouteAttempt{
					Selection: route,
					Status:    status,
					ErrorCode: code,
					Error:     errorMessage(retryErr),
				})
				s.writeRouteHeaders(w, routed.Call, route, len(attempts))
				usage = Usage{}
				break
			}
			s.writeRouteHeaders(w, routed.Call, route, len(attempts)+1)
		}
		status, code := statusAndCode(err)
		if err == nil {
			s.store.MarkRouteUsed(route.Route.ID)
			s.store.MarkProviderResourceUsed(routeResourceID(route))
		}
		s.store.RecordRouteAttempts(routed.Call.RequestID, attempts)
		s.store.FinishCall(routed.Call, route, usage, status, code, s.clientIP(r), r.UserAgent())
		s.recordRequestPayload(routed.Call.RequestID, req, auditStreamPayload(status, code, err))
		if err != nil && !streamWriter.Wrote() {
			w.Header().Del("cache-control")
			writeError(w, r, err)
		}
		return
	}

	resp, route, usage, attempts, err := s.executeRoutedChat(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, err)
		s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.store.RecordRouteAttempts(routed.Call.RequestID, attempts)
	s.store.FinishCall(routed.Call, route, usage, http.StatusOK, "", s.clientIP(r), r.UserAgent())
	s.recordRequestPayload(routed.Call.RequestID, req, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req ResponsesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	if req.Stream {
		writeError(w, r, NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Streaming responses are not supported",
		))
		return
	}
	routed, ok := s.startRoutedCall(w, r, project, key, req.Model, req)
	if !ok {
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedResponses(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, err)
		s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.store.RecordRouteAttempts(routed.Call.RequestID, attempts)
	s.store.FinishCall(routed.Call, route, usage, http.StatusOK, "", s.clientIP(r), r.UserAgent())
	s.recordRequestPayload(routed.Call.RequestID, req, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req EmbeddingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	routed, ok := s.startRoutedCall(w, r, project, key, req.Model, req)
	if !ok {
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedEmbeddings(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, err)
		s.recordRequestPayload(routed.Call.RequestID, req, auditErrorPayload(err, routed.Call.RequestID))
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.store.RecordRouteAttempts(routed.Call.RequestID, attempts)
	s.store.FinishCall(routed.Call, route, usage, http.StatusOK, "", s.clientIP(r), r.UserAgent())
	s.recordRequestPayload(routed.Call.RequestID, req, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) startRoutedCall(w http.ResponseWriter, r *http.Request, project Project, key APIKey, model string, requestPayload any) (RoutedCall, bool) {
	call, err := s.store.StartCall(r.Context(), project, key, model)
	if err != nil {
		httpErr := AsHTTPError(err)
		requestID := s.store.RecordRejectedRequest(project, key, model, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		w.Header().Set("x-request-id", requestID)
		s.recordRequestPayload(requestID, requestPayload, auditErrorPayload(err, requestID))
		writeError(w, r, err)
		return RoutedCall{}, false
	}
	w.Header().Set("x-request-id", call.RequestID)
	if call.requestContext != nil {
		*r = *r.WithContext(call.requestContext)
	}
	routes, err := s.store.SelectRouteCandidates(model)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.store.FinishCall(call, RouteSelection{}, Usage{}, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		s.recordRequestPayload(call.RequestID, requestPayload, auditErrorPayload(err, call.RequestID))
		writeError(w, r, err)
		return RoutedCall{}, false
	}
	return RoutedCall{Call: call, Routes: s.planRouteOrder(call, routes)}, true
}

func (s *Server) executeRoutedChat(r *http.Request, routed RoutedCall, req ChatCompletionRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
	return executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq.ReasoningEffort = nil
		}
		return adapter.Chat(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}

func (s *Server) executeRoutedResponses(r *http.Request, routed RoutedCall, req ResponsesRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(responsesReasoningEffort(req)) != nil
	return executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq.Reasoning = nil
		}
		return adapter.Responses(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}

func (s *Server) executeRoutedEmbeddings(r *http.Request, routed RoutedCall, req EmbeddingsRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		return adapter.Embeddings(ctx, route.Provider, route.ProviderModel, req)
	})
}

func (s *Server) handleAdminPlaygroundChat(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "playground", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req ChatCompletionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	req.Stream = false
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, r, NewHTTPError(400, "missing_messages", "messages are required"))
		return
	}
	for _, message := range req.Messages {
		if strings.TrimSpace(message.Role) == "" {
			writeError(w, r, NewHTTPError(400, "invalid_message", "message role is required"))
			return
		}
	}

	routes, err := s.store.SelectRouteCandidates(req.Model)
	if err != nil {
		writeError(w, r, err)
		return
	}
	requestID := NewID("pg")
	routed := RoutedCall{
		Call: CallContext{
			RequestID: requestID,
			Project:   Project{ID: "admin_playground", Name: "Admin Playground", Status: StatusActive},
			Key:       APIKey{ID: user.ID, Name: "Admin Playground"},
			Model:     Model{Name: req.Model, Status: StatusActive},
			StartedAt: time.Now().UTC(),
		},
	}
	routed.Routes = s.planRouteOrder(routed.Call, routes)
	resp, route, usage, attempts, err := s.executeRoutedChat(r, routed, req)
	if err != nil {
		httpErr := AsHTTPError(err)
		route = lastAttemptRoute(attempts)
		s.store.RecordRouteAttempts(requestID, attempts)
		s.store.RecordPlaygroundRequest(routed.Call, route, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
		s.recordRequestPayload(requestID, req, auditErrorPayload(err, requestID))
		s.recordAdminAudit(r, user, "chat_failed", "playground", req.Model, "", map[string]any{
			"model":    req.Model,
			"attempts": playgroundRouteAttempts(attempts),
			"error":    httpErr.Code,
		})
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.store.RecordRouteAttempts(requestID, attempts)
	s.store.RecordPlaygroundRequest(routed.Call, route, http.StatusOK, "", s.clientIP(r), r.UserAgent())
	s.recordAdminAudit(r, user, "chat", "playground", req.Model, "", map[string]any{
		"model":    req.Model,
		"route":    playgroundRouteSummary(route),
		"usage":    usage,
		"attempts": len(attempts),
	})
	s.recordRequestPayload(requestID, req, resp)
	w.Header().Set("x-request-id", requestID)
	writeJSON(w, http.StatusOK, PlaygroundChatResponse{
		Response:  resp,
		Route:     playgroundRouteSummary(route),
		Usage:     usage,
		Attempts:  playgroundRouteAttempts(attempts),
		RequestID: requestID,
	})
}

func executeRoutedWithStore[T any](
	ctx context.Context,
	store Store,
	routed RoutedCall,
	allowReasoningEffortFallback bool,
	call func(context.Context, RouteSelection, bool) (T, Usage, error),
) (T, RouteSelection, Usage, []RouteAttempt, error) {
	var zero T
	var lastErr error = ErrProviderMissing
	attempts := make([]RouteAttempt, 0, len(routed.Routes)+1)
	for _, route := range routed.Routes {
		if leaseErr := coordinationLeaseError(ctx); leaseErr != nil {
			return zero, route, Usage{}, attempts, leaseErr
		}
		resourceID := routeResourceID(route)
		leaseID, leaseCtx, err := store.CheckProviderResourceCapacity(ctx, resourceID)
		if err != nil {
			status, code := statusAndCode(err)
			attempts = append(attempts, RouteAttempt{
				Selection: route,
				Status:    status,
				ErrorCode: code,
				Error:     errorMessage(err),
			})
			lastErr = err
			if !shouldFailoverProviderError(err) {
				return zero, route, Usage{}, attempts, err
			}
			continue
		}
		omitReasoningEffort := false
		for {
			resp, usage, err := call(leaseCtx, route, omitReasoningEffort)
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			retryWithoutEffort := allowReasoningEffortFallback &&
				!omitReasoningEffort &&
				isReasoningEffortRejection(err)
			if !retryWithoutEffort {
				finishProviderResourceAttempt(store, resourceID, leaseID, err, usage)
			}
			status, code := routeAttemptStatusAndCode(err, retryWithoutEffort)
			attempts = append(attempts, RouteAttempt{
				Selection: route,
				Status:    status,
				ErrorCode: code,
				Error:     errorMessage(err),
			})
			if err == nil {
				return resp, route, usage, attempts, nil
			}
			lastErr = err
			if retryWithoutEffort {
				if retryErr := store.CheckProviderResourceRetryCapacity(leaseCtx, resourceID, leaseID); retryErr != nil {
					store.ReleaseProviderResourceCapacity(resourceID, leaseID)
					status, code = statusAndCode(retryErr)
					attempts = append(attempts, RouteAttempt{
						Selection: route,
						Status:    status,
						ErrorCode: code,
						Error:     errorMessage(retryErr),
					})
					lastErr = retryErr
					if !shouldFailoverProviderError(retryErr) {
						return zero, route, Usage{}, attempts, retryErr
					}
					break
				}
				omitReasoningEffort = true
				continue
			}
			if !shouldFailoverProviderError(err) {
				return zero, route, usage, attempts, err
			}
			break
		}
	}
	return zero, RouteSelection{}, Usage{}, attempts, lastErr
}

func routeAttemptStatusAndCode(err error, reasoningEffortRejected bool) (int, string) {
	if !reasoningEffortRejected {
		return statusAndCode(err)
	}
	httpErr := AsHTTPError(err)
	return httpErr.UpstreamStatus, "reasoning_effort_rejected"
}

func coordinationLeaseError(ctx context.Context) error {
	if ctx != nil && errors.Is(context.Cause(ctx), ErrCoordinationLeaseLost) {
		return ErrCoordinationLeaseLost
	}
	return nil
}

func finishProviderResourceAttempt(store Store, resourceID string, leaseID string, err error, usage Usage) {
	if resourceID == "" {
		return
	}
	if errors.Is(err, ErrCoordinationLeaseLost) {
		store.ReleaseProviderResourceCapacity(resourceID, leaseID)
		return
	}
	store.FinishProviderResourceAttempt(resourceID, leaseID, err == nil, usage)
}

type streamWriteTracker struct {
	writer io.Writer
	wrote  bool
}

func (w *streamWriteTracker) Write(data []byte) (int, error) {
	if !w.wrote {
		if responseWriter, ok := w.writer.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusOK)
			if flusher, ok := responseWriter.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
	w.wrote = true
	return w.writer.Write(data)
}

func (w *streamWriteTracker) Wrote() bool {
	return w != nil && w.wrote
}

func (s *Server) finishFailedRoutedCall(r *http.Request, routed RoutedCall, attempts []RouteAttempt, err error) {
	httpErr := AsHTTPError(err)
	route := lastAttemptRoute(attempts)
	s.store.RecordRouteAttempts(routed.Call.RequestID, attempts)
	s.store.FinishCall(routed.Call, route, Usage{}, httpErr.Status, httpErr.Code, s.clientIP(r), r.UserAgent())
}

func (s *Server) adapterForRoute(route RouteSelection) (ProviderAdapter, error) {
	adapter, ok := s.adapters[route.Provider.Type]
	if !ok {
		return nil, NewHTTPError(503, "provider_adapter_missing", "Provider adapter is not registered")
	}
	return adapter, nil
}

func (s *Server) planRouteOrder(call CallContext, routes []RouteSelection) []RouteSelection {
	ordered := append([]RouteSelection(nil), routes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Route.Priority != ordered[j].Route.Priority {
			return ordered[i].Route.Priority < ordered[j].Route.Priority
		}
		if routeResourcePriority(ordered[i]) != routeResourcePriority(ordered[j]) {
			return routeResourcePriority(ordered[i]) < routeResourcePriority(ordered[j])
		}
		if routeWeight(ordered[i].Route) != routeWeight(ordered[j].Route) {
			return routeWeight(ordered[i].Route) > routeWeight(ordered[j].Route)
		}
		return routeSortID(ordered[i]) < routeSortID(ordered[j])
	})

	var planned []RouteSelection
	for len(ordered) > 0 {
		priority := ordered[0].Route.Priority
		end := 0
		for end < len(ordered) && ordered[end].Route.Priority == priority {
			end++
		}
		priorityGroup := append([]RouteSelection(nil), ordered[:end]...)
		for len(priorityGroup) > 0 {
			resourcePriority := routeResourcePriority(priorityGroup[0])
			groupEnd := 0
			for groupEnd < len(priorityGroup) && routeResourcePriority(priorityGroup[groupEnd]) == resourcePriority {
				groupEnd++
			}
			group := append([]RouteSelection(nil), priorityGroup[:groupEnd]...)
			strategy := routeStrategy(group[0].Route)
			if strategy == RouteStrategyPriorityOnly || strategy == RouteStrategyQuality || strategy == RouteStrategyCost {
				sortRouteGroupByStrategy(strategy, group)
				planned = append(planned, group...)
				priorityGroup = priorityGroup[groupEnd:]
				continue
			}
			if group[0].Route.StickySession {
				index := stickyRouteIndex(call, group)
				planned = append(planned, group[index])
				group = append(group[:index], group[index+1:]...)
			}
			for len(group) > 0 {
				index := weightedRouteIndex(call.RequestID, len(planned), group)
				planned = append(planned, group[index])
				group = append(group[:index], group[index+1:]...)
			}
			priorityGroup = priorityGroup[groupEnd:]
		}
		ordered = ordered[end:]
	}
	return planned
}

func sortRouteGroupByStrategy(strategy string, routes []RouteSelection) {
	sort.SliceStable(routes, func(i, j int) bool {
		switch strategy {
		case RouteStrategyQuality:
			if routeQualityScore(routes[i].Route) != routeQualityScore(routes[j].Route) {
				return routeQualityScore(routes[i].Route) > routeQualityScore(routes[j].Route)
			}
		case RouteStrategyCost:
			if routeCostScore(routes[i].Route) != routeCostScore(routes[j].Route) {
				return routeCostScore(routes[i].Route) > routeCostScore(routes[j].Route)
			}
		}
		if routeWeight(routes[i].Route) != routeWeight(routes[j].Route) {
			return routeWeight(routes[i].Route) > routeWeight(routes[j].Route)
		}
		return routeSortID(routes[i]) < routeSortID(routes[j])
	})
}

func stickyRouteIndex(call CallContext, routes []RouteSelection) int {
	if len(routes) <= 1 {
		return 0
	}
	stickyKey := call.Key.ID
	if stickyKey == "" {
		stickyKey = call.Project.ID
	}
	return stableHashInt(stickyKey, len(routes)) % len(routes)
}

func weightedRouteIndex(requestID string, salt int, routes []RouteSelection) int {
	if len(routes) <= 1 {
		return 0
	}
	total := 0
	for _, route := range routes {
		total += routeEffectiveWeight(route.Route)
	}
	if total <= 0 {
		return 0
	}
	needle := stableHashInt(requestID, salt) % total
	for index, route := range routes {
		needle -= routeEffectiveWeight(route.Route)
		if needle < 0 {
			return index
		}
	}
	return len(routes) - 1
}

func stableHashInt(value string, salt int) int {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte(":"))
	_, _ = hash.Write([]byte(strconv.Itoa(salt)))
	return int(hash.Sum64() % uint64(^uint(0)>>1))
}

func routeWeight(route ModelRoute) int {
	if route.Weight <= 0 {
		return 1
	}
	return route.Weight
}

func routeEffectiveWeight(route ModelRoute) int {
	weight := routeWeight(route)
	switch routeStrategy(route) {
	case RouteStrategyBalanced:
		return maxInt(1, weight+routeQualityScore(route)+routeCostScore(route))
	default:
		return weight
	}
}

func routeQualityScore(route ModelRoute) int {
	if route.QualityScore <= 0 {
		return 50
	}
	if route.QualityScore > 100 {
		return 100
	}
	return route.QualityScore
}

func routeCostScore(route ModelRoute) int {
	if route.CostScore <= 0 {
		return 50
	}
	if route.CostScore > 100 {
		return 100
	}
	return route.CostScore
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func routeResourcePriority(route RouteSelection) int {
	if route.Resource == nil || route.Resource.Priority == 0 {
		return 9999
	}
	return route.Resource.Priority
}

func routeResourceID(route RouteSelection) string {
	if route.Resource != nil {
		return route.Resource.ID
	}
	return route.Route.ProviderResourceID
}

func routeSortID(route RouteSelection) string {
	if resourceID := routeResourceID(route); resourceID != "" {
		return route.Route.ID + ":" + resourceID
	}
	return route.Route.ID
}

func routeStrategy(route ModelRoute) string {
	if strings.TrimSpace(route.Strategy) == "" {
		return RouteStrategyBalanced
	}
	strategy := strings.TrimSpace(route.Strategy)
	if strategy == RouteStrategyPriorityWeighted {
		return RouteStrategyBalanced
	}
	return strategy
}

func shouldFailoverProviderError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCoordinationLeaseLost) {
		return false
	}
	httpErr := AsHTTPError(err)
	switch httpErr.Status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return httpErr.Status >= 500
	}
}

func lastAttemptRoute(attempts []RouteAttempt) RouteSelection {
	if len(attempts) == 0 {
		return RouteSelection{}
	}
	return attempts[len(attempts)-1].Selection
}

func playgroundRouteAttempts(attempts []RouteAttempt) []PlaygroundRouteAttempt {
	out := make([]PlaygroundRouteAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		out = append(out, PlaygroundRouteAttempt{
			Route:  playgroundRouteSummary(attempt.Selection),
			Status: attempt.Status,
			Code:   attempt.ErrorCode,
			Error:  attempt.Error,
		})
	}
	return out
}

func playgroundRouteSummary(route RouteSelection) PlaygroundRouteSummary {
	summary := PlaygroundRouteSummary{
		RouteID:          route.Route.ID,
		ProviderID:       route.Provider.ID,
		ProviderName:     route.Provider.Name,
		ResourceID:       routeResourceID(route),
		ProviderModel:    route.ProviderModel,
		Priority:         route.Route.Priority,
		ResourcePriority: routeResourcePriority(route),
		Weight:           routeWeight(route.Route),
		QualityScore:     routeQualityScore(route.Route),
		CostScore:        routeCostScore(route.Route),
		Strategy:         routeStrategy(route.Route),
	}
	if route.Resource != nil {
		summary.ResourceName = route.Resource.Name
	}
	return summary
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) writeRouteHeaders(w http.ResponseWriter, call CallContext, route RouteSelection, attempts int) {
	w.Header().Set("x-tokenhub-project-id", call.Project.ID)
	w.Header().Set("x-tokenhub-provider", route.Provider.ID)
	if resourceID := routeResourceID(route); resourceID != "" {
		w.Header().Set("x-tokenhub-provider-resource-id", resourceID)
	}
	w.Header().Set("x-tokenhub-model", route.ProviderModel)
	w.Header().Set("x-tokenhub-route-id", route.Route.ID)
	w.Header().Set("x-tokenhub-route-attempts", strconv.Itoa(attempts))
}

func (s *Server) authenticate(r *http.Request) (Project, APIKey, error) {
	auth := r.Header.Get("authorization")
	if auth == "" {
		return Project{}, APIKey{}, ErrInvalidAPIKey
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return Project{}, APIKey{}, ErrInvalidAPIKey
	}
	return s.store.ValidateAPIKey(strings.TrimSpace(strings.TrimPrefix(auth, prefix)), s.clientIP(r))
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if strings.TrimSpace(req.Identity) == "" || req.Password == "" {
		writeError(w, r, NewHTTPError(400, "invalid_request", "identity and password are required"))
		return
	}
	user, session, err := s.store.AuthenticateAdminUser(req.Identity, req.Password, 12*time.Hour)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       user,
	})
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if strings.TrimSpace(req.Token) == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, r, NewHTTPError(400, "invalid_reset_request", "token and password are required"))
		return
	}
	if len(req.Password) < 8 {
		writeError(w, r, NewHTTPError(400, "weak_password", "Password must be at least 8 characters"))
		return
	}
	user, err := s.store.ResetAdminUserPassword(req.Token, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	token := bearerToken(r)
	if token != "" && token != strings.TrimSpace(s.config.AdminToken) {
		s.store.RevokeAdminSession(token)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

type adminAuthIdentityProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	ProviderType string `json:"provider_type"`
	IssuerURL    string `json:"issuer_url,omitempty"`
	IconKey      string `json:"icon_key,omitempty"`
}

type oauthStatePayload struct {
	ProviderID  string `json:"provider_id"`
	ReturnURL   string `json:"return_url"`
	RedirectURI string `json:"redirect_uri"`
	ExpiresAt   int64  `json:"expires_at"`
	Nonce       string `json:"nonce"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope,omitempty"`
}

func (s *Server) handleAdminAuthIdentityProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	providers := []adminAuthIdentityProvider{}
	for _, item := range s.activeOAuthIdentityProviders() {
		providers = append(providers, adminAuthIdentityProvider{
			ID:           item.ID,
			Name:         item.Name,
			DisplayName:  identityProviderDisplayName(item),
			ProviderType: strings.ToLower(strings.TrimSpace(stringField(item.Fields, "provider_type"))),
			IssuerURL:    strings.TrimSpace(stringField(item.Fields, "issuer_url")),
			IconKey:      identityProviderIconKey(item),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": providers})
}

func (s *Server) handleAdminOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	provider, ok := s.findActiveOAuthIdentityProvider(r.URL.Query().Get("id"))
	if !ok {
		writeError(w, r, NewHTTPError(404, "identity_provider_not_found", "Identity provider not found"))
		return
	}
	authorizeURL := strings.TrimSpace(stringField(provider.Fields, "authorize_url"))
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	if authorizeURL == "" || clientID == "" {
		writeError(w, r, NewHTTPError(400, "identity_provider_incomplete", "Identity provider authorize URL and client ID are required"))
		return
	}
	redirectURI, err := identityProviderRedirectURI(provider, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	returnURL := safeOAuthReturnURL(r.URL.Query().Get("return_url"), r)
	state, err := s.signOAuthState(oauthStatePayload{
		ProviderID:  provider.ID,
		ReturnURL:   returnURL,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute).Unix(),
		Nonce:       NewID("oauth"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	target, err := buildOAuthAuthorizeURL(authorizeURL, clientID, redirectURI, identityProviderScopes(provider), state)
	if err != nil {
		writeError(w, r, err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleAdminOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	state, err := s.verifyOAuthState(r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_oauth_state", "OAuth state is invalid or expired"))
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "provider_error"), http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "missing_code"), http.StatusFound)
		return
	}
	provider, ok := s.findActiveOAuthIdentityProvider(state.ProviderID)
	if !ok {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "identity_provider_not_found"), http.StatusFound)
		return
	}
	token, err := s.exchangeOAuthCode(r.Context(), provider, code, state.RedirectURI)
	if err != nil {
		log.Printf("oauth token exchange failed provider_id=%s redirect_uri=%s error=%v", provider.ID, state.RedirectURI, err)
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, oauthErrorCode("token_exchange_failed", err)), http.StatusFound)
		return
	}
	claims, err := s.fetchOAuthUserInfo(r.Context(), provider, token.AccessToken)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "userinfo_failed"), http.StatusFound)
		return
	}
	user, err := s.upsertOAuthAdminUser(provider, claims)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "user_sync_failed"), http.StatusFound)
		return
	}
	_, session, err := s.store.CreateAdminSession(user.ID, 12*time.Hour)
	if err != nil {
		http.Redirect(w, r, oauthRedirectWithError(state.ReturnURL, "session_failed"), http.StatusFound)
		return
	}
	http.Redirect(w, r, oauthRedirectWithSession(state.ReturnURL, session), http.StatusFound)
}

func (s *Server) activeOAuthIdentityProviders() []AdminResource {
	items := []AdminResource{}
	for _, item := range s.store.ListResources("identity-providers") {
		if item.Status != StatusActive {
			continue
		}
		providerType := strings.ToLower(strings.TrimSpace(stringField(item.Fields, "provider_type")))
		if providerType != "oidc" && providerType != "oauth2" {
			continue
		}
		if strings.TrimSpace(stringField(item.Fields, "authorize_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "token_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "userinfo_url")) == "" ||
			strings.TrimSpace(stringField(item.Fields, "client_id")) == "" {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (s *Server) findActiveOAuthIdentityProvider(id string) (AdminResource, bool) {
	id = strings.TrimSpace(id)
	items := s.activeOAuthIdentityProviders()
	if id == "" && len(items) == 1 {
		return items[0], true
	}
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return AdminResource{}, false
}

func identityProviderScopes(provider AdminResource) string {
	raw := strings.TrimSpace(stringField(provider.Fields, "scopes"))
	if raw == "" {
		return "openid profile email"
	}
	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				out = append(out, value)
			}
		}
		return strings.Join(out, " ")
	}
	return raw
}

func identityProviderIconKey(provider AdminResource) string {
	configured := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "icon_key")))
	if configured != "" && configured != "auto" {
		return configured
	}
	providerType := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "provider_type")))
	fingerprint := strings.ToLower(strings.Join([]string{
		provider.Name,
		stringField(provider.Fields, "issuer_url"),
		stringField(provider.Fields, "authorize_url"),
		providerType,
	}, " "))
	for _, key := range []string{"gitlab", "github", "google", "microsoft", "azure", "entra", "okta", "keycloak"} {
		if strings.Contains(fingerprint, key) {
			if key == "azure" || key == "entra" {
				return "microsoft"
			}
			return key
		}
	}
	switch providerType {
	case "oidc", "oauth2", "saml", "ldap":
		return providerType
	default:
		return "sso"
	}
}

func identityProviderDisplayName(provider AdminResource) string {
	if label := strings.TrimSpace(stringField(provider.Fields, "login_label")); label != "" {
		return label
	}
	iconKey := identityProviderIconKey(provider)
	if label := identityProviderIconDisplayName(iconKey); label != "" {
		return label
	}
	if provider.Name != "" {
		return provider.Name
	}
	return identityProviderTypeLabel(strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "provider_type"))))
}

func identityProviderIconDisplayName(iconKey string) string {
	switch strings.ToLower(strings.TrimSpace(iconKey)) {
	case "gitlab":
		return "GitLab"
	case "github":
		return "GitHub"
	case "google":
		return "Google"
	case "microsoft":
		return "Microsoft"
	case "okta":
		return "Okta"
	case "keycloak":
		return "Keycloak"
	default:
		return ""
	}
}

func identityProviderTypeLabel(providerType string) string {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "oidc":
		return "OIDC"
	case "oauth2":
		return "OAuth2"
	case "saml":
		return "SAML"
	case "ldap":
		return "LDAP"
	default:
		return "SSO"
	}
}

func buildOAuthAuthorizeURL(authorizeURL string, clientID string, redirectURI string, scope string, state string) (string, error) {
	target, err := url.Parse(authorizeURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", NewHTTPError(400, "invalid_authorize_url", "Authorize URL is invalid")
	}
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(scope) != "" {
		query.Set("scope", scope)
	}
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func oauthCallbackURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-proto")); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if forwarded := firstForwardedValue(r.Header.Get("x-forwarded-host")); forwarded != "" {
		host = forwarded
	}
	return fmt.Sprintf("%s://%s/api/admin/auth/oauth/callback", scheme, host)
}

func identityProviderRedirectURI(provider AdminResource, r *http.Request) (string, error) {
	configured := strings.TrimSpace(stringField(provider.Fields, "redirect_uri"))
	if configured == "" {
		return oauthCallbackURL(r), nil
	}
	target, err := url.Parse(configured)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must be an absolute URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must use http or https")
	}
	if target.Fragment != "" {
		return "", NewHTTPError(400, "invalid_redirect_uri", "OAuth callback URL must not contain a fragment")
	}
	return configured, nil
}

func firstForwardedValue(value string) string {
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func safeOAuthReturnURL(raw string, r *http.Request) string {
	fallback := "http://localhost:3000/overview"
	if origin := strings.TrimSpace(r.Header.Get("origin")); origin != "" {
		fallback = strings.TrimRight(origin, "/") + "/overview"
	} else if referer := strings.TrimSpace(r.Header.Get("referer")); referer != "" {
		if parsed, err := url.Parse(referer); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			fallback = parsed.Scheme + "://" + parsed.Host + "/overview"
		}
	}
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return fallback
	}
	parsed, err := url.Parse(candidate)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fallback
	}
	if isAllowedOAuthReturnHost(parsed.Hostname(), r.Host) {
		return parsed.String()
	}
	return fallback
}

func isAllowedOAuthReturnHost(hostname string, requestHost string) bool {
	hostname = strings.ToLower(strings.Trim(hostname, "[]"))
	requestHostname := strings.ToLower(strings.Trim(strings.Split(requestHost, ":")[0], "[]"))
	switch hostname {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return hostname != "" && hostname == requestHostname
}

func (s *Server) signOAuthState(payload oauthStatePayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(s.oauthStateSecret()))
	_, _ = mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (s *Server) verifyOAuthState(state string) (oauthStatePayload, error) {
	parts := strings.Split(strings.TrimSpace(state), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	mac := hmac.New(sha256.New, []byte(s.oauthStateSecret()))
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, expected) {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oauthStatePayload{}, err
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return oauthStatePayload{}, err
	}
	if payload.ProviderID == "" || payload.ReturnURL == "" || payload.RedirectURI == "" || time.Now().UTC().Unix() > payload.ExpiresAt {
		return oauthStatePayload{}, fmt.Errorf("invalid oauth state")
	}
	return payload, nil
}

func (s *Server) oauthStateSecret() string {
	if secret := strings.TrimSpace(s.config.SecretKey); secret != "" {
		return secret
	}
	if secret := strings.TrimSpace(s.config.AdminToken); secret != "" {
		return secret
	}
	return "tokenhub-oauth-state"
}

func (s *Server) exchangeOAuthCode(ctx context.Context, provider AdminResource, code string, redirectURI string) (oauthTokenResponse, error) {
	tokenURL := strings.TrimSpace(stringField(provider.Fields, "token_url"))
	clientID := strings.TrimSpace(stringField(provider.Fields, "client_id"))
	clientSecret := strings.TrimSpace(stringField(provider.Fields, "client_secret"))
	if tokenURL == "" || clientID == "" {
		return oauthTokenResponse{}, NewHTTPError(400, "identity_provider_incomplete", "Identity provider token URL and client ID are required")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	token, detail, err := requestOAuthToken(ctx, tokenURL, form, "", "")
	if err == nil {
		if strings.TrimSpace(token.AccessToken) == "" {
			return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
		}
		return token, nil
	}
	if clientSecret == "" || !strings.Contains(detail, "invalid_client") {
		return oauthTokenResponse{}, err
	}
	log.Printf("oauth token exchange retrying with client_secret_basic after invalid_client")
	basicForm := url.Values{}
	basicForm.Set("grant_type", "authorization_code")
	basicForm.Set("code", code)
	basicForm.Set("redirect_uri", redirectURI)
	token, _, err = requestOAuthToken(ctx, tokenURL, basicForm, clientID, clientSecret)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, NewHTTPError(502, "oauth_token_missing", "OAuth token endpoint did not return an access token")
	}
	return token, nil
}

func requestOAuthToken(ctx context.Context, tokenURL string, form url.Values, basicClientID string, basicSecret string) (oauthTokenResponse, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	if basicClientID != "" || basicSecret != "" {
		req.SetBasicAuth(basicClientID, basicSecret)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return oauthTokenResponse{}, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := sanitizeOAuthErrorDetail(body)
		if detail != "" {
			return oauthTokenResponse{}, detail, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d: %s", resp.StatusCode, detail))
		}
		return oauthTokenResponse{}, detail, NewHTTPError(502, "oauth_token_failed", fmt.Sprintf("OAuth token endpoint returned %d", resp.StatusCode))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return oauthTokenResponse{}, "", err
	}
	return token, "", nil
}

func (s *Server) fetchOAuthUserInfo(ctx context.Context, provider AdminResource, accessToken string) (map[string]any, error) {
	userinfoURL := strings.TrimSpace(stringField(provider.Fields, "userinfo_url"))
	if userinfoURL == "" {
		return nil, NewHTTPError(400, "identity_provider_incomplete", "Identity provider userinfo URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewHTTPError(502, "oauth_userinfo_failed", fmt.Sprintf("OAuth userinfo endpoint returned %d", resp.StatusCode))
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Server) upsertOAuthAdminUser(provider AdminResource, claims map[string]any) (AdminUser, error) {
	usernameClaim := strings.TrimSpace(stringField(provider.Fields, "username_claim"))
	emailClaim := strings.TrimSpace(stringField(provider.Fields, "email_claim"))
	teamClaim := strings.TrimSpace(stringField(provider.Fields, "team_claim"))
	email := firstOAuthClaim(claims, emailClaim, "email", "public_email")
	if email == "" {
		return AdminUser{}, NewHTTPError(400, "oauth_email_missing", "OAuth userinfo did not include an email")
	}
	username := firstOAuthClaim(claims, usernameClaim, "preferred_username", "username", "nickname", "name")
	if username == "" {
		username = strings.Split(email, "@")[0]
	}
	name := firstOAuthClaim(claims, "name", "display_name", usernameClaim, "username")
	if name == "" {
		name = username
	}
	claimedTeamID := s.oauthTeamID(firstOAuthClaim(claims, teamClaim))
	defaultTeamID := s.oauthDefaultTeamID(provider)
	teamID := claimedTeamID
	if teamID == "" {
		teamID = defaultTeamID
	}
	users := s.store.ListAdminUsers()
	if existing, ok := findOAuthAdminUser(users, email, username); ok {
		if existing.Status != StatusActive {
			return AdminUser{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
		}
		patch := existing
		if name != "" {
			patch.Name = name
		}
		patch.Email = email
		if username != "" && !adminUsernameTaken(users, username, existing.ID) {
			patch.Username = username
		}
		if claimedTeamID != "" {
			patch.TeamID = claimedTeamID
		} else if strings.TrimSpace(patch.TeamID) == "" && defaultTeamID != "" {
			patch.TeamID = defaultTeamID
		}
		updated, err := s.store.UpdateAdminUser(existing.ID, patch, "")
		if err != nil {
			return AdminUser{}, err
		}
		s.assignOAuthDefaultProject(provider, updated)
		return updated, nil
	}
	username = uniqueOAuthUsername(users, username, email)
	user, err := s.store.CreateAdminUser(AdminUser{
		Username: username,
		Name:     name,
		Email:    email,
		Role:     oauthDefaultRole(provider),
		TeamID:   teamID,
		Status:   StatusActive,
	}, GenerateAdminSessionToken())
	if err != nil {
		return AdminUser{}, err
	}
	s.assignOAuthDefaultProject(provider, user)
	return user, nil
}

func firstOAuthClaim(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := oauthClaimString(claims, key); value != "" {
			return value
		}
	}
	return ""
}

func oauthClaimString(claims map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || claims == nil {
		return ""
	}
	var value any = claims
	for _, part := range strings.Split(key, ".") {
		fields, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value, ok = fields[part]
		if !ok || value == nil {
			return ""
		}
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func findOAuthAdminUser(users []AdminUser, email string, username string) (AdminUser, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.ToLower(strings.TrimSpace(username))
	for _, user := range users {
		if email != "" && strings.ToLower(strings.TrimSpace(user.Email)) == email {
			return user, true
		}
	}
	for _, user := range users {
		if username != "" && strings.ToLower(strings.TrimSpace(user.Username)) == username {
			return user, true
		}
	}
	return AdminUser{}, false
}

func adminUsernameTaken(users []AdminUser, username string, allowedUserID string) bool {
	username = strings.ToLower(strings.TrimSpace(username))
	for _, user := range users {
		if user.ID != allowedUserID && strings.ToLower(strings.TrimSpace(user.Username)) == username {
			return true
		}
	}
	return false
}

func uniqueOAuthUsername(users []AdminUser, preferred string, email string) string {
	base := strings.TrimSpace(preferred)
	if base == "" {
		base = strings.Split(strings.TrimSpace(email), "@")[0]
	}
	if base == "" {
		base = "oauth-user"
	}
	if !adminUsernameTaken(users, base, "") {
		return base
	}
	for index := 2; index < 1000; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if !adminUsernameTaken(users, candidate, "") {
			return candidate
		}
	}
	return base + "-" + NewID("oauth")
}

func (s *Server) oauthTeamID(claimValue string) string {
	normalized := normalizeScopeValue(claimValue)
	if normalized == "" {
		return ""
	}
	for _, team := range s.store.ListResources("teams") {
		for _, value := range []string{
			team.ID,
			team.Name,
			stringField(team.Fields, "name"),
			stringField(team.Fields, "code"),
			stringField(team.Fields, "team_id"),
			stringField(team.Fields, "team_name"),
		} {
			if normalizeScopeValue(value) == normalized {
				return team.ID
			}
		}
	}
	return ""
}

func oauthDefaultRole(provider AdminResource) string {
	role := normalizeAdminRole(stringField(provider.Fields, "default_role"))
	switch role {
	case "team_leader":
		return "team_leader"
	default:
		return "user"
	}
}

func (s *Server) oauthDefaultTeamID(provider AdminResource) string {
	return s.oauthTeamID(firstStringField(provider.Fields, "default_team_id", "default_team", "default_team_name"))
}

func (s *Server) oauthDefaultProject(provider AdminResource) (Project, bool) {
	return s.oauthProject(firstStringField(provider.Fields, "default_project_id", "default_project", "default_project_name"))
}

func (s *Server) oauthProject(value string) (Project, bool) {
	normalized := normalizeScopeValue(value)
	if normalized == "" {
		return Project{}, false
	}
	for _, project := range s.store.ListProjects() {
		if project.Status != "" && project.Status != StatusActive {
			continue
		}
		for _, candidate := range []string{project.ID, project.Name} {
			if normalizeScopeValue(candidate) == normalized {
				return project, true
			}
		}
	}
	return Project{}, false
}

func oauthDefaultProjectRole(provider AdminResource) string {
	role := strings.ToLower(strings.TrimSpace(stringField(provider.Fields, "default_project_role")))
	switch role {
	case "viewer", "developer", "maintainer":
		return role
	default:
		return "developer"
	}
}

func (s *Server) assignOAuthDefaultProject(provider AdminResource, user AdminUser) {
	project, ok := s.oauthDefaultProject(provider)
	if !ok || strings.TrimSpace(user.ID) == "" {
		return
	}
	for _, item := range s.store.ListResources("project-members") {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == project.ID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == user.ID {
			return
		}
	}
	role := oauthDefaultProjectRole(provider)
	displayName := user.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = user.Username
	}
	s.store.CreateResource("project-members", AdminResource{
		Name:   fmt.Sprintf("%s / %s", project.Name, displayName),
		Status: StatusActive,
		Fields: map[string]any{
			"project_id":      project.ID,
			"user_id":         user.ID,
			"role":            role,
			"can_issue_keys":  projectMemberRoleCanIssueKey(role),
			"provisioned_by":  "oauth_default_project",
			"identity_source": provider.ID,
		},
	})
}

func oauthRedirectWithSession(returnURL string, session AdminSession) string {
	values := url.Values{}
	values.Set("oauth_token", session.Token)
	values.Set("oauth_expires_at", session.ExpiresAt.Format(time.RFC3339))
	return oauthRedirectWithFragment(returnURL, values)
}

func oauthRedirectWithError(returnURL string, code string) string {
	values := url.Values{}
	values.Set("oauth_error", code)
	return oauthRedirectWithFragment(returnURL, values)
}

func oauthErrorCode(code string, err error) string {
	if err == nil {
		return code
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return code
	}
	detail = strings.ReplaceAll(detail, "\n", " ")
	detail = strings.ReplaceAll(detail, "\r", " ")
	if len(detail) > 160 {
		detail = detail[:160]
	}
	return code + ": " + detail
}

func sanitizeOAuthErrorDetail(body []byte) string {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		parts := []string{}
		for _, key := range []string{"error", "error_description", "error_uri", "message"} {
			if value, ok := parsed[key].(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", key, strings.TrimSpace(value)))
			}
		}
		if len(parts) > 0 {
			raw = strings.Join(parts, "; ")
		}
	}
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\r", " ")
	if len(raw) > 240 {
		raw = raw[:240]
	}
	return raw
}

func oauthRedirectWithFragment(returnURL string, values url.Values) string {
	target, err := url.Parse(returnURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		target, _ = url.Parse("http://localhost:3000/overview")
	}
	query := target.Query()
	for key, items := range values {
		query.Del(key)
		for _, item := range items {
			query.Add(key, item)
		}
	}
	target.RawQuery = query.Encode()
	target.Fragment = values.Encode()
	return target.String()
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "overview", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	providers := []Provider{}
	providerResources := []ProviderResource{}
	models := []Model{}
	alerts := []AlertEvent{}
	if s.canViewGlobalOperations(user) {
		providers = s.store.ListProviders()
		providerResources = s.store.ListProviderResources()
		alerts = s.store.ListAlerts()
	}
	models = s.accessibleModelsForAdminUser(user)
	routes := []ModelRoute{}
	if s.canViewGlobalOperations(user) {
		routes = s.store.ListRoutes()
	}
	activeRoutes := 0
	for _, route := range routes {
		if route.Status == StatusActive {
			activeRoutes++
		}
	}
	summary := s.usageSummaryForUser(user)
	summary["api_key_count"] = len(s.filterAPIKeysForUser(user, s.store.ListAPIKeys()))
	summary["route_count"] = len(routes)
	summary["active_route_count"] = activeRoutes
	summary["user_count"] = len(s.filterAdminUsersForUser(user, s.store.ListAdminUsers()))
	writeJSON(w, http.StatusOK, map[string]any{
		"summary":            summary,
		"projects":           s.filterProjectsForUser(user, s.store.ListProjects()),
		"providers":          providers,
		"provider_resources": providerResources,
		"models":             models,
		"alerts":             alerts,
	})
}

func (s *Server) handleAdminProjects(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "project", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.filterProjectsForUser(user, s.store.ListProjects())})
	case http.MethodPost:
		var req Project
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if normalizeAdminRole(user.Role) == "team_leader" {
			if strings.TrimSpace(user.TeamID) == "" {
				writeError(w, r, NewHTTPError(403, "team_required", "Team leader must belong to a team"))
				return
			}
			req.TeamID = user.TeamID
			if strings.TrimSpace(req.OwnerUserID) == "" {
				req.OwnerUserID = user.ID
			}
		}
		project := s.store.CreateProject(req)
		s.recordAdminAudit(r, user, "create", "project", project.ID, "", project)
		writeJSON(w, http.StatusCreated, project)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProjectNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/projects/"), "/")
	projectID := parts[0]
	if projectID == "" {
		writeError(w, r, NewHTTPError(400, "project_required", "Project ID is required"))
		return
	}
	permission := "project"
	if len(parts) == 2 && parts[1] == "keys" {
		permission = "api_key"
	}
	if len(parts) == 2 && parts[1] == "quota-increase" {
		permission = "approval"
	}
	if !canAdmin(user.Role, permission, r.Method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return
	}
	if len(parts) == 2 && parts[1] == "quota-increase" {
		s.handleAdminProjectQuotaIncrease(w, r, user, projectID)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var req Project
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			if normalizeAdminRole(user.Role) == "team_leader" {
				existing, err := s.findProject(projectID)
				if err != nil {
					writeError(w, r, err)
					return
				}
				if !s.canAccessProject(user, existing) {
					writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
					return
				}
				req.TeamID = user.TeamID
				if strings.TrimSpace(req.OwnerUserID) == "" {
					req.OwnerUserID = existing.OwnerUserID
				}
			}
			project, err := s.store.UpdateProject(projectID, req)
			if err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "update", "project", project.ID, "", project)
			writeJSON(w, http.StatusOK, project)
		case http.MethodDelete:
			if normalizeAdminRole(user.Role) == "team_leader" {
				existing, err := s.findProject(projectID)
				if err != nil {
					writeError(w, r, err)
					return
				}
				if !s.canAccessProject(user, existing) {
					writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
					return
				}
			}
			if err := s.store.DeleteProject(projectID); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "project", projectID, "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if len(parts) != 2 || parts[1] != "keys" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.canUseProjectForAPIKey(user, projectID) {
			writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAPIKeysForUser(user, s.store.ListProjectKeys(projectID))})
	case http.MethodPost:
		if !s.canUseProjectForAPIKey(user, projectID) {
			writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
			return
		}
		s.handleAdminAPIKeyCreate(w, r, user, projectID)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAdminUsersForUser(actor, s.store.ListAdminUsers())})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Name     string `json:"name"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			TeamID   string `json:"team_id"`
			Status   string `json:"status"`
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if normalizeAdminRole(actor.Role) == "team_leader" {
			if req.TeamID != "" && req.TeamID != actor.TeamID {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage own team"))
				return
			}
			req.TeamID = actor.TeamID
			if normalizeAdminRole(req.Role) != "user" {
				writeError(w, r, NewHTTPError(403, "role_forbidden", "Team leader can only create ordinary users"))
				return
			}
		}
		user, err := s.store.CreateAdminUser(AdminUser{
			Username: req.Username,
			Name:     req.Name,
			Email:    req.Email,
			Role:     req.Role,
			TeamID:   req.TeamID,
			Status:   req.Status,
		}, req.Password)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, actor, "create", "admin_user", user.ID, "", user)
		writeJSON(w, http.StatusCreated, user)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

type adminUserImportItem struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TeamID   string `json:"team_id"`
	Status   string `json:"status"`
}

func (s *Server) handleAdminUsersImport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Source  string                `json:"source"`
		Format  string                `json:"format"`
		Content string                `json:"content"`
		Users   []adminUserImportItem `json:"users"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	users := req.Users
	if strings.TrimSpace(req.Content) != "" {
		parsed, err := parseAdminUserImportCSV(req.Content)
		if err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_import", err.Error()))
			return
		}
		users = append(users, parsed...)
	}
	if len(users) == 0 {
		writeError(w, r, NewHTTPError(400, "invalid_import", "no users to import"))
		return
	}
	mailChannel, err := s.resolvePasswordResetMailChannel()
	if err != nil {
		writeError(w, r, err)
		return
	}

	existing := s.store.ListAdminUsers()
	result := map[string]any{
		"source":            strings.TrimSpace(req.Source),
		"format":            strings.TrimSpace(req.Format),
		"created":           0,
		"updated":           0,
		"skipped":           0,
		"reset_emails_sent": 0,
		"errors":            []string{},
		"users":             []AdminUser{},
	}
	importedUsers := []AdminUser{}
	errors := []string{}
	created := 0
	updated := 0
	resetEmailsSent := 0
	skipped := 0

	for index, item := range users {
		normalized, err := normalizeAdminUserImportItem(actor, item)
		if err != nil {
			skipped++
			errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
			continue
		}
		if normalizeAdminRole(actor.Role) == "team_leader" {
			if normalized.TeamID != actor.TeamID {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: team leader can only import own team", index+1))
				continue
			}
			if normalizeAdminRole(normalized.Role) != "user" {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: team leader can only import ordinary users", index+1))
				continue
			}
		}

		if current, ok := findImportedAdminUser(existing, normalized); ok {
			if normalizeAdminRole(actor.Role) == "team_leader" && current.TeamID != actor.TeamID {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: existing user is outside current team", index+1))
				continue
			}
			user, err := s.store.UpdateAdminUser(current.ID, normalized, "")
			if err != nil {
				skipped++
				errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
				continue
			}
			importedUsers = append(importedUsers, user)
			updated++
			if err := s.sendAdminPasswordResetEmail(r, mailChannel, user, actor.ID); err != nil {
				errors = append(errors, fmt.Sprintf("row %d: reset email failed: %s", index+1, err.Error()))
			} else {
				resetEmailsSent++
			}
			for i := range existing {
				if existing[i].ID == user.ID {
					existing[i] = user
					break
				}
			}
			continue
		}

		user, err := s.store.CreateAdminUser(normalized, NewID("sso"))
		if err != nil {
			skipped++
			errors = append(errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
			continue
		}
		importedUsers = append(importedUsers, user)
		existing = append(existing, user)
		created++
		if err := s.sendAdminPasswordResetEmail(r, mailChannel, user, actor.ID); err != nil {
			errors = append(errors, fmt.Sprintf("row %d: reset email failed: %s", index+1, err.Error()))
		} else {
			resetEmailsSent++
		}
	}

	result["created"] = created
	result["updated"] = updated
	result["skipped"] = skipped
	result["reset_emails_sent"] = resetEmailsSent
	result["errors"] = errors
	result["users"] = importedUsers
	s.recordAdminAudit(r, actor, "import", "admin_user", "", "", result)
	writeJSON(w, http.StatusOK, result)
}

func parseAdminUserImportCSV(content string) ([]adminUserImportItem, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("csv must include at least one user row")
	}
	headers := map[string]int{}
	for index, header := range records[0] {
		headers[normalizeImportHeader(header)] = index
	}
	hasHeader := hasAdminUserImportHeader(headers)
	value := func(record []string, names ...string) string {
		for _, name := range names {
			if index, ok := headers[name]; ok && index < len(record) {
				return strings.TrimSpace(record[index])
			}
		}
		return ""
	}
	items := make([]adminUserImportItem, 0, len(records))
	start := 0
	if hasHeader {
		start = 1
	}
	for _, record := range records[start:] {
		if len(record) == 0 || strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}
		if hasHeader {
			items = append(items, adminUserImportItem{
				Username: value(record, "username"),
				Name:     value(record, "name"),
				Email:    value(record, "email"),
				Role:     value(record, "role"),
				TeamID:   value(record, "team_id", "team"),
				Status:   value(record, "status"),
			})
			continue
		}
		items = append(items, adminUserImportItemFromRecord(record))
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("csv must include at least one user row")
	}
	return items, nil
}

func hasAdminUserImportHeader(headers map[string]int) bool {
	for _, name := range []string{"username", "name", "email", "role", "team_id", "team", "status"} {
		if _, ok := headers[name]; ok {
			return true
		}
	}
	return false
}

func adminUserImportItemFromRecord(record []string) adminUserImportItem {
	field := func(index int) string {
		if index >= 0 && index < len(record) {
			return strings.TrimSpace(record[index])
		}
		return ""
	}
	return adminUserImportItem{
		Username: field(0),
		Name:     field(1),
		Email:    field(2),
		Role:     field(3),
		TeamID:   field(4),
		Status:   field(5),
	}
}

func normalizeImportHeader(header string) string {
	header = strings.TrimSpace(strings.ToLower(header))
	switch header {
	case "用户名", "账号", "工号":
		return "username"
	case "姓名", "名称", "昵称":
		return "name"
	case "邮箱", "邮件":
		return "email"
	case "角色":
		return "role"
	case "团队", "团队id", "部门", "部门id":
		return "team_id"
	case "状态":
		return "status"
	default:
		return strings.ReplaceAll(header, "-", "_")
	}
}

func normalizeAdminUserImportItem(actor AdminUser, item adminUserImportItem) (AdminUser, error) {
	email := strings.TrimSpace(item.Email)
	username := strings.TrimSpace(item.Username)
	if email == "" {
		return AdminUser{}, fmt.Errorf("email is required")
	}
	if username == "" {
		username = email
	}
	role := normalizeAdminRole(item.Role)
	if role == "" {
		role = "user"
	}
	teamID := strings.TrimSpace(item.TeamID)
	if normalizeAdminRole(actor.Role) == "team_leader" {
		teamID = actor.TeamID
	}
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = StatusActive
	}
	return AdminUser{
		Username: username,
		Name:     strings.TrimSpace(item.Name),
		Email:    email,
		Role:     role,
		TeamID:   teamID,
		Status:   status,
	}, nil
}

func findImportedAdminUser(existing []AdminUser, user AdminUser) (AdminUser, bool) {
	email := strings.ToLower(strings.TrimSpace(user.Email))
	username := strings.ToLower(strings.TrimSpace(user.Username))
	for _, item := range existing {
		if email != "" && strings.ToLower(strings.TrimSpace(item.Email)) == email {
			return item, true
		}
		if username != "" && strings.ToLower(strings.TrimSpace(item.Username)) == username {
			return item, true
		}
	}
	return AdminUser{}, false
}

func (s *Server) handleAdminUserItem(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdmin(w, r, "identity", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	userID := parts[0]
	if len(parts) == 2 {
		if parts[1] != "reset-password-email" || r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
			return
		}
		s.handleAdminUserResetPasswordEmail(w, r, actor, userID)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req struct {
			Username string `json:"username"`
			Name     string `json:"name"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			TeamID   string `json:"team_id"`
			Status   string `json:"status"`
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if normalizeAdminRole(actor.Role) == "team_leader" {
			target, ok := s.findAdminUser(userID)
			if !ok || target.TeamID != actor.TeamID || normalizeAdminRole(target.Role) != "user" {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage ordinary users in own team"))
				return
			}
			if req.TeamID != "" && req.TeamID != actor.TeamID {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage own team"))
				return
			}
			req.TeamID = actor.TeamID
			if req.Role != "" && normalizeAdminRole(req.Role) != "user" {
				writeError(w, r, NewHTTPError(403, "role_forbidden", "Team leader cannot elevate user role"))
				return
			}
			req.Role = "user"
		}
		updatedUser, err := s.store.UpdateAdminUser(userID, AdminUser{
			Username: req.Username,
			Name:     req.Name,
			Email:    req.Email,
			Role:     req.Role,
			TeamID:   req.TeamID,
			Status:   req.Status,
		}, req.Password)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, actor, "update", "admin_user", userID, "", updatedUser)
		writeJSON(w, http.StatusOK, updatedUser)
	case http.MethodDelete:
		if normalizeAdminRole(actor.Role) == "team_leader" {
			target, ok := s.findAdminUser(userID)
			if !ok || target.TeamID != actor.TeamID || normalizeAdminRole(target.Role) != "user" {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only delete ordinary users in own team"))
				return
			}
		}
		if err := s.store.DeleteAdminUser(userID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, actor, "delete", "admin_user", userID, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminUserResetPasswordEmail(w http.ResponseWriter, r *http.Request, actor AdminUser, userID string) {
	target, ok := s.findAdminUser(userID)
	if !ok {
		writeError(w, r, NewHTTPError(404, "admin_user_not_found", "Admin user not found"))
		return
	}
	if normalizeAdminRole(actor.Role) == "team_leader" && (target.TeamID != actor.TeamID || normalizeAdminRole(target.Role) != "user") {
		writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only manage ordinary users in own team"))
		return
	}
	mailChannel, err := s.resolvePasswordResetMailChannel()
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.sendAdminPasswordResetEmail(r, mailChannel, target, actor.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, actor, "send_reset_password_email", "admin_user", userID, "", map[string]any{"email": target.Email})
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "user": target})
}

func (s *Server) handleAdminAPIKeys(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.filterAPIKeysForUser(user, s.store.ListAPIKeys())})
	case http.MethodPost:
		project, err := s.personalAPIKeyProject(user)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.handleAdminAPIKeyCreate(w, r, user, project.ID)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) personalAPIKeyProject(user AdminUser) (Project, error) {
	if normalizeAdminRole(user.Role) != "user" {
		return Project{}, NewHTTPError(400, "project_required", "Project ID is required")
	}
	for _, project := range s.store.ListProjects() {
		if project.ID == defaultProjectID || (project.Status != "" && project.Status != StatusActive) {
			continue
		}
		if s.canUseProjectForAPIKey(user, project.ID) {
			return project, nil
		}
	}
	project, ok := s.store.GetProject(defaultProjectID)
	if !ok || (project.Status != "" && project.Status != StatusActive) {
		return Project{}, NewHTTPError(409, "default_project_unavailable", "Default project is unavailable")
	}
	return project, nil
}

func (s *Server) handleAdminAPIKeyCreate(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	var req struct {
		Name          string      `json:"name"`
		Group         string      `json:"group"`
		AllowedModels []string    `json:"allowed_models"`
		IPAllowlist   []string    `json:"ip_allowlist"`
		Limits        QuotaLimits `json:"limits"`
		ExpiresAt     *time.Time  `json:"expires_at"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	payload := map[string]any{
		"project_id":       projectID,
		"name":             req.Name,
		"group":            req.Group,
		"allowed_models":   req.AllowedModels,
		"ip_allowlist":     req.IPAllowlist,
		"limits":           req.Limits,
		"expires_at":       req.ExpiresAt,
		"requested_action": "api_key_create",
	}
	if approval, required := s.approvalRequired(user, "api_key_create", "api_key", "", payload); required {
		s.recordAdminAudit(r, user, "request_approval", "api_key", approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	key, secret, err := s.store.CreateAPIKey(projectID, APIKey{
		Name:        req.Name,
		Group:       req.Group,
		Allowed:     req.AllowedModels,
		IPAllowlist: req.IPAllowlist,
		Limits:      req.Limits,
		ExpiresAt:   req.ExpiresAt,
		Status:      StatusActive,
		Metadata: map[string]string{
			"created_by":      user.ID,
			"created_by_role": normalizeAdminRole(user.Role),
		},
	}, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "api_key", key.ID, "", map[string]any{"project_id": key.ProjectID, "name": key.Name})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":                      key.ID,
		"api_key":                 secret,
		"name":                    key.Name,
		"project_id":              key.ProjectID,
		"plain_text_visible_once": true,
	})
}

func (s *Server) handleAdminAPIKeyItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "api_key", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/api-keys/"), "/"), "/")
	keyID := parts[0]
	if keyID == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if !s.canManageAPIKey(user, keyID) {
		writeError(w, r, NewHTTPError(403, "api_key_forbidden", "API key is not available for this user"))
		return
	}
	if len(parts) == 2 {
		if parts[1] != "rotate" {
			writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		var req struct {
			GraceUntil *time.Time `json:"grace_until"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
		}
		key, secret, err := s.store.RotateAPIKey(keyID, req.GraceUntil)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "rotate", "api_key", keyID, "", map[string]any{"new_key_id": key.ID})
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":                      key.ID,
			"api_key":                 secret,
			"name":                    key.Name,
			"project_id":              key.ProjectID,
			"rotated_from_id":         key.RotatedFromID,
			"plain_text_visible_once": true,
		})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req APIKey
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if approval, required := s.apiKeyUpdateApproval(user, keyID, req); required {
			s.recordAdminAudit(r, user, "request_approval", "api_key", approval.ID, "", approval)
			writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
			return
		}
		key, err := s.store.UpdateAPIKey(keyID, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "api_key", keyID, "", key)
		writeJSON(w, http.StatusOK, key)
	case http.MethodDelete:
		if err := s.store.DeleteAPIKey(keyID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "api_key", keyID, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProviders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviders()})
	case http.MethodPost:
		var req ProviderCreateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		provider, catalog, catalogSource, err := s.providerFromCreateRequest(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if provider.Name == "" || provider.Type == "" {
			writeError(w, r, NewHTTPError(400, "invalid_provider", "name and type are required"))
			return
		}
		created := s.store.AddProvider(provider)
		result := ProviderCreateResult{
			Provider:      created,
			CatalogSource: catalogSource,
		}
		if shouldCreateProviderRoutes(req, catalog, true) {
			result.CreatedRoutes, result.ModelNames, result.RouteIDs = s.createProviderCatalogRoutes(created.ID, catalog, req)
		}
		s.recordAdminAudit(r, user, "create", "provider", created.ID, "", result)
		writeJSON(w, http.StatusCreated, result)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProviderCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	refresh := r.URL.Query().Get("refresh") == "true"
	entries, source, err := LoadProviderCatalog(r.Context(), http.DefaultClient, refresh)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries, "source": source})
}

func (s *Server) handleAdminProviderCatalogItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/provider-catalog/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	refresh := r.URL.Query().Get("refresh") == "true"
	entry, source, ok, err := GetProviderCatalogEntry(r.Context(), http.DefaultClient, id, refresh)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !ok {
		writeError(w, r, NewHTTPError(404, "provider_catalog_not_found", "Provider catalog entry not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": source})
}

func (s *Server) providerFromCreateRequest(ctx context.Context, req ProviderCreateRequest) (Provider, ProviderCatalogEntry, string, error) {
	var catalog ProviderCatalogEntry
	catalogSource := ""
	catalogID := strings.TrimSpace(req.CatalogID)
	if catalogID != "" {
		entry, source, ok, err := GetProviderCatalogEntry(ctx, http.DefaultClient, catalogID, false)
		if err != nil {
			return Provider{}, ProviderCatalogEntry{}, source, err
		}
		if !ok {
			return Provider{}, ProviderCatalogEntry{}, source, NewHTTPError(400, "provider_catalog_not_found", "Provider catalog entry not found")
		}
		catalog = entry
		catalogSource = source
	}
	if catalog.ID == "custom" {
		catalog = s.customProviderCatalogFromStandardModels(req.ModelCategory)
	}
	id := strings.TrimSpace(req.ID)
	if id == "" && catalog.ID != "" && catalog.ID != "custom" {
		id = "prv_" + sanitizeIdentifier(catalog.ID)
	}
	provider := Provider{
		ID:       id,
		Name:     firstNonEmpty(req.Name, catalog.DisplayName, catalog.Name),
		Type:     firstNonEmpty(req.Type, catalog.Type, ProviderOpenAICompatible),
		BaseURL:  firstNonEmpty(req.BaseURL, catalog.BaseURL),
		APIKey:   req.APIKey,
		Status:   firstNonEmpty(req.Status, StatusActive),
		Healthy:  req.Healthy,
		Priority: req.Priority,
		Headers:  req.Headers,
		Options:  req.Options,
	}
	if provider.Priority == 0 {
		provider.Priority = 10
	}
	provider.BaseURL = normalizeProviderBaseURL(provider.ID, provider.BaseURL)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	if catalog.ID != "" {
		provider.Options["catalog_id"] = catalog.ID
		provider.Options["catalog_source"] = catalogSource
		if catalog.DocURL != "" {
			provider.Options["doc_url"] = catalog.DocURL
		}
	}
	if strings.TrimSpace(req.ModelCategory) != "" {
		provider.Options["model_category"] = strings.TrimSpace(req.ModelCategory)
	}
	return provider, catalog, catalogSource, nil
}

func (s *Server) createProviderCatalogRoutes(providerID string, catalog ProviderCatalogEntry, req ProviderCreateRequest) (int, []string, []string) {
	selected := map[string]bool{}
	for _, modelID := range req.SelectedModels {
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			selected[modelID] = true
		}
	}
	modelNames := []string{}
	routeIDs := []string{}
	category := strings.TrimSpace(req.ModelCategory)
	standardModelNames := standardModelNameSet(s.store.ListModels())
	existingRoutes := s.store.ListRoutes()
	existingRouteIDs := existingRouteIDSet(existingRoutes)
	routePriorities := routePriorityByModel(existingRoutes)
	for _, catalogModel := range catalog.Models {
		if len(selected) > 0 && !selected[catalogModel.ID] {
			continue
		}
		modelCategory := standardModelCategory(firstNonEmpty(catalogModel.Category, inferModelCategory(catalogModel.ID, catalogModel.DisplayName)))
		if category != "" && category != "all" && modelCategory != category {
			continue
		}
		route := ProviderCatalogModelRoute(providerID, catalogModel)
		if !standardModelNames[normalizeModelLookupName(route.ModelName)] {
			continue
		}
		if existingRouteIDs[route.ID] {
			continue
		}
		route.Priority = takeNextRoutePriority(routePriorities, route.ModelName)
		route = s.store.AddRoute(route)
		existingRouteIDs[route.ID] = true
		routeIDs = append(routeIDs, route.ID)
		modelNames = append(modelNames, route.ModelName)
	}
	return len(routeIDs), modelNames, routeIDs
}

func (s *Server) customProviderCatalogFromStandardModels(category string) ProviderCatalogEntry {
	models := []ProviderCatalogModel{}
	normalizedCategory := standardModelCategory(category)
	for _, model := range s.store.ListModels() {
		modelCategory := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.Name, model.Name)))
		if normalizedCategory != "" && normalizedCategory != "all" && modelCategory != normalizedCategory {
			continue
		}
		models = append(models, ProviderCatalogModel{
			ID:                     model.Name,
			Name:                   model.Name,
			DisplayName:            model.Name,
			CanonicalName:          model.Name,
			Category:               modelCategory,
			Family:                 model.Family,
			Type:                   model.Modality,
			ContextWindow:          model.ContextWindow,
			InputPriceUSDPer1M:     model.InputPriceUSDPer1M,
			CacheReadPriceUSDPer1M: model.CacheReadPriceUSDPer1M,
			OutputPriceUSDPer1M:    model.OutputPriceUSDPer1M,
			InputModalities:        append([]string(nil), model.InputModalities...),
			OutputModalities:       append([]string(nil), model.OutputModalities...),
			Capabilities:           append([]string(nil), model.Capabilities...),
			SupportedParameters:    append([]string(nil), model.SupportedParameters...),
			Metadata:               map[string]string{"source": "tokenhub-standard-catalog"},
		})
	}
	categories, categoryCounts := catalogCategorySummary(models)
	if len(models) == 0 {
		entry := customProviderCatalogEntry()
		entry.Categories = []string{firstNonEmpty(normalizedCategory, "custom")}
		entry.CategoryCounts = map[string]int{firstNonEmpty(normalizedCategory, "custom"): 0}
		entry.Models = nil
		entry.ModelsCount = 0
		return entry
	}
	entry := customProviderCatalogEntry()
	entry.Categories = categories
	entry.CategoryCounts = categoryCounts
	entry.Models = models
	entry.ModelsCount = len(models)
	return entry
}

func shouldCreateProviderRoutes(req ProviderCreateRequest, catalog ProviderCatalogEntry, isCreate bool) bool {
	if catalog.ID == "" || len(catalog.Models) == 0 {
		return false
	}
	if req.CreateRoutes != nil {
		return *req.CreateRoutes
	}
	return isCreate
}

func standardModelNameSet(models []Model) map[string]bool {
	set := map[string]bool{}
	for _, model := range models {
		for _, name := range []string{model.Name, model.ID} {
			normalized := normalizeModelLookupName(name)
			if normalized != "" {
				set[normalized] = true
			}
		}
	}
	return set
}

func existingRouteIDSet(routes []ModelRoute) map[string]bool {
	set := map[string]bool{}
	for _, route := range routes {
		id := strings.TrimSpace(route.ID)
		if id != "" {
			set[id] = true
		}
	}
	return set
}

func routePriorityByModel(routes []ModelRoute) map[string]int {
	priorities := map[string]int{}
	for _, route := range routes {
		modelName := strings.TrimSpace(route.ModelName)
		if modelName == "" {
			continue
		}
		if route.Priority > priorities[modelName] {
			priorities[modelName] = route.Priority
		}
	}
	return priorities
}

func takeNextRoutePriority(priorities map[string]int, modelName string) int {
	modelName = strings.TrimSpace(modelName)
	next := priorities[modelName] + 1
	if next <= 0 {
		next = 1
	}
	priorities[modelName] = next
	return next
}

func normalizeModelLookupName(value string) string {
	return canonicalModelName(value, value)
}

func (s *Server) handleAdminProviderNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/providers/"), "/")
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var req ProviderCreateRequest
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			provider, catalog, catalogSource, err := s.providerFromCreateRequest(r.Context(), req)
			if err != nil {
				writeError(w, r, err)
				return
			}
			provider.ID = parts[0]
			updated, err := s.store.UpdateProvider(parts[0], provider)
			if err != nil {
				writeError(w, r, err)
				return
			}
			result := ProviderCreateResult{
				Provider:      updated,
				CatalogSource: catalogSource,
			}
			if shouldCreateProviderRoutes(req, catalog, false) {
				result.CreatedRoutes, result.ModelNames, result.RouteIDs = s.createProviderCatalogRoutes(updated.ID, catalog, req)
			}
			s.recordAdminAudit(r, user, "update", "provider", parts[0], "", result)
			writeJSON(w, http.StatusOK, result)
		case http.MethodDelete:
			if err := s.store.DeleteProvider(parts[0]); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "provider", parts[0], "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if len(parts) != 2 || (parts[1] != "health" && parts[1] != "test" && parts[1] != "refresh-token") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	if parts[1] == "test" {
		provider, err := s.store.TestProvider(parts[0])
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "test", "provider", parts[0], "", map[string]any{"healthy": provider.Healthy})
		writeJSON(w, http.StatusOK, provider)
		return
	}
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	provider, err := s.store.SetProviderHealth(parts[0], req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider", parts[0], "", provider)
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) handleAdminProviderResources(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviderResources()})
	case http.MethodPost:
		var req ProviderResource
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if req.ProviderID == "" || req.Name == "" {
			writeError(w, r, NewHTTPError(400, "invalid_provider_resource", "provider_id and name are required"))
			return
		}
		resource, err := s.store.AddProviderResource(req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "provider_resource", resource.ID, "", resource)
		writeJSON(w, http.StatusCreated, resource)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProviderResourceNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/provider-resources/"), "/")
	if len(parts) == 1 && parts[0] == "bulk" {
		s.handleAdminProviderResourceBulk(w, r, user)
		return
	}
	if len(parts) == 1 && parts[0] == "import" {
		s.handleAdminProviderResourceImport(w, r, user)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var req ProviderResource
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			resource, err := s.store.UpdateProviderResource(parts[0], req)
			if err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "update", "provider_resource", parts[0], "", resource)
			writeJSON(w, http.StatusOK, resource)
		case http.MethodDelete:
			if err := s.store.DeleteProviderResource(parts[0]); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "provider_resource", parts[0], "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if len(parts) != 2 || (parts[1] != "health" && parts[1] != "test") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	if parts[1] == "test" {
		resource, err := s.store.TestProviderResource(parts[0])
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "test", "provider_resource", parts[0], "", map[string]any{"healthy": resource.Healthy})
		writeJSON(w, http.StatusOK, resource)
		return
	}
	if parts[1] == "refresh-token" {
		creds, err := s.store.RefreshProviderResourceCredentials(r.Context(), parts[0], true)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "refresh_token", "provider_resource", parts[0], "", providerAccountCredentialSummary(creds))
		writeJSON(w, http.StatusOK, map[string]any{"credential_summary": providerAccountCredentialSummary(creds)})
		return
	}
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	resource, err := s.store.SetProviderResourceHealth(parts[0], req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider_resource", parts[0], "", resource)
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) handleAdminProviderResourceBulk(w http.ResponseWriter, r *http.Request, user AdminUser) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	result, err := s.store.BulkOperateProviderResources(req.Action, req.IDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "bulk_"+req.Action, "provider_resource", strings.Join(req.IDs, ","), "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminProviderResourceImport(w http.ResponseWriter, r *http.Request, user AdminUser) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Resources []ProviderResource `json:"resources"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	result, err := s.store.ImportProviderResources(req.Resources)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "import", "provider_resource", "", "", result)
	status := http.StatusCreated
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (s *Server) handleAdminModels(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	if !canAdmin(user.Role, "model", r.Method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.accessibleModelsForAdminUser(user)})
	case http.MethodPost:
		var req struct {
			Model
			Routes []ModelRoute `json:"routes"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		model := s.store.AddModel(req.Model)
		for _, route := range req.Routes {
			route.ModelName = model.Name
			s.store.AddRoute(route)
		}
		s.recordAdminAudit(r, user, "create", "model", model.Name, "", model)
		writeJSON(w, http.StatusCreated, model)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminModelItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
		return
	}
	modelName := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/models/"), "/")
	if modelName == "" || strings.Contains(modelName, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req Model
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		model, err := s.store.UpdateModel(modelName, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "model", modelName, "", model)
		writeJSON(w, http.StatusOK, model)
	case http.MethodDelete:
		if err := s.store.DeleteModel(modelName); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "model", modelName, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListRoutes()})
	case http.MethodPost:
		var req ModelRoute
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if req.ModelName == "" || req.ProviderID == "" || req.ProviderModel == "" {
			writeError(w, r, NewHTTPError(400, "invalid_route", "model_name, provider_id and provider_model are required"))
			return
		}
		if req.Priority <= 0 {
			req.Priority = takeNextRoutePriority(routePriorityByModel(s.store.ListRoutes()), req.ModelName)
		}
		route := s.store.AddRoute(req)
		s.recordAdminAudit(r, user, "create", "routing_rule", route.ID, "", route)
		writeJSON(w, http.StatusCreated, route)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminRouteItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/routing-rules/"), "/"), "/")
	routeID := parts[0]
	if routeID == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if len(parts) == 2 {
		if parts[1] != "explain" {
			writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		modelName := r.URL.Query().Get("model")
		if modelName == "" {
			writeError(w, r, NewHTTPError(400, "missing_model", "model query is required"))
			return
		}
		routes, err := s.store.SelectRouteCandidates(modelName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		call := CallContext{RequestID: NewID("exp"), Project: Project{ID: r.URL.Query().Get("project_id")}, Key: APIKey{ID: r.URL.Query().Get("api_key_id")}}
		planned := s.planRouteOrder(call, routes)
		steps := make([]RouteExplainStep, 0, len(planned))
		for _, route := range planned {
			steps = append(steps, RouteExplainStep{
				RouteID:          route.Route.ID,
				ProviderID:       route.Provider.ID,
				ResourceID:       routeResourceID(route),
				ProviderModel:    route.ProviderModel,
				Priority:         route.Route.Priority,
				ResourcePriority: routeResourcePriority(route),
				Weight:           routeWeight(route.Route),
				QualityScore:     routeQualityScore(route.Route),
				CostScore:        routeCostScore(route.Route),
				Strategy:         routeStrategy(route.Route),
				Status:           "candidate",
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": steps})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req ModelRoute
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		route, err := s.store.UpdateRoute(routeID, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "routing_rule", routeID, "", route)
		writeJSON(w, http.StatusOK, route)
	case http.MethodDelete:
		if err := s.store.DeleteRoute(routeID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "routing_rule", routeID, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminResources(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, adminResourcePermission(r.URL.Path), r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/resources/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	kind := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if kind == "monitors" {
				s.ensureDefaultMonitors()
			}
			if kind == "alert-rules" {
				s.ensureDefaultAlertRules()
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": s.filterResourcesForUser(user, kind, s.store.ListResources(kind))})
		case http.MethodPost:
			if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
				writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot create teams"))
				return
			}
			var req AdminResource
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			if req.Name == "" {
				writeError(w, r, NewHTTPError(400, "invalid_resource", "name is required"))
				return
			}
			if err := s.validateScopedResourceMutation(user, kind, "", req); err != nil {
				writeError(w, r, err)
				return
			}
			if approval, required := s.adminResourceApproval(user, kind, "", req); required {
				s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
				writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
				return
			}
			resource := s.store.CreateResource(kind, req)
			s.recordAdminAudit(r, user, "create", kind, resource.ID, "", resource)
			writeJSON(w, http.StatusCreated, resource)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if kind == "invoices" && len(parts) == 3 && parts[1] != "" {
		s.handleAdminInvoiceAction(w, r, user, parts[1], parts[2])
		return
	}
	if kind == "monitors" && len(parts) == 3 && parts[1] != "" && parts[2] == "run" {
		s.handleAdminMonitorRun(w, r, user, parts[1])
		return
	}
	if len(parts) != 2 || parts[1] == "" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" && parts[1] != user.TeamID {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader can only update own team"))
			return
		}
		var req AdminResource
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if err := s.validateScopedResourceMutation(user, kind, parts[1], req); err != nil {
			writeError(w, r, err)
			return
		}
		if approval, required := s.adminResourceApproval(user, kind, parts[1], req); required {
			s.recordAdminAudit(r, user, "request_approval", kind, approval.ID, "", approval)
			writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
			return
		}
		resource, err := s.store.UpdateResource(kind, parts[1], req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", kind, parts[1], "", resource)
		writeJSON(w, http.StatusOK, resource)
	case http.MethodDelete:
		if normalizeAdminRole(user.Role) == "team_leader" && kind == "teams" {
			writeError(w, r, NewHTTPError(403, "team_forbidden", "Team leader cannot delete teams"))
			return
		}
		if err := s.store.DeleteResource(kind, parts[1]); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", kind, parts[1], "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProjectQuotaIncrease(w http.ResponseWriter, r *http.Request, user AdminUser, projectID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		writeError(w, r, NewHTTPError(404, "project_not_found", "Project not found"))
		return
	}
	if !s.canAccessProject(user, project) {
		writeError(w, r, NewHTTPError(403, "project_forbidden", "Project is not available for this user"))
		return
	}
	var req AdminResource
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("%s 项目额度提升", project.Name)
	}
	if req.Description == "" {
		req.Description = "项目空间发起的额度提升申请"
	}
	if req.Status == "" {
		req.Status = StatusActive
	}
	fields := req.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	fields["scope"] = "project"
	fields["scope_id"] = project.ID
	req.Fields = fields
	resourceID := ""
	if quota, ok := s.projectQuotaPolicy(project); ok {
		resourceID = quota.ID
	}
	payload := map[string]any{
		"kind":             "quota-policies",
		"resource_id":      resourceID,
		"project_id":       project.ID,
		"name":             req.Name,
		"description":      req.Description,
		"status":           req.Status,
		"fields":           req.Fields,
		"requested_action": "quota_increase",
	}
	flowID := ""
	if flow, ok := s.matchApprovalFlow("quota_increase", payload); ok {
		flowID = flow.ID
	}
	approval := s.createApprovalRequest(user, flowID, "quota_increase", "quota-policies", resourceID, payload)
	s.recordAdminAudit(r, user, "request_approval", "quota-policies", approval.ID, "", approval)
	writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
}

func (s *Server) ensureDefaultMonitors() {
	existing := s.store.ListResources("monitors")
	existingIDs := map[string]bool{}
	existingTargets := map[string]bool{}
	createdIDs := map[string]bool{}
	for _, item := range existing {
		existingIDs[item.ID] = true
		if key := monitorTargetKey(item.Fields); key != "" {
			existingTargets[key] = true
		}
	}
	for _, item := range s.defaultMonitorResources(existingIDs, existingTargets) {
		created := s.store.CreateResource("monitors", item)
		_, _ = s.store.RunMonitor(created.ID)
		existingIDs[created.ID] = true
		createdIDs[created.ID] = true
		if key := monitorTargetKey(created.Fields); key != "" {
			existingTargets[key] = true
		}
	}
	s.runDueMonitors(createdIDs)
}

func (s *Server) defaultMonitorResources(existingIDs map[string]bool, existingTargets map[string]bool) []AdminResource {
	now := time.Now().UTC()
	items := []AdminResource{}
	add := func(targetKey string, id string, name string, description string, fields map[string]any) {
		if targetKey == "" || existingTargets[targetKey] || existingIDs[id] {
			return
		}
		fields["managed_by"] = "tokenhub_auto"
		fields["auto_key"] = targetKey
		fields["interval_seconds"] = defaultFloatField(fields, "interval_seconds", 60)
		items = append(items, AdminResource{
			ID:          id,
			Name:        name,
			Description: description,
			Status:      StatusActive,
			Fields:      fields,
			CreatedAt:   now,
		})
	}
	for _, provider := range s.store.ListProviders() {
		add(
			"provider:"+provider.ID,
			autoMonitorID("provider", provider.ID),
			fmt.Sprintf("%s Provider Connectivity", provider.Name),
			"System default check for whether the Provider is enabled and can participate in routing.",
			map[string]any{
				"target_type": "provider",
				"provider_id": provider.ID,
			},
		)
	}
	for _, resource := range s.store.ListProviderResources() {
		add(
			"resource:"+resource.ID,
			autoMonitorID("resource", resource.ID),
			fmt.Sprintf("%s Resource Health", resource.Name),
			"System default check for Provider resource availability.",
			map[string]any{
				"target_type":          "resource",
				"provider_id":          resource.ProviderID,
				"provider_resource_id": resource.ID,
			},
		)
	}
	seenModels := map[string]bool{}
	for _, route := range s.store.ListRoutes() {
		modelName := strings.TrimSpace(route.ModelName)
		if modelName == "" || route.Status != StatusActive || seenModels[modelName] {
			continue
		}
		seenModels[modelName] = true
		add(
			"model:"+modelName,
			autoMonitorID("model", modelName),
			fmt.Sprintf("%s Model Route Heartbeat", modelName),
			"System default check for whether the model API has an enabled route.",
			map[string]any{
				"target_type": "model",
				"model":       modelName,
			},
		)
	}
	return items
}

func autoMonitorID(kind string, target string) string {
	return fmt.Sprintf("mon_auto_%s_%d", kind, stableHashInt(target, 91))
}

func monitorTargetKey(fields map[string]any) string {
	targetType := strings.ToLower(strings.TrimSpace(stringField(fields, "target_type")))
	if targetType == "" {
		targetType = inferMonitorTargetType(fields)
	}
	switch targetType {
	case "provider":
		if providerID := strings.TrimSpace(firstStringField(fields, "provider_id", "provider")); providerID != "" {
			return "provider:" + providerID
		}
	case "resource", "provider_resource":
		if resourceID := strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource")); resourceID != "" {
			return "resource:" + resourceID
		}
	case "model":
		if modelName := strings.TrimSpace(firstStringField(fields, "model", "model_name")); modelName != "" {
			return "model:" + modelName
		}
	}
	return ""
}

func defaultFloatField(fields map[string]any, key string, fallback float64) float64 {
	if value := float64Field(fields, key); value > 0 {
		return value
	}
	return fallback
}

func (s *Server) runDueMonitors(skip map[string]bool) {
	now := time.Now().UTC()
	for _, item := range s.store.ListResources("monitors") {
		if skip[item.ID] || item.Status != StatusActive || !monitorRunDue(item, now) {
			continue
		}
		_, _ = s.store.RunMonitor(item.ID)
	}
}

func monitorRunDue(item AdminResource, now time.Time) bool {
	intervalSeconds := defaultFloatField(item.Fields, "interval_seconds", 60)
	if intervalSeconds < 1 {
		intervalSeconds = 60
	}
	lastCheckedText := strings.TrimSpace(stringField(item.Fields, "last_checked_at"))
	if lastCheckedText == "" {
		return true
	}
	lastChecked, err := time.Parse(time.RFC3339, lastCheckedText)
	if err != nil {
		return true
	}
	return now.Sub(lastChecked) >= time.Duration(intervalSeconds)*time.Second
}

func (s *Server) ensureDefaultAlertRules() {
	existing := s.store.ListResources("alert-rules")
	existingIDs := map[string]bool{}
	existingKeys := map[string]bool{}
	for _, item := range existing {
		existingIDs[item.ID] = true
		if key := alertRuleKey(item.Fields); key != "" {
			existingKeys[key] = true
		}
	}
	for _, item := range defaultAlertRuleResources(existingIDs, existingKeys) {
		created := s.store.CreateResource("alert-rules", item)
		existingIDs[created.ID] = true
		if key := alertRuleKey(created.Fields); key != "" {
			existingKeys[key] = true
		}
	}
}

func defaultAlertRuleResources(existingIDs map[string]bool, existingKeys map[string]bool) []AdminResource {
	now := time.Now().UTC()
	items := []AdminResource{}
	add := func(ruleKey string, id string, name string, description string, metric string, threshold string, severity string, scope string, eventCodes []string) {
		if ruleKey == "" || existingKeys[ruleKey] || existingIDs[id] {
			return
		}
		fields := map[string]any{
			"rule_key":    ruleKey,
			"metric":      metric,
			"threshold":   threshold,
			"severity":    severity,
			"scope":       scope,
			"channel":     "default",
			"event_codes": strings.Join(eventCodes, ","),
			"managed_by":  "tokenhub_auto",
		}
		items = append(items, AdminResource{
			ID:          id,
			Name:        name,
			Description: description,
			Status:      StatusActive,
			Fields:      fields,
			CreatedAt:   now,
		})
	}
	add(
		"provider_health_failed",
		"alr_default_provider_health",
		"Provider Unavailable Alert",
		"Triggered when Provider health checks fail or the Provider is disabled.",
		"provider_health",
		"failed",
		"critical",
		"provider",
		[]string{"monitor_check_failed"},
	)
	add(
		"provider_resource_health_failed",
		"alr_default_provider_resource_health",
		"Provider Resource Unavailable Alert",
		"Triggered when a resource check fails, the resource is disabled, or it enters cooldown.",
		"provider_resource_health",
		"failed",
		"warning",
		"provider_resource",
		[]string{"monitor_check_failed", "provider_resource_cooling_down"},
	)
	add(
		"request_quota_near_limit",
		"alr_default_quota_requests",
		"Request Quota Alert",
		"Triggered when request usage reaches the quota threshold or requests are rejected by quota.",
		"request_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"quota_exceeded"},
	)
	add(
		"token_quota_near_limit",
		"alr_default_quota_tokens",
		"Token Quota Alert",
		"Triggered when daily or monthly token usage reaches the quota threshold.",
		"token_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"daily_tokens_near_limit", "monthly_tokens_near_limit"},
	)
	add(
		"cost_quota_near_limit",
		"alr_default_quota_cost",
		"Cost Quota Alert",
		"Triggered when daily or monthly cost reaches the quota threshold.",
		"cost_quota_usage",
		"90%",
		"warning",
		"quota",
		[]string{"daily_cost_near_limit", "monthly_cost_near_limit"},
	)
	return items
}

func alertRuleKey(fields map[string]any) string {
	if ruleKey := strings.TrimSpace(stringField(fields, "rule_key")); ruleKey != "" {
		return ruleKey
	}
	metric := strings.TrimSpace(stringField(fields, "metric"))
	if metric == "" {
		return ""
	}
	scope := strings.TrimSpace(stringField(fields, "scope"))
	threshold := strings.TrimSpace(stringField(fields, "threshold"))
	return "metric:" + metric + ":" + scope + ":" + threshold
}

func (s *Server) handleAdminMonitorRun(w http.ResponseWriter, r *http.Request, user AdminUser, monitorID string) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	result, err := s.store.RunMonitor(monitorID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "run", "monitor", monitorID, "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminSQLiteBackups(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListSQLiteBackups()})
	case http.MethodPost:
		var req struct {
			ExpireDays int `json:"expire_days"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
		}
		backup, err := s.store.CreateSQLiteBackup(user.ID, req.ExpireDays)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "sqlite_backup", backup.ID, "", backup)
		writeJSON(w, http.StatusCreated, backup)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminSQLiteBackupItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "backup", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/sqlite/backups/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	backupID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			backup, err := s.store.GetSQLiteBackup(backupID)
			if err != nil {
				writeError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, backup)
		case http.MethodDelete:
			before, _ := s.store.GetSQLiteBackup(backupID)
			if err := s.store.DeleteSQLiteBackup(backupID); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "sqlite_backup", backupID, before, nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	switch parts[1] {
	case "download":
		if r.Method != http.MethodGet {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		backup, err := s.store.GetSQLiteBackup(backupID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if backup.Status != "ready" && backup.Status != "restored" {
			writeError(w, r, NewHTTPError(409, "backup_not_ready", "Backup is not ready to download"))
			return
		}
		if _, err := os.Stat(backup.FilePath); err != nil {
			writeError(w, r, NewHTTPError(404, "backup_file_missing", "Backup file is missing"))
			return
		}
		w.Header().Set("content-type", "application/vnd.sqlite3")
		w.Header().Set("content-disposition", `attachment; filename="`+backup.FileName+`"`)
		http.ServeFile(w, r, backup.FilePath)
		s.recordAdminAudit(r, user, "download", "sqlite_backup", backupID, "", map[string]any{"file_name": backup.FileName})
	case "restore":
		if r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		var req struct {
			Confirmation string `json:"confirmation"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if strings.TrimSpace(req.Confirmation) != "RESTORE "+backupID {
			writeError(w, r, NewHTTPError(400, "invalid_restore_confirmation", "Restore confirmation is invalid"))
			return
		}
		before, _ := s.store.GetSQLiteBackup(backupID)
		backup, err := s.store.RestoreSQLiteBackup(backupID, user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "restore", "sqlite_backup", backupID, before, backup)
		writeJSON(w, http.StatusOK, backup)
	default:
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
	}
}

func (s *Server) handleAdminInvoiceAction(w http.ResponseWriter, r *http.Request, user AdminUser, invoiceID string, action string) {
	if action != "confirm" && action != "reject" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		InvoiceNote  string `json:"invoice_note"`
		RejectReason string `json:"reject_reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
	}
	invoice, err := s.findResource("invoices", invoiceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	payload := invoiceDecisionPayload(invoice, action, req.InvoiceNote, req.RejectReason)
	if approval, required := s.approvalRequired(user, "invoice_"+action, "invoices", invoiceID, payload); required {
		s.recordAdminAudit(r, user, "request_approval", "invoices", approval.ID, "", approval)
		writeJSON(w, http.StatusAccepted, map[string]any{"approval_required": true, "approval": approval})
		return
	}
	updated, err := s.applyInvoiceDecision(invoice, action, user, req.InvoiceNote, req.RejectReason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, action, "invoices", invoiceID, invoice, updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAdminUsageSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, s.usageSummaryForUser(user))
}

func (s *Server) handleAdminUsageBreakdown(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, s.usageBreakdownForUser(user))
}

func (s *Server) handleAdminUsageTimeseries(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.usageTimeseriesForUser(user, 31)})
}

func (s *Server) handleAdminGenerateBilling(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Period string `json:"period"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
	}
	result, err := s.store.GenerateBillingPeriod(req.Period)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "generate", "billing", stringifyCSV(result["period"]), "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminRequestLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterRequestLogsForUser(user, s.store.ListRequestLogs())})
}

func (s *Server) handleAdminRequestDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "audit", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	requestID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/audit/requests/"), "/")
	if requestID == "" || strings.Contains(requestID, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	detail, err := s.store.GetRequestDetail(requestID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	log, ok := detail["log"].(RequestLog)
	if !ok {
		writeError(w, r, NewHTTPError(500, "internal_error", "Request detail is missing request log"))
		return
	}
	if !s.canAccessRequestLog(user, log) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to access this request"))
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleAdminAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "admin_audit", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAuditEvents()})
}

func (s *Server) handleAdminExport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	kind := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/export/"), "/")
	if kind == "" || strings.Contains(kind, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if !s.canExportKind(user, kind) {
		writeError(w, r, NewHTTPError(403, "export_forbidden", "Export is not available for this user"))
		return
	}
	periodFilter := normalizeExportPeriod(r.URL.Query().Get("period"))
	w.Header().Set("content-type", "text/csv; charset=utf-8")
	w.Header().Set("content-disposition", `attachment; filename="tokenhub-`+kind+`.csv"`)
	writer := csv.NewWriter(w)
	switch kind {
	case "requests":
		_ = writer.Write([]string{"created_at", "request_id", "project_id", "api_key_id", "model", "provider_id", "provider_resource_id", "status_code", "error_code", "latency_ms"})
		for _, item := range s.filterRequestLogsForUser(user, s.store.ListRequestLogs()) {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.RequestID,
				item.ProjectID,
				item.APIKeyID,
				item.ModelName,
				item.ProviderID,
				item.ProviderResourceID,
				strconv.Itoa(item.StatusCode),
				item.ErrorCode,
				strconv.FormatInt(item.LatencyMS, 10),
			})
		}
	case "usage":
		_ = writer.Write([]string{"dimension", "id", "request_count", "input_tokens", "cached_input_tokens", "output_tokens", "total_tokens", "estimated_cost_usd"})
		records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
		if periodFilter != "" {
			filtered := make([]UsageRecord, 0, len(records))
			start := periodStart(periodFilter)
			end := periodEnd(periodFilter)
			for _, record := range records {
				if !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
					filtered = append(filtered, record)
				}
			}
			records = filtered
		}
		breakdown := s.usageBreakdownFromRecords(records)
		for _, dimension := range []string{"projects", "models", "providers", "provider_resources", "cost_centers"} {
			rows, _ := breakdown[dimension].([]map[string]any)
			for _, row := range rows {
				_ = writer.Write([]string{
					dimension,
					stringifyCSV(row["id"]),
					stringifyCSV(row["request_count"]),
					stringifyCSV(row["input_tokens"]),
					stringifyCSV(row["cached_input_tokens"]),
					stringifyCSV(row["output_tokens"]),
					stringifyCSV(row["total_tokens"]),
					stringifyCSV(row["estimated_cost_usd"]),
				})
			}
		}
	case "cost-centers":
		s.writeResourceExport(writer, user, "cost-centers", "", []resourceExportColumn{
			{Header: "code", Field: "code"},
			{Header: "name", Source: "name"},
			{Header: "department", Field: "department"},
			{Header: "owner", Field: "owner"},
			{Header: "monthly_budget_usd", Field: "monthly_budget_usd"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "budgets":
		s.writeResourceExport(writer, user, "budgets", periodFilter, []resourceExportColumn{
			{Header: "name", Source: "name"},
			{Header: "scope", Field: "scope"},
			{Header: "scope_id", Field: "scope_id"},
			{Header: "period", Field: "period"},
			{Header: "period_ref", Field: "period_ref"},
			{Header: "amount_usd", Field: "amount_usd"},
			{Header: "warn_percent", Field: "warn_percent"},
			{Header: "used_usd", Field: "used_usd"},
			{Header: "remaining_usd", Field: "remaining_usd"},
			{Header: "usage_percent", Field: "usage_percent"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "chargebacks":
		s.writeResourceExport(writer, user, "chargebacks", periodFilter, []resourceExportColumn{
			{Header: "period", Field: "period"},
			{Header: "cost_center", Field: "cost_center"},
			{Header: "team_id", Field: "team_id"},
			{Header: "project_id", Field: "project_id"},
			{Header: "allocated_cost_usd", Field: "allocated_cost_usd"},
			{Header: "request_count", Field: "request_count"},
			{Header: "input_tokens", Field: "input_tokens"},
			{Header: "cached_input_tokens", Field: "cached_input_tokens"},
			{Header: "output_tokens", Field: "output_tokens"},
			{Header: "total_tokens", Field: "total_tokens"},
			{Header: "allocation_rule", Field: "allocation_rule"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "invoices":
		s.writeResourceExport(writer, user, "invoices", periodFilter, []resourceExportColumn{
			{Header: "period", Field: "period"},
			{Header: "cost_center", Field: "cost_center"},
			{Header: "amount_usd", Field: "amount_usd"},
			{Header: "invoice_note", Field: "invoice_note"},
			{Header: "confirmed_by", Field: "confirmed_by"},
			{Header: "confirmed_at", Field: "confirmed_at"},
			{Header: "reject_reason", Field: "reject_reason"},
			{Header: "status", Source: "status"},
			{Header: "updated_at", Source: "updated_at"},
		})
	case "approvals":
		_ = writer.Write([]string{"created_at", "id", "trigger", "resource_type", "resource_id", "requester", "status", "decided_by", "decided_at", "reason"})
		for _, item := range s.filterApprovalRequestsForUser(user, s.store.ListApprovalRequests()) {
			decidedAt := ""
			if item.DecidedAt != nil {
				decidedAt = item.DecidedAt.Format(time.RFC3339)
			}
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.ID,
				item.Trigger,
				item.ResourceType,
				item.ResourceID,
				item.Requester,
				item.Status,
				item.DecidedBy,
				decidedAt,
				item.Reason,
			})
		}
	case "audit-events":
		_ = writer.Write([]string{"created_at", "actor_user_id", "actor_name", "actor_role", "action", "resource_type", "resource_id", "status", "message", "ip"})
		for _, item := range s.store.ListAuditEvents() {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.ActorUserID,
				item.ActorName,
				item.ActorRole,
				item.Action,
				item.ResourceType,
				item.ResourceID,
				item.Status,
				item.Message,
				item.IP,
			})
		}
	case "alert-deliveries":
		_ = writer.Write([]string{"created_at", "alert_id", "channel_id", "channel", "target", "status", "status_code", "error"})
		for _, item := range s.store.ListAlertDeliveries() {
			_ = writer.Write([]string{
				item.CreatedAt.Format(time.RFC3339),
				item.AlertID,
				item.ChannelID,
				item.Channel,
				item.Target,
				item.Status,
				strconv.Itoa(item.StatusCode),
				item.Error,
			})
		}
	default:
		items := s.filterResourcesForUser(user, kind, s.store.ListResources(kind))
		_ = writer.Write([]string{"id", "kind", "name", "status", "description", "fields", "updated_at"})
		for _, item := range items {
			_ = writer.Write([]string{
				item.ID,
				item.Kind,
				item.Name,
				item.Status,
				item.Description,
				snapshotJSON(item.Fields),
				item.UpdatedAt.Format(time.RFC3339),
			})
		}
	}
	writer.Flush()
	s.recordAdminAudit(r, user, "export", kind, "", "", map[string]any{"format": "csv", "period": periodFilter})
}

func (s *Server) handleAdminAlerts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "alert", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAlerts()})
}

func (s *Server) handleAdminAlertItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "alert", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/alerts/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "deliver" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
	}
	delivery, err := s.deliverAlert(r.Context(), parts[0], req.ChannelID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "deliver", "alert", parts[0], "", delivery)
	writeJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleAdminAlertDeliveries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "alert", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAlertDeliveries()})
}

func (s *Server) handleAdminApprovals(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "approval", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterApprovalRequestsForUser(user, s.store.ListApprovalRequests())})
}

func (s *Server) handleAdminApprovalItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "approval", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/approvals/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "approve" && parts[1] != "reject") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
	}
	status := "approved"
	if parts[1] == "reject" {
		status = "rejected"
	}
	pending, err := s.store.GetApprovalRequest(parts[0])
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.requireApprovalRole(pending, user); err != nil {
		writeError(w, r, err)
		return
	}
	var result any
	if status == "approved" {
		result, err = s.applyApprovalRequest(pending, user)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "apply_approval", pending.ResourceType, pending.ResourceID, pending, result)
	}
	item, err := s.store.UpdateApprovalRequestStatus(parts[0], status, user.ID, req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, status, "approval", item.ID, "", item)
	writeJSON(w, http.StatusOK, map[string]any{"approval": item, "result": result})
}

func (s *Server) requireApprovalRole(request ApprovalRequest, user AdminUser) error {
	if strings.TrimSpace(request.FlowID) == "" {
		return nil
	}
	flow, err := s.findResource("approval-flows", request.FlowID)
	if err != nil {
		return err
	}
	required := strings.TrimSpace(stringField(flow.Fields, "approver_role"))
	if required == "" {
		return nil
	}
	if !adminRoleMatches(user.Role, required) {
		return NewHTTPError(http.StatusForbidden, "approval_role_forbidden", "Admin role is not allowed to decide this approval")
	}
	return nil
}

func (s *Server) approvalRequired(user AdminUser, trigger string, resourceType string, resourceID string, payload any) (ApprovalRequest, bool) {
	flow, ok := s.matchApprovalFlow(trigger, payload)
	if !ok {
		return ApprovalRequest{}, false
	}
	return s.createApprovalRequest(user, flow.ID, trigger, resourceType, resourceID, payload), true
}

func (s *Server) createApprovalRequest(user AdminUser, flowID string, trigger string, resourceType string, resourceID string, payload any) ApprovalRequest {
	request := ApprovalRequest{
		FlowID:       flowID,
		Trigger:      trigger,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		RequesterID:  user.ID,
		Requester:    user.Name,
		Status:       "pending",
		Payload:      snapshotJSON(payload),
	}
	return s.store.CreateApprovalRequest(request)
}

func (s *Server) matchApprovalFlow(trigger string, payload any) (AdminResource, bool) {
	flows := s.store.ListResources("approval-flows")
	payloadCost := approvalPayloadCost(payload)
	for _, flow := range flows {
		if flow.Status != StatusActive {
			continue
		}
		if strings.TrimSpace(stringField(flow.Fields, "trigger")) != trigger {
			continue
		}
		threshold := float64Field(flow.Fields, "threshold_usd")
		if threshold > 0 && payloadCost > 0 && payloadCost < threshold {
			continue
		}
		return flow, true
	}
	return AdminResource{}, false
}

func (s *Server) apiKeyUpdateApproval(user AdminUser, keyID string, patch APIKey) (ApprovalRequest, bool) {
	trigger := ""
	if patch.Limits != (QuotaLimits{}) {
		trigger = "quota_increase"
	}
	if trigger == "" && len(patch.Allowed) > 0 {
		trigger = "model_access"
	}
	if trigger == "" {
		return ApprovalRequest{}, false
	}
	payload := map[string]any{
		"api_key_id":       keyID,
		"requested_action": trigger,
		"allowed_models":   patch.Allowed,
		"limits":           patch.Limits,
		"status":           patch.Status,
	}
	return s.approvalRequired(user, trigger, "api_key", keyID, payload)
}

func (s *Server) adminResourceApproval(user AdminUser, kind string, resourceID string, resource AdminResource) (ApprovalRequest, bool) {
	trigger := ""
	switch kind {
	case "budgets":
		trigger = "budget_change"
	case "quota-policies":
		trigger = "quota_increase"
	default:
		return ApprovalRequest{}, false
	}
	payload := map[string]any{
		"kind":             kind,
		"resource_id":      resourceID,
		"name":             resource.Name,
		"description":      resource.Description,
		"status":           resource.Status,
		"fields":           resource.Fields,
		"requested_action": trigger,
	}
	return s.approvalRequired(user, trigger, kind, resourceID, payload)
}

func approvalPayloadCost(payload any) float64 {
	switch typed := payload.(type) {
	case map[string]any:
		if fields, ok := typed["fields"].(map[string]any); ok {
			if amount := float64Field(fields, "amount_usd"); amount > 0 {
				return amount
			}
		}
		if limits, ok := typed["limits"].(QuotaLimits); ok {
			return limits.MonthlyCostUSD
		}
		if limits, ok := typed["limits"].(map[string]any); ok {
			if amount := float64Field(limits, "monthly_cost_usd"); amount > 0 {
				return amount
			}
			return float64Field(limits, "daily_cost_usd")
		}
	}
	return 0
}

func (s *Server) deliverAlert(ctx context.Context, alertID string, channelID string) (AlertDelivery, error) {
	alert, err := s.store.GetAlert(alertID)
	if err != nil {
		return AlertDelivery{}, err
	}
	channel, err := s.resolveNotificationChannel(channelID)
	if err != nil {
		return AlertDelivery{}, err
	}
	payload := map[string]any{
		"source":     "tokenhub",
		"alert":      alert,
		"channel":    channel.Name,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
		"severity":   alert.Severity,
		"scope":      alert.ScopeType,
		"scope_id":   alert.ScopeID,
		"message":    alert.Message,
		"event_code": alert.Code,
	}
	delivery := AlertDelivery{
		AlertID:   alert.ID,
		ChannelID: channel.ID,
		Channel:   normalizeNotificationChannelType(stringField(channel.Fields, "type")),
		Target:    notificationChannelTarget(channel),
		Status:    "success",
		Payload:   snapshotJSON(payload),
	}
	if delivery.Channel == "" {
		delivery.Channel = "webhook"
	}
	if !supportedNotificationChannel(delivery.Channel) {
		delivery.Status = "failed"
		delivery.Error = "unsupported notification channel"
		return s.store.RecordAlertDelivery(delivery), nil
	}
	if delivery.Channel == "email" {
		if err := sendEmailAlert(ctx, channel, alert); err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
		}
		return s.store.RecordAlertDelivery(delivery), nil
	}
	target, err := notificationChannelRequestTarget(channel)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.store.RecordAlertDelivery(delivery), nil
	}
	bodyPayload, headers, err := notificationChannelPayloadForChannel(channel, payload, alert)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.store.RecordAlertDelivery(delivery), nil
	}
	body, _ := json.Marshal(bodyPayload)
	if delivery.Channel == "dingtalk" {
		target, err = signedDingTalkWebhookURL(target, firstStringField(channel.Fields, "secret", "sign_secret", "dingtalk_secret"))
		if err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
			return s.store.RecordAlertDelivery(delivery), nil
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.store.RecordAlertDelivery(delivery), nil
	}
	req.Header.Set("content-type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.store.RecordAlertDelivery(delivery), nil
	}
	defer resp.Body.Close()
	delivery.StatusCode = resp.StatusCode
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		delivery.Status = "failed"
		delivery.Error = resp.Status
	} else if err := notificationChannelResponseError(delivery.Channel, resp.Header.Get("content-type"), respBody); err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
	}
	return s.store.RecordAlertDelivery(delivery), nil
}

func signedDingTalkWebhookURL(rawURL string, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", sign)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func notificationChannelResponseError(channelType string, contentType string, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	switch normalizeNotificationChannelType(channelType) {
	case "dingtalk":
		if code := int64Field(payload, "errcode"); code != 0 {
			return fmt.Errorf("dingtalk response error: errcode=%d errmsg=%s", code, stringField(payload, "errmsg"))
		}
	case "feishu":
		if code := int64Field(payload, "code"); code != 0 {
			return fmt.Errorf("feishu response error: code=%d msg=%s", code, firstStringField(payload, "msg", "message"))
		}
	}
	return nil
}

func normalizeNotificationChannelType(channelType string) string {
	normalized := strings.ToLower(strings.TrimSpace(channelType))
	switch normalized {
	case "", "webhook":
		return "webhook"
	case "feishu", "lark":
		return "feishu"
	case "dingtalk", "dingding", "ding_talk":
		return "dingtalk"
	case "wecom", "wechat_work", "weixin_work", "enterprise_wechat":
		return "wecom"
	case "slack":
		return "slack"
	case "discord":
		return "discord"
	case "telegram", "tg":
		return "telegram"
	case "whatsapp", "whatsapp_cloud", "whatsapp_business", "wa":
		return "whatsapp"
	case "email", "mail", "smtp":
		return "email"
	default:
		return normalized
	}
}

func supportedNotificationChannel(channelType string) bool {
	switch normalizeNotificationChannelType(channelType) {
	case "webhook", "feishu", "dingtalk", "wecom", "slack", "discord", "telegram", "whatsapp", "email":
		return true
	default:
		return false
	}
}

func notificationChannelPayload(channelType string, payload map[string]any, alert AlertEvent) any {
	text := notificationChannelText(alert)
	switch normalizeNotificationChannelType(channelType) {
	case "feishu":
		return map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": text},
		}
	case "dingtalk", "wecom":
		return map[string]any{
			"msgtype": "text",
			"text":    map[string]any{"content": text},
		}
	case "slack":
		return map[string]any{
			"text": text,
		}
	case "discord":
		return map[string]any{
			"content":          text,
			"allowed_mentions": map[string]any{"parse": []string{}},
		}
	default:
		return payload
	}
}

func notificationChannelPayloadForChannel(channel AdminResource, payload map[string]any, alert AlertEvent) (any, map[string]string, error) {
	fields := channel.Fields
	text := notificationChannelText(alert)
	switch normalizeNotificationChannelType(stringField(fields, "type")) {
	case "telegram":
		chatID := strings.TrimSpace(firstStringField(fields, "telegram_chat_id", "chat_id", "recipient", "to"))
		if chatID == "" {
			return nil, nil, fmt.Errorf("telegram_chat_id is required")
		}
		body := map[string]any{
			"chat_id":                  chatID,
			"text":                     text,
			"disable_web_page_preview": true,
		}
		if threadID := strings.TrimSpace(firstStringField(fields, "telegram_thread_id", "message_thread_id", "thread_id")); threadID != "" {
			body["message_thread_id"] = threadID
		}
		return body, nil, nil
	case "whatsapp":
		recipient := strings.TrimSpace(firstStringField(fields, "whatsapp_to", "recipient", "to"))
		if recipient == "" {
			return nil, nil, fmt.Errorf("whatsapp_to is required")
		}
		accessToken := strings.TrimSpace(firstStringField(fields, "access_token", "whatsapp_access_token", "token", "secret"))
		if accessToken == "" {
			return nil, nil, fmt.Errorf("access_token is required")
		}
		return map[string]any{
				"messaging_product": "whatsapp",
				"to":                recipient,
				"type":              "text",
				"text": map[string]any{
					"preview_url": false,
					"body":        text,
				},
			}, map[string]string{
				"authorization": "Bearer " + accessToken,
			}, nil
	default:
		return notificationChannelPayload(stringField(fields, "type"), payload, alert), nil, nil
	}
}

func notificationChannelText(alert AlertEvent) string {
	return fmt.Sprintf("[TokenHub] %s\n%s\n对象：%s/%s", alert.Code, alert.Message, alert.ScopeType, alert.ScopeID)
}

func notificationChannelTarget(channel AdminResource) string {
	channelType := normalizeNotificationChannelType(stringField(channel.Fields, "type"))
	if channelType == "email" {
		return firstStringField(channel.Fields, "email_to", "recipients", "to")
	}
	if channelType == "telegram" {
		if chatID := strings.TrimSpace(firstStringField(channel.Fields, "telegram_chat_id", "chat_id", "recipient", "to")); chatID != "" {
			return "telegram:" + chatID
		}
		return "telegram"
	}
	if channelType == "whatsapp" {
		if recipient := strings.TrimSpace(firstStringField(channel.Fields, "whatsapp_to", "recipient", "to")); recipient != "" {
			return "whatsapp:" + recipient
		}
		return "whatsapp"
	}
	return firstStringField(channel.Fields, "webhook_url", "url")
}

func notificationChannelRequestTarget(channel AdminResource) (string, error) {
	fields := channel.Fields
	channelType := normalizeNotificationChannelType(stringField(fields, "type"))
	if target := strings.TrimSpace(firstStringField(fields, "webhook_url", "url")); target != "" {
		return target, nil
	}
	switch channelType {
	case "telegram":
		botToken := strings.TrimSpace(firstStringField(fields, "telegram_bot_token", "bot_token", "token", "secret"))
		if botToken == "" {
			return "", fmt.Errorf("telegram_bot_token is required")
		}
		return "https://api.telegram.org/bot" + botToken + "/sendMessage", nil
	case "whatsapp":
		phoneNumberID := strings.TrimSpace(firstStringField(fields, "whatsapp_phone_number_id", "phone_number_id"))
		if phoneNumberID == "" {
			return "", fmt.Errorf("whatsapp_phone_number_id is required")
		}
		apiVersion := strings.Trim(strings.TrimSpace(firstStringField(fields, "whatsapp_api_version", "api_version")), "/")
		if apiVersion == "" {
			apiVersion = "v20.0"
		}
		return "https://graph.facebook.com/" + apiVersion + "/" + phoneNumberID + "/messages", nil
	default:
		return "", fmt.Errorf("webhook_url is required")
	}
}

func sendEmailAlert(ctx context.Context, channel AdminResource, alert AlertEvent) error {
	fields := channel.Fields
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	recipients := splitNotificationRecipients(firstStringField(fields, "email_to", "recipients", "to"))
	if len(recipients) == 0 {
		return fmt.Errorf("email_to is required")
	}
	return sendEmail(ctx, fields, recipients, emailAlertMessage(from, recipients, alert))
}

func sendEmail(ctx context.Context, fields map[string]any, recipients []string, message []byte) error {
	host := strings.TrimSpace(stringField(fields, "smtp_host"))
	if host == "" {
		return fmt.Errorf("smtp_host is required")
	}
	port := int64Field(fields, "smtp_port")
	if port <= 0 {
		port = 587
	}
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	if from == "" {
		return fmt.Errorf("smtp_from is required")
	}
	addr := net.JoinHostPort(host, strconv.FormatInt(port, 10))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	username := strings.TrimSpace(firstStringField(fields, "smtp_username", "username"))
	password := firstStringField(fields, "smtp_password", "password")
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func splitNotificationRecipients(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	recipients := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			recipients = append(recipients, field)
		}
	}
	return recipients
}

func (s *Server) resolvePasswordResetMailChannel() (AdminResource, error) {
	channels := s.store.ListResources("notification-channels")
	for _, channel := range channels {
		if channel.Status != StatusActive || normalizeNotificationChannelType(stringField(channel.Fields, "type")) != "email" {
			continue
		}
		if err := validatePasswordResetMailChannel(channel); err == nil {
			return channel, nil
		}
	}
	return AdminResource{}, NewHTTPError(400, "email_notification_required", "Active email notification channel with SMTP host, port and sender is required")
}

func validatePasswordResetMailChannel(channel AdminResource) error {
	fields := channel.Fields
	if normalizeNotificationChannelType(stringField(fields, "type")) != "email" {
		return fmt.Errorf("email notification channel is required")
	}
	if strings.TrimSpace(stringField(fields, "smtp_host")) == "" {
		return fmt.Errorf("smtp_host is required")
	}
	if int64Field(fields, "smtp_port") <= 0 {
		return fmt.Errorf("smtp_port is required")
	}
	if strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from")) == "" {
		return fmt.Errorf("smtp_from is required")
	}
	return nil
}

func (s *Server) sendAdminPasswordResetEmail(r *http.Request, channel AdminResource, user AdminUser, createdBy string) error {
	if strings.TrimSpace(user.Email) == "" {
		return NewHTTPError(400, "missing_user_email", "User email is required")
	}
	plainToken, token, err := s.store.CreateAdminPasswordResetToken(user.ID, createdBy, 24*time.Hour)
	if err != nil {
		return err
	}
	resetLink := adminPasswordResetLink(r, plainToken)
	return sendEmail(r.Context(), channel.Fields, []string{user.Email}, passwordResetEmailMessage(channel.Fields, []string{user.Email}, user, resetLink, token.ExpiresAt))
}

func adminPasswordResetLink(r *http.Request, token string) string {
	baseURL := ""
	if r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
			scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
		}
		if host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); host != "" {
			baseURL = scheme + "://" + strings.TrimSpace(strings.Split(host, ",")[0])
		} else if r.Host != "" {
			baseURL = scheme + "://" + r.Host
		}
	}
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	return strings.TrimRight(baseURL, "/") + "/?reset_token=" + token
}

func passwordResetEmailMessage(fields map[string]any, recipients []string, user AdminUser, resetLink string, expiresAt time.Time) []byte {
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	subject := sanitizeEmailHeader("[TokenHub] 重置控制台登录密码")
	body := fmt.Sprintf("您好 %s，\n\n管理员已为您的 TokenHub 控制台账号发起密码重置。\n账号：%s\n邮箱：%s\n\n请在 24 小时内打开以下链接设置新密码：\n%s\n\n过期时间：%s\n如非本人操作，请联系管理员。\n",
		defaultString(user.Name, user.Username),
		user.Username,
		user.Email,
		resetLink,
		expiresAt.Format(time.RFC3339),
	)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		sanitizeEmailHeader(from),
		sanitizeEmailHeader(strings.Join(recipients, ", ")),
		subject,
		body,
	)
	return []byte(message)
}

func emailAlertMessage(from string, recipients []string, alert AlertEvent) []byte {
	subject := sanitizeEmailHeader("[TokenHub] " + alert.Code)
	body := fmt.Sprintf("告警事件：%s\n级别：%s\n对象：%s/%s\n说明：%s\n时间：%s\n",
		alert.Code,
		alert.Severity,
		alert.ScopeType,
		alert.ScopeID,
		alert.Message,
		alert.CreatedAt.Format(time.RFC3339),
	)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		sanitizeEmailHeader(from),
		sanitizeEmailHeader(strings.Join(recipients, ", ")),
		subject,
		body,
	)
	return []byte(message)
}

func sanitizeEmailHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func (s *Server) resolveNotificationChannel(channelID string) (AdminResource, error) {
	channels := s.store.ListResources("notification-channels")
	var fallback *AdminResource
	for i := range channels {
		channel := channels[i]
		if channel.Status != StatusActive {
			continue
		}
		if channelID != "" && channel.ID == channelID {
			return channel, nil
		}
		if fallback == nil {
			copy := channel
			fallback = &copy
		}
	}
	if channelID != "" {
		return AdminResource{}, NewHTTPError(404, "notification_channel_not_found", "Notification channel not found")
	}
	if fallback == nil {
		return AdminResource{}, NewHTTPError(404, "notification_channel_not_found", "No active notification channel")
	}
	return *fallback, nil
}

func (s *Server) applyApprovalRequest(request ApprovalRequest, actor AdminUser) (any, error) {
	if request.Status != "pending" {
		return nil, NewHTTPError(http.StatusConflict, "approval_already_decided", "Approval request has already been decided")
	}
	payload := map[string]any{}
	if request.Payload != "" {
		if err := json.Unmarshal([]byte(request.Payload), &payload); err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_approval_payload", "Approval payload is invalid")
		}
	}
	switch {
	case request.Trigger == "api_key_create":
		projectID := stringFromPayload(payload, "project_id")
		key, secret, err := s.store.CreateAPIKey(projectID, APIKey{
			Name:        stringFromPayload(payload, "name"),
			Group:       stringFromPayload(payload, "group"),
			Allowed:     stringSliceFromPayload(payload["allowed_models"]),
			IPAllowlist: stringSliceFromPayload(payload["ip_allowlist"]),
			Limits:      quotaLimitsFromPayload(payload["limits"]),
			Status:      StatusActive,
		}, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":                      key.ID,
			"api_key":                 secret,
			"name":                    key.Name,
			"project_id":              key.ProjectID,
			"plain_text_visible_once": true,
		}, nil
	case request.ResourceType == "api_key":
		key, err := s.store.UpdateAPIKey(request.ResourceID, APIKey{
			Allowed:     stringSliceFromPayload(payload["allowed_models"]),
			IPAllowlist: stringSliceFromPayload(payload["ip_allowlist"]),
			Limits:      quotaLimitsFromPayload(payload["limits"]),
			Status:      stringFromPayload(payload, "status"),
		})
		return key, err
	case request.ResourceType == "budgets" || request.ResourceType == "quota-policies":
		resource := AdminResource{
			Name:        stringFromPayload(payload, "name"),
			Description: stringFromPayload(payload, "description"),
			Status:      stringFromPayload(payload, "status"),
			Fields:      fieldsFromPayload(payload["fields"]),
		}
		var saved AdminResource
		var err error
		if request.ResourceID == "" {
			saved = s.store.CreateResource(request.ResourceType, resource)
		} else {
			saved, err = s.store.UpdateResource(request.ResourceType, request.ResourceID, resource)
			if err != nil {
				return nil, err
			}
		}
		if request.ResourceType == "quota-policies" {
			if err := s.linkProjectQuotaPolicy(saved, payload); err != nil {
				return nil, err
			}
		}
		return saved, nil
	case request.ResourceType == "invoices" && (request.Trigger == "invoice_confirm" || request.Trigger == "invoice_reject"):
		invoice, err := s.findResource("invoices", request.ResourceID)
		if err != nil {
			return nil, err
		}
		action := strings.TrimPrefix(request.Trigger, "invoice_")
		return s.applyInvoiceDecision(invoice, action, actor, stringFromPayload(payload, "invoice_note"), stringFromPayload(payload, "reject_reason"))
	default:
		return map[string]any{"applied": false, "reason": "no runtime apply handler"}, nil
	}
}

type resourceExportColumn struct {
	Header string
	Field  string
	Source string
}

func (s *Server) writeResourceExport(writer *csv.Writer, user AdminUser, kind string, periodFilter string, columns []resourceExportColumn) {
	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.Header)
	}
	_ = writer.Write(headers)
	for _, item := range s.filterResourcesForUser(user, kind, s.store.ListResources(kind)) {
		if periodFilter != "" && !resourceMatchesPeriod(item, periodFilter) {
			continue
		}
		row := make([]string, 0, len(columns))
		for _, column := range columns {
			row = append(row, resourceExportValue(item, column))
		}
		_ = writer.Write(row)
	}
}

func normalizeExportPeriod(period string) string {
	period = strings.TrimSpace(period)
	if period == "" {
		return ""
	}
	return normalizeBillingPeriod(period, time.Now().UTC())
}

func resourceMatchesPeriod(item AdminResource, period string) bool {
	if period == "" {
		return true
	}
	for _, key := range []string{"period", "period_ref", "last_calculated_period"} {
		if normalizeExportPeriod(stringField(item.Fields, key)) == period {
			return true
		}
	}
	return false
}

func resourceExportValue(item AdminResource, column resourceExportColumn) string {
	switch column.Source {
	case "id":
		return item.ID
	case "kind":
		return item.Kind
	case "name":
		return item.Name
	case "description":
		return item.Description
	case "status":
		return item.Status
	case "created_at":
		return item.CreatedAt.Format(time.RFC3339)
	case "updated_at":
		return item.UpdatedAt.Format(time.RFC3339)
	}
	return stringifyCSV(item.Fields[column.Field])
}

func (s *Server) findResource(kind string, id string) (AdminResource, error) {
	for _, item := range s.store.ListResources(kind) {
		if item.ID == id {
			return item, nil
		}
	}
	return AdminResource{}, NewHTTPError(404, "resource_not_found", "Resource not found")
}

func (s *Server) findProject(id string) (Project, error) {
	id = strings.TrimSpace(id)
	for _, project := range s.store.ListProjects() {
		if project.ID == id {
			return project, nil
		}
	}
	return Project{}, NewHTTPError(404, "project_not_found", "Project not found")
}

func invoiceDecisionPayload(invoice AdminResource, action string, invoiceNote string, rejectReason string) map[string]any {
	fields := map[string]any{}
	for key, value := range invoice.Fields {
		fields[key] = value
	}
	return map[string]any{
		"kind":             "invoices",
		"resource_id":      invoice.ID,
		"name":             invoice.Name,
		"status":           invoice.Status,
		"fields":           fields,
		"amount_usd":       float64Field(invoice.Fields, "amount_usd"),
		"invoice_note":     invoiceNote,
		"reject_reason":    rejectReason,
		"requested_action": "invoice_" + action,
	}
}

func (s *Server) applyInvoiceDecision(invoice AdminResource, action string, actor AdminUser, invoiceNote string, rejectReason string) (AdminResource, error) {
	if invoice.Kind != "invoices" {
		return AdminResource{}, NewHTTPError(400, "invalid_invoice", "Resource is not an invoice")
	}
	status := strings.ToLower(strings.TrimSpace(invoice.Status))
	if status == "confirmed" || status == "rejected" {
		return AdminResource{}, NewHTTPError(http.StatusConflict, "invoice_already_decided", "Invoice has already been decided")
	}
	fields := map[string]any{}
	for key, value := range invoice.Fields {
		fields[key] = value
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(invoiceNote) != "" {
		fields["invoice_note"] = strings.TrimSpace(invoiceNote)
	}
	switch action {
	case "confirm":
		fields["confirmed_by"] = actor.Name
		fields["confirmed_by_id"] = actor.ID
		fields["confirmed_at"] = now
		fields["reject_reason"] = ""
		invoice.Status = "confirmed"
	case "reject":
		fields["rejected_by"] = actor.Name
		fields["rejected_by_id"] = actor.ID
		fields["rejected_at"] = now
		fields["reject_reason"] = strings.TrimSpace(rejectReason)
		invoice.Status = "rejected"
	default:
		return AdminResource{}, NewHTTPError(400, "invalid_invoice_action", "Invalid invoice action")
	}
	invoice.Fields = fields
	return s.store.UpdateResource("invoices", invoice.ID, invoice)
}

func stringFromPayload(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return strings.TrimSpace(stringifyCSV(value))
}

func stringSliceFromPayload(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringifyCSV(item))
			if text != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		items := strings.Split(typed, ",")
		result := make([]string, 0, len(items))
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	default:
		return nil
	}
}

func fieldsFromPayload(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	return map[string]any{}
}

func quotaLimitsFromPayload(value any) QuotaLimits {
	switch typed := value.(type) {
	case QuotaLimits:
		return typed
	case map[string]any:
		return QuotaLimits{
			DailyRequests:   int64Field(typed, "daily_requests"),
			MonthlyRequests: int64Field(typed, "monthly_requests"),
			DailyTokens:     int64Field(typed, "daily_tokens"),
			MonthlyTokens:   int64Field(typed, "monthly_tokens"),
			DailyCostUSD:    float64Field(typed, "daily_cost_usd"),
			MonthlyCostUSD:  float64Field(typed, "monthly_cost_usd"),
			MaxConcurrency:  int64Field(typed, "max_concurrency"),
		}
	default:
		return QuotaLimits{}
	}
}

func (s *Server) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	_, ok := s.requireAdmin(w, r, "overview", r.Method)
	return ok
}

func (s *Server) authorizeAdminUser(w http.ResponseWriter, r *http.Request) (AdminUser, bool) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, r, NewHTTPError(401, "invalid_admin_token", "Invalid admin token"))
		return AdminUser{}, false
	}
	if token == strings.TrimSpace(s.config.AdminToken) {
		users := s.store.ListAdminUsers()
		if len(users) > 0 {
			return users[0], true
		}
		return AdminUser{ID: "dev_admin", Username: "dev_admin", Name: "开发管理员", Email: "admin@tokenhub.local", Role: "admin", Status: StatusActive}, true
	}
	user, ok := s.store.ValidateAdminSession(token)
	if !ok {
		writeError(w, r, NewHTTPError(401, "invalid_admin_token", "Invalid admin token"))
		return AdminUser{}, false
	}
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, resource string, method string) (AdminUser, bool) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return AdminUser{}, false
	}
	if !canAdmin(user.Role, resource, method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return AdminUser{}, false
	}
	return user, true
}

func canAdmin(role string, resource string, method string) bool {
	role = normalizeAdminRole(role)
	if role == "" {
		role = "user"
	}
	resource = strings.ToLower(strings.TrimSpace(resource))
	write := method != http.MethodGet
	switch role {
	case "admin", "system_admin":
		return true
	case "security_admin":
		if resource == "backup" {
			return false
		}
		if write {
			return resource == "alert" || resource == "security" || resource == "audit" || resource == "admin_audit" || resource == "approval"
		}
		return resource == "overview" || resource == "usage" || resource == "audit" || resource == "admin_audit" || resource == "alert" || resource == "security" || resource == "approval"
	case "team_leader":
		if resource == "backup" {
			return false
		}
		if write {
			return resource == "identity" || resource == "project" || resource == "api_key" || resource == "approval" || resource == "playground" || resource == "quota"
		}
		return resource == "overview" || resource == "project" || resource == "api_key" || resource == "usage" || resource == "audit" || resource == "identity" || resource == "approval" || resource == "quota"
	case "user":
		if write {
			return resource == "api_key" || resource == "playground"
		}
		return resource == "overview" || resource == "project" || resource == "api_key" || resource == "usage" || resource == "audit" || resource == "model" || resource == "playground"
	default:
		return !write && resource == "overview"
	}
}

func adminRoleMatches(actual string, required string) bool {
	actual = normalizeAdminRole(actual)
	required = normalizeAdminRole(required)
	if actual == "admin" || actual == "system_admin" {
		return true
	}
	return actual == required
}

func normalizeAdminRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "security":
		return "security_admin"
	case "project_admin", "teamlead":
		return "team_leader"
	case "viewer", "readonly", "read_only", "member":
		return "user"
	default:
		return role
	}
}

func isPlatformAdminRole(role string) bool {
	role = normalizeAdminRole(role)
	return role == "admin" || role == "system_admin"
}

func (s *Server) canViewGlobalOperations(user AdminUser) bool {
	role := normalizeAdminRole(user.Role)
	return isPlatformAdminRole(role) || role == "security_admin"
}

func (s *Server) filterProjectsForUser(user AdminUser, projects []Project) []Project {
	if s.canViewGlobalOperations(user) {
		return projects
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		if s.canAccessProject(user, project) {
			out = append(out, project)
		}
	}
	return out
}

func (s *Server) canAccessProject(user AdminUser, project Project) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	if project.OwnerUserID != "" && project.OwnerUserID == user.ID {
		return true
	}
	if s.projectMemberGrantsProjectAccess(user, project.ID) {
		return true
	}
	return normalizeAdminRole(user.Role) == "team_leader" && user.TeamID != "" && project.TeamID == user.TeamID
}

func (s *Server) projectQuotaPolicy(project Project) (AdminResource, bool) {
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		if quota, err := s.findResource("quota-policies", project.DefaultQuotaRef); err == nil {
			return quota, true
		}
	}
	for _, quota := range s.store.ListResources("quota-policies") {
		scope := strings.ToLower(strings.TrimSpace(stringField(quota.Fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(quota.Fields, "scope_type")))
		}
		scopeID := strings.TrimSpace(stringField(quota.Fields, "scope_id"))
		if scope == "project" && scopeID == project.ID {
			return quota, true
		}
	}
	return AdminResource{}, false
}

func (s *Server) linkProjectQuotaPolicy(quota AdminResource, payload map[string]any) error {
	projectID := strings.TrimSpace(stringFromPayload(payload, "project_id"))
	if projectID == "" {
		fields := fieldsFromPayload(payload["fields"])
		scope := strings.ToLower(strings.TrimSpace(stringField(fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(fields, "scope_type")))
		}
		if scope == "project" {
			projectID = strings.TrimSpace(stringField(fields, "scope_id"))
		}
	}
	if projectID == "" {
		return nil
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		return NewHTTPError(404, "project_not_found", "Project not found")
	}
	if project.DefaultQuotaRef == quota.ID {
		return nil
	}
	project.DefaultQuotaRef = quota.ID
	_, err := s.store.UpdateProject(project.ID, Project{
		Name:            project.Name,
		TeamID:          project.TeamID,
		OwnerUserID:     project.OwnerUserID,
		CostCenter:      project.CostCenter,
		Status:          project.Status,
		DefaultQuotaRef: quota.ID,
	})
	return err
}

func (s *Server) usageSummaryForUser(user AdminUser) map[string]any {
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	logs := s.filterRequestLogsForUser(user, s.store.ListRequestLogs())
	var input, cachedInput, output, total int64
	var cost float64
	errorsCount := 0
	for _, record := range records {
		input += record.InputTokens
		cachedInput += record.CachedInputTokens
		output += record.OutputTokens
		total += record.TotalTokens
		cost += record.CostUSD
	}
	for _, log := range logs {
		if isPlaygroundRequestLog(log) {
			continue
		}
		if log.StatusCode >= 400 {
			errorsCount++
		}
	}
	return map[string]any{
		"request_count":       billableRequestLogCount(logs),
		"usage_record_count":  len(records),
		"input_tokens":        input,
		"cached_input_tokens": cachedInput,
		"output_tokens":       output,
		"total_tokens":        total,
		"estimated_cost_usd":  cost,
		"errors":              errorsCount,
	}
}

func (s *Server) usageBreakdownForUser(user AdminUser) map[string]any {
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	breakdown := s.usageBreakdownFromRecords(records)
	breakdown["members"] = s.aggregateUsageByMember(user, records)
	return breakdown
}

func (s *Server) usageBreakdownFromRecords(records []UsageRecord) map[string]any {
	return map[string]any{
		"projects":  aggregateUsage(records, func(record UsageRecord) string { return record.ProjectID }),
		"models":    aggregateUsage(records, func(record UsageRecord) string { return record.ModelName }),
		"providers": aggregateUsage(records, func(record UsageRecord) string { return record.ProviderID }),
		"provider_resources": aggregateUsage(records, func(record UsageRecord) string {
			return record.ProviderResourceID
		}),
		"cost_centers": aggregateUsage(records, func(record UsageRecord) string {
			project, ok := s.store.GetProject(record.ProjectID)
			if !ok {
				return "unknown"
			}
			return s.costCenterForProject(project)
		}),
	}
}

func (s *Server) aggregateUsageByMember(user AdminUser, records []UsageRecord) []map[string]any {
	keysByID := map[string]APIKey{}
	for _, key := range s.store.ListAPIKeys() {
		keysByID[key.ID] = key
	}
	projectsByID := map[string]Project{}
	for _, project := range s.store.ListProjects() {
		projectsByID[project.ID] = project
	}
	usersByID := map[string]AdminUser{}
	for _, item := range s.store.ListAdminUsers() {
		usersByID[item.ID] = item
	}
	return aggregateUsage(records, func(record UsageRecord) string {
		if key, ok := keysByID[record.APIKeyID]; ok {
			if owner := strings.TrimSpace(key.Metadata["created_by"]); owner != "" {
				if canAttributeUsageToMember(user, usersByID, owner) {
					return owner
				}
			}
		}
		if project, ok := projectsByID[record.ProjectID]; ok {
			if canAttributeUsageToMember(user, usersByID, project.OwnerUserID) {
				return project.OwnerUserID
			}
		}
		return "unknown"
	})
}

func canAttributeUsageToMember(user AdminUser, usersByID map[string]AdminUser, memberID string) bool {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return false
	}
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) || role == "security_admin" {
		return true
	}
	if role == "team_leader" {
		member, ok := usersByID[memberID]
		if !ok || member.TeamID != user.TeamID {
			return false
		}
		memberRole := normalizeAdminRole(member.Role)
		return memberRole == "user" || memberRole == "team_leader"
	}
	return memberID == user.ID
}

func (s *Server) usageTimeseriesForUser(user AdminUser, days int) []map[string]any {
	if days <= 0 {
		days = 31
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().UTC()
	series := make([]map[string]any, 0, days)
	indexByDay := map[string]int{}
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		indexByDay[day] = len(series)
		series = append(series, map[string]any{
			"date":                day,
			"request_count":       int64(0),
			"input_tokens":        int64(0),
			"cached_input_tokens": int64(0),
			"output_tokens":       int64(0),
			"total_tokens":        int64(0),
			"estimated_cost_usd":  float64(0),
		})
	}
	for _, record := range s.filterUsageRecordsForUser(user, s.store.ListUsageRecords()) {
		if record.CreatedAt.Before(now.AddDate(0, 0, -days+1)) {
			continue
		}
		day := record.CreatedAt.UTC().Format("2006-01-02")
		idx, ok := indexByDay[day]
		if !ok {
			continue
		}
		series[idx]["request_count"] = series[idx]["request_count"].(int64) + 1
		series[idx]["input_tokens"] = series[idx]["input_tokens"].(int64) + record.InputTokens
		series[idx]["cached_input_tokens"] = series[idx]["cached_input_tokens"].(int64) + record.CachedInputTokens
		series[idx]["output_tokens"] = series[idx]["output_tokens"].(int64) + record.OutputTokens
		series[idx]["total_tokens"] = series[idx]["total_tokens"].(int64) + record.TotalTokens
		series[idx]["estimated_cost_usd"] = series[idx]["estimated_cost_usd"].(float64) + record.CostUSD
	}
	return series
}

func (s *Server) filterUsageRecordsForUser(user AdminUser, records []UsageRecord) []UsageRecord {
	if s.canViewGlobalOperations(user) {
		return records
	}
	visibleProjects := s.visibleProjectIDSet(user)
	visibleKeys := s.visibleAPIKeyIDSet(user)
	out := make([]UsageRecord, 0, len(records))
	for _, record := range records {
		if normalizeAdminRole(user.Role) == "team_leader" && visibleProjects[record.ProjectID] {
			out = append(out, record)
			continue
		}
		if record.APIKeyID != "" && visibleKeys[record.APIKeyID] {
			out = append(out, record)
		}
	}
	return out
}

func (s *Server) filterRequestLogsForUser(user AdminUser, logs []RequestLog) []RequestLog {
	if s.canViewGlobalOperations(user) {
		return logs
	}
	visibleProjects := s.visibleProjectIDSet(user)
	visibleKeys := s.visibleAPIKeyIDSet(user)
	out := make([]RequestLog, 0, len(logs))
	for _, log := range logs {
		if canAccessRequestLogFromSets(user, log, visibleProjects, visibleKeys) {
			out = append(out, log)
		}
	}
	return out
}

func (s *Server) canAccessRequestLog(user AdminUser, log RequestLog) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	return canAccessRequestLogFromSets(user, log, s.visibleProjectIDSet(user), s.visibleAPIKeyIDSet(user))
}

func canAccessRequestLogFromSets(user AdminUser, log RequestLog, visibleProjects map[string]bool, visibleKeys map[string]bool) bool {
	if normalizeAdminRole(user.Role) == "team_leader" && visibleProjects[log.ProjectID] {
		return true
	}
	return log.APIKeyID != "" && visibleKeys[log.APIKeyID]
}

func (s *Server) visibleProjectIDSet(user AdminUser) map[string]bool {
	out := map[string]bool{}
	for _, project := range s.filterProjectsForUser(user, s.store.ListProjects()) {
		out[project.ID] = true
	}
	return out
}

func (s *Server) visibleAPIKeyIDSet(user AdminUser) map[string]bool {
	out := map[string]bool{}
	for _, key := range s.filterAPIKeysForUser(user, s.store.ListAPIKeys()) {
		out[key.ID] = true
	}
	return out
}

func (s *Server) costCenterForProject(project Project) string {
	if costCenter := strings.TrimSpace(project.CostCenter); costCenter != "" {
		return costCenter
	}
	if strings.TrimSpace(project.TeamID) != "" {
		for _, team := range s.store.ListResources("teams") {
			if team.ID == project.TeamID {
				if costCenter := strings.TrimSpace(stringField(team.Fields, "cost_center")); costCenter != "" {
					return costCenter
				}
			}
		}
	}
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		for _, quota := range s.store.ListResources("quota-policies") {
			if quota.ID == project.DefaultQuotaRef {
				if costCenter := strings.TrimSpace(stringField(quota.Fields, "cost_center")); costCenter != "" {
					return costCenter
				}
			}
		}
	}
	if strings.TrimSpace(project.TeamID) != "" {
		return project.TeamID
	}
	if strings.TrimSpace(project.ID) != "" {
		return "project:" + project.ID
	}
	return "unknown"
}

func (s *Server) filterAPIKeysForUser(user AdminUser, keys []APIKey) []APIKey {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return keys
	}
	out := make([]APIKey, 0, len(keys))
	for _, key := range keys {
		if s.canAccessAPIKey(user, key) {
			out = append(out, key)
		}
	}
	return out
}

func (s *Server) accessibleModelsForAdminUser(user AdminUser) []Model {
	if s.canViewGlobalOperations(user) {
		return s.store.ListModels()
	}
	routed := s.activeRoutedModelNameSet()
	models := s.store.ListModels()
	out := make([]Model, 0, len(models))
	for _, model := range models {
		if model.Status != StatusActive {
			continue
		}
		if routed[model.Name] || routed[model.ID] {
			out = append(out, model)
		}
	}
	return out
}

func (s *Server) activeRoutedModelNameSet() map[string]bool {
	out := map[string]bool{}
	for _, route := range s.store.ListRoutes() {
		if route.Status != StatusActive {
			continue
		}
		if modelName := strings.TrimSpace(route.ModelName); modelName != "" {
			out[modelName] = true
		}
	}
	return out
}

func (s *Server) canManageAPIKey(user AdminUser, keyID string) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	for _, key := range s.store.ListAPIKeys() {
		if key.ID == keyID {
			return s.canAccessAPIKey(user, key)
		}
	}
	return false
}

func (s *Server) canAccessAPIKey(user AdminUser, key APIKey) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	if key.Metadata != nil && key.Metadata["created_by"] == user.ID {
		return true
	}
	for _, project := range s.store.ListProjects() {
		if project.ID != key.ProjectID {
			continue
		}
		if project.OwnerUserID != "" && project.OwnerUserID == user.ID {
			return true
		}
		if role == "team_leader" && s.canAccessProject(user, project) {
			return true
		}
		if s.projectMemberCanManageKeys(user, project.ID) {
			return true
		}
	}
	return false
}

func (s *Server) canUseProjectForAPIKey(user AdminUser, projectID string) bool {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return true
	}
	for _, project := range s.store.ListProjects() {
		if project.ID != projectID {
			continue
		}
		if project.Status != "" && project.Status != StatusActive {
			return false
		}
		if project.OwnerUserID != "" && project.OwnerUserID == user.ID {
			return true
		}
		if role == "team_leader" && s.canAccessProject(user, project) {
			return true
		}
		return s.projectMemberCanIssueKey(user, project.ID)
	}
	return false
}

func (s *Server) projectMemberGrantsProjectAccess(user AdminUser, projectID string) bool {
	for _, member := range s.store.ListResources("project-members") {
		if !projectMemberMatches(member, projectID, user.ID) {
			continue
		}
		return true
	}
	return false
}

func (s *Server) projectMemberCanIssueKey(user AdminUser, projectID string) bool {
	for _, member := range s.store.ListResources("project-members") {
		if !projectMemberMatches(member, projectID, user.ID) {
			continue
		}
		if projectMemberRoleCanIssueKey(memberRole(member)) || truthyField(member.Fields, "can_issue_keys") {
			return true
		}
	}
	return false
}

func (s *Server) projectMemberCanManageKeys(user AdminUser, projectID string) bool {
	for _, member := range s.store.ListResources("project-members") {
		if !projectMemberMatches(member, projectID, user.ID) {
			continue
		}
		if projectMemberRoleCanManageKeys(memberRole(member)) {
			return true
		}
	}
	return false
}

func projectMemberMatches(member AdminResource, projectID string, userID string) bool {
	return member.Status == StatusActive &&
		strings.TrimSpace(stringField(member.Fields, "project_id")) == projectID &&
		strings.TrimSpace(stringField(member.Fields, "user_id")) == userID
}

func memberRole(member AdminResource) string {
	return strings.ToLower(strings.TrimSpace(stringField(member.Fields, "role")))
}

func projectMemberRoleCanIssueKey(role string) bool {
	switch role {
	case "owner", "maintainer", "developer":
		return true
	default:
		return false
	}
}

func projectMemberRoleCanManageKeys(role string) bool {
	switch role {
	case "owner", "maintainer":
		return true
	default:
		return false
	}
}

func truthyField(fields map[string]any, key string) bool {
	value := strings.ToLower(strings.TrimSpace(stringField(fields, key)))
	switch value {
	case "true", "1", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func (s *Server) filterAdminUsersForUser(user AdminUser, users []AdminUser) []AdminUser {
	role := normalizeAdminRole(user.Role)
	if isPlatformAdminRole(role) {
		return users
	}
	if role != "team_leader" {
		return nil
	}
	out := make([]AdminUser, 0, len(users))
	for _, item := range users {
		if item.TeamID == user.TeamID {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) filterApprovalRequestsForUser(user AdminUser, approvals []ApprovalRequest) []ApprovalRequest {
	if s.canViewGlobalOperations(user) {
		return approvals
	}
	role := normalizeAdminRole(user.Role)
	if role != "team_leader" {
		return nil
	}
	teamUsers := map[string]bool{user.ID: true}
	for _, item := range s.store.ListAdminUsers() {
		if item.TeamID == user.TeamID {
			teamUsers[item.ID] = true
		}
	}
	out := make([]ApprovalRequest, 0, len(approvals))
	for _, item := range approvals {
		if teamUsers[item.RequesterID] {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) canExportKind(user AdminUser, kind string) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	role := normalizeAdminRole(user.Role)
	switch role {
	case "team_leader":
		switch kind {
		case "requests", "usage", "cost-centers", "budgets", "chargebacks", "invoices", "approvals":
			return true
		default:
			return false
		}
	case "user":
		return kind == "requests" || kind == "usage"
	default:
		return false
	}
}

func (s *Server) findAdminUser(userID string) (AdminUser, bool) {
	for _, user := range s.store.ListAdminUsers() {
		if user.ID == userID {
			return user, true
		}
	}
	return AdminUser{}, false
}

func (s *Server) filterResourcesForUser(user AdminUser, kind string, resources []AdminResource) []AdminResource {
	role := normalizeAdminRole(user.Role)
	if s.canViewGlobalOperations(user) {
		return resources
	}
	if role == "user" && kind == "project-members" {
		out := make([]AdminResource, 0, len(resources))
		for _, item := range resources {
			if item.Status == StatusActive && strings.TrimSpace(stringField(item.Fields, "user_id")) == user.ID {
				out = append(out, item)
			}
		}
		return out
	}
	if role != "team_leader" {
		return nil
	}
	out := make([]AdminResource, 0, len(resources))
	for _, item := range resources {
		if s.canAccessScopedResource(user, kind, item) {
			out = append(out, item)
		}
	}
	return out
}

func (s *Server) canAccessScopedResource(user AdminUser, kind string, item AdminResource) bool {
	switch kind {
	case "teams":
		return item.ID == user.TeamID
	case "cost-centers":
		return s.resourceMatchesTeamCostCenter(user.TeamID, item)
	case "budgets":
		scope := strings.ToLower(strings.TrimSpace(stringField(item.Fields, "scope")))
		scopeID := strings.TrimSpace(stringField(item.Fields, "scope_id"))
		if scope == "team" {
			return scopeID == user.TeamID
		}
		if scope == "cost_center" || scope == "cost-center" {
			return s.teamCostCenterSet(user.TeamID)[normalizeScopeValue(scopeID)]
		}
		return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
	case "quota-policies":
		return s.canAccessQuotaPolicy(user, item)
	case "project-members":
		return s.canAccessProjectMemberResource(user, item)
	case "chargebacks", "invoices":
		return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
	default:
		return false
	}
}

func (s *Server) canAccessProjectMemberResource(user AdminUser, item AdminResource) bool {
	projectID := strings.TrimSpace(stringField(item.Fields, "project_id"))
	if projectID == "" {
		return false
	}
	project, ok := s.store.GetProject(projectID)
	if !ok || !s.canAccessProject(user, project) {
		return false
	}
	if normalizeAdminRole(user.Role) != "team_leader" {
		return true
	}
	targetUserID := strings.TrimSpace(stringField(item.Fields, "user_id"))
	targetUser, ok := s.findAdminUser(targetUserID)
	return ok && targetUser.TeamID == user.TeamID
}

func (s *Server) canAccessQuotaPolicy(user AdminUser, item AdminResource) bool {
	if s.canViewGlobalOperations(user) {
		return true
	}
	if normalizeAdminRole(user.Role) != "team_leader" {
		return false
	}
	scope := strings.ToLower(strings.TrimSpace(firstStringField(item.Fields, "scope", "scope_type")))
	scopeID := strings.TrimSpace(stringField(item.Fields, "scope_id"))
	switch scope {
	case "project":
		return s.visibleProjectIDSet(user)[scopeID]
	case "team":
		return scopeID == user.TeamID
	case "cost_center", "cost-center":
		return s.teamCostCenterSet(user.TeamID)[normalizeScopeValue(scopeID)]
	}
	for _, project := range s.filterProjectsForUser(user, s.store.ListProjects()) {
		if strings.TrimSpace(project.DefaultQuotaRef) == item.ID {
			return true
		}
	}
	return s.resourceMatchesTeamOrCostCenter(user.TeamID, item)
}

func (s *Server) validateScopedResourceMutation(user AdminUser, kind string, resourceID string, req AdminResource) error {
	if kind == "project-members" {
		return s.validateProjectMemberMutation(user, resourceID, req)
	}
	if normalizeAdminRole(user.Role) != "team_leader" || kind != "quota-policies" {
		return nil
	}
	if resourceID != "" {
		existing, err := s.findResource(kind, resourceID)
		if err != nil {
			return err
		}
		if !s.canAccessQuotaPolicy(user, existing) {
			return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Quota policy is not available for this user")
		}
		if req.Fields == nil {
			return nil
		}
	}
	if !s.quotaPolicyReferencesVisibleProject(user, req) {
		return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Quota policy must belong to a visible project")
	}
	return nil
}

func (s *Server) validateProjectMemberMutation(user AdminUser, resourceID string, req AdminResource) error {
	var existing AdminResource
	var err error
	if resourceID != "" {
		existing, err = s.findResource("project-members", resourceID)
		if err != nil {
			return err
		}
		if normalizeAdminRole(user.Role) == "team_leader" && !s.canAccessProjectMemberResource(user, existing) {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Project member is not available for this user")
		}
	}
	fields := req.Fields
	if fields == nil {
		fields = existing.Fields
	}
	projectID := strings.TrimSpace(stringField(fields, "project_id"))
	userID := strings.TrimSpace(stringField(fields, "user_id"))
	role := strings.ToLower(strings.TrimSpace(stringField(fields, "role")))
	if projectID == "" || userID == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "project_id and user_id are required")
	}
	if role == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "role is required")
	}
	if !validProjectMemberRole(role) {
		return NewHTTPError(http.StatusBadRequest, "invalid_project_member", "role must be owner, maintainer, developer, or viewer")
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		return NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
	}
	targetUser, ok := s.findAdminUser(userID)
	if !ok {
		return NewHTTPError(http.StatusNotFound, "admin_user_not_found", "Admin user not found")
	}
	if normalizeAdminRole(user.Role) == "team_leader" {
		if strings.TrimSpace(project.TeamID) == "" || project.TeamID != user.TeamID {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Team leader can only assign own team projects")
		}
		if targetUser.TeamID != user.TeamID || normalizeAdminRole(targetUser.Role) != "user" {
			return NewHTTPError(http.StatusForbidden, "project_member_forbidden", "Team leader can only assign ordinary users in own team")
		}
	}
	for _, item := range s.store.ListResources("project-members") {
		if item.ID == resourceID {
			continue
		}
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			return NewHTTPError(http.StatusConflict, "project_member_conflict", "User is already assigned to this project")
		}
	}
	return nil
}

func validProjectMemberRole(role string) bool {
	switch role {
	case "owner", "maintainer", "developer", "viewer":
		return true
	default:
		return false
	}
}

func (s *Server) quotaPolicyReferencesVisibleProject(user AdminUser, item AdminResource) bool {
	projectID := quotaPolicyProjectID(item)
	if projectID == "" {
		return false
	}
	return s.visibleProjectIDSet(user)[projectID]
}

func quotaPolicyProjectID(item AdminResource) string {
	scope := strings.ToLower(strings.TrimSpace(firstStringField(item.Fields, "scope", "scope_type")))
	if scope != "project" {
		return ""
	}
	return strings.TrimSpace(firstStringField(item.Fields, "scope_id", "project_id"))
}

func (s *Server) resourceMatchesTeamOrCostCenter(teamID string, item AdminResource) bool {
	if strings.TrimSpace(stringField(item.Fields, "team_id")) == teamID {
		return true
	}
	return s.resourceMatchesTeamCostCenter(teamID, item)
}

func (s *Server) resourceMatchesTeamCostCenter(teamID string, item AdminResource) bool {
	costCenters := s.teamCostCenterSet(teamID)
	for _, value := range []string{
		item.ID,
		item.Name,
		stringField(item.Fields, "code"),
		stringField(item.Fields, "cost_center"),
		stringField(item.Fields, "scope_id"),
	} {
		if costCenters[normalizeScopeValue(value)] {
			return true
		}
	}
	return false
}

func (s *Server) teamCostCenterSet(teamID string) map[string]bool {
	out := map[string]bool{}
	for _, team := range s.store.ListResources("teams") {
		if team.ID == teamID {
			addScopeValue(out, stringField(team.Fields, "cost_center"))
			break
		}
	}
	for _, project := range s.store.ListProjects() {
		if project.TeamID == teamID {
			addScopeValue(out, project.CostCenter)
			addScopeValue(out, s.costCenterForProject(project))
		}
	}
	return out
}

func addScopeValue(set map[string]bool, value string) {
	if normalized := normalizeScopeValue(value); normalized != "" {
		set[normalized] = true
	}
}

func normalizeScopeValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func adminResourcePermission(path string) string {
	if strings.Contains(path, "/quota-policies") {
		return "quota"
	}
	if strings.Contains(path, "/project-members") {
		return "project"
	}
	if strings.Contains(path, "/security-policies") {
		return "security"
	}
	if strings.Contains(path, "/identity-providers") {
		return "security"
	}
	if strings.Contains(path, "/alert-rules") {
		return "alert"
	}
	if strings.Contains(path, "/notification-channels") {
		return "alert"
	}
	if strings.Contains(path, "/cost-centers") || strings.Contains(path, "/budgets") ||
		strings.Contains(path, "/chargebacks") || strings.Contains(path, "/invoices") ||
		strings.Contains(path, "/approval-flows") || strings.Contains(path, "/reports") {
		return "usage"
	}
	if strings.Contains(path, "/teams") || strings.Contains(path, "/role-configs") {
		return "identity"
	}
	if strings.Contains(path, "/monitors") || strings.Contains(path, "/proxies") || strings.Contains(path, "/settings") {
		return "provider"
	}
	return "overview"
}

func (s *Server) recordAdminAudit(r *http.Request, user AdminUser, action string, resourceType string, resourceID string, before any, after any) {
	s.store.RecordAuditEvent(AuditEvent{
		ActorUserID:    user.ID,
		ActorName:      user.Name,
		ActorRole:      user.Role,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Status:         "success",
		BeforeSnapshot: snapshotJSON(before),
		AfterSnapshot:  snapshotJSON(after),
		IP:             s.clientIP(r),
		UserAgent:      r.UserAgent(),
	})
}

func snapshotJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func stringifyCSV(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.Trim(snapshotJSON(typed), `"`)
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	httpErr := AsHTTPError(err)
	requestID := strings.TrimSpace(w.Header().Get("x-request-id"))
	if requestID == "" {
		requestID = NewID("req")
	}
	w.Header().Set("x-request-id", requestID)
	writeJSON(w, httpErr.Status, map[string]any{
		"error": map[string]any{
			"message": httpErr.Message,
			"type":    httpErr.Code,
			"code":    httpErr.Code,
		},
		"request_id": requestID,
	})
}

const auditPayloadMaxChars = 64 * 1024

func (s *Server) recordRequestPayload(requestID string, requestPayload any, responsePayload any) {
	requestBody, requestTruncated := auditPayloadBody(requestPayload)
	responseBody, responseTruncated := auditPayloadBody(responsePayload)
	s.store.RecordRequestPayload(requestID, requestBody, requestTruncated, responseBody, responseTruncated)
}

func auditPayloadBody(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	raw, err := json.MarshalIndent(redactAuditPayload(value), "", "  ")
	if err != nil {
		raw, _ = json.MarshalIndent(map[string]any{"error": "payload_not_serializable"}, "", "  ")
	}
	text := string(raw)
	runes := []rune(text)
	if len(runes) <= auditPayloadMaxChars {
		return text, false
	}
	return string(runes[:auditPayloadMaxChars]) + "\n... truncated", true
}

func redactAuditPayload(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return value
	}
	return redactAuditValue(decoded)
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveAuditKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactAuditValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactAuditValue(item))
		}
		return out
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	switch normalized {
	case "authorization", "apikey", "accesstoken", "refreshtoken", "clientsecret", "secretkey", "password", "token", "secret":
		return true
	default:
		return strings.Contains(normalized, "authorization") || strings.Contains(normalized, "password") || strings.Contains(normalized, "secret")
	}
}

func auditErrorPayload(err error, requestID string) map[string]any {
	httpErr := AsHTTPError(err)
	return map[string]any{
		"error": map[string]any{
			"message": httpErr.Message,
			"type":    httpErr.Code,
			"code":    httpErr.Code,
		},
		"request_id": requestID,
	}
}

func auditStreamPayload(status int, code string, err error) map[string]any {
	payload := map[string]any{
		"stream":      true,
		"captured":    false,
		"status_code": status,
	}
	if err != nil {
		payload["error"] = map[string]any{
			"message": errorMessage(err),
			"type":    code,
			"code":    code,
		}
	}
	return payload
}

func statusAndCode(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	httpErr := AsHTTPError(err)
	return httpErr.Status, httpErr.Code
}

func (s *Server) clientIP(r *http.Request) string {
	remoteIP := requestRemoteIP(r)
	if !ipMatchesTrustedProxy(remoteIP, s.config.TrustedProxyCIDRs) {
		return remoteIP
	}
	forwarded := strings.Split(r.Header.Get("x-forwarded-for"), ",")
	parsed := make([]string, 0, len(forwarded))
	for _, value := range forwarded {
		value = strings.TrimSpace(value)
		ip := net.ParseIP(value)
		if value == "" || ip == nil {
			return remoteIP
		}
		parsed = append(parsed, ip.String())
	}
	for index := len(parsed) - 1; index >= 0; index-- {
		if !ipMatchesTrustedProxy(parsed[index], s.config.TrustedProxyCIDRs) {
			return parsed[index]
		}
	}
	if len(parsed) > 0 {
		return parsed[0]
	}
	return remoteIP
}

func requestRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
}

func ipMatchesTrustedProxy(rawIP string, trusted []string) bool {
	ip := net.ParseIP(strings.TrimSpace(rawIP))
	if ip == nil {
		return false
	}
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func isDevEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "dev", "development", "local", "test":
		return true
	}
	return false
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Determine allowed origins: use explicit config if set, otherwise derive from PublicBaseURL
		allowedOrigins := s.config.CORSAllowedOrigins
		if len(allowedOrigins) == 0 && s.config.PublicBaseURL != "" {
			allowedOrigins = []string{s.config.PublicBaseURL}
		}

		// If request has Origin header and we have an allowed origins list, validate it
		if origin != "" && len(allowedOrigins) > 0 {
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if origin == strings.TrimSpace(allowedOrigin) {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("access-control-allow-origin", origin)
				w.Header().Set("access-control-allow-credentials", "true")
				w.Header().Add("Vary", "Origin")
			} else {
				// Origin not in allowlist, deny credentials but allow simple requests
				w.Header().Set("access-control-allow-origin", "*")
			}
		} else if origin != "" && isDevEnvironment(s.config.Environment) {
			// Dev environment without an allowlist: echo origin with credentials
			// for convenience. Never do this in production, where an explicit
			// allowlist (or PublicBaseURL) is required to authorize credentials.
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("access-control-allow-credentials", "true")
			w.Header().Add("Vary", "Origin")
		} else {
			// No Origin header, or production with no configured allowlist:
			// allow simple cross-origin requests but never credentials.
			w.Header().Set("access-control-allow-origin", "*")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
			}
		}

		w.Header().Set("access-control-allow-methods", "GET,POST,PATCH,DELETE,OPTIONS")
		allowHeaders := "authorization,content-type"
		if reqHeaders := r.Header.Get("access-control-request-headers"); reqHeaders != "" {
			seen := map[string]bool{"authorization": true, "content-type": true}
			for _, h := range strings.Split(reqHeaders, ",") {
				h = strings.ToLower(strings.TrimSpace(h))
				if h != "" && !seen[h] {
					allowHeaders += "," + h
					seen[h] = true
				}
			}
		}
		w.Header().Set("access-control-allow-headers", allowHeaders)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAdminSystemDBStatus(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify administrator permissions.
	if _, ok := s.requireAdmin(w, r, "system", r.Method); !ok {
		return
	}

	// Retrieve the database status.
	status, err := s.store.GetDatabaseStatus()
	if err != nil {
		log.Printf("[tokenhub] failed to get database status: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return the JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("[tokenhub] failed to encode database status response: %v", err)
	}
}
