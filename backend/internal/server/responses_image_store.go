package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/dbschema"
)

const (
	// responsesImageReplayBytes is how far /v1/responses will read so a Codex
	// replay can be shrunk before the configured forward limit is applied.
	responsesImageReplayBytes = 128 << 20
	// responsesImageStoreTTL matches the upstream Responses retention window.
	responsesImageStoreTTL = 30 * 24 * time.Hour
	// responsesInlineImageMinChars ignores tiny data URLs used by fixtures.
	responsesInlineImageMinChars = 1024
)

// responsesImageStoreBinding pins one Codex session to the upstream response
// that still holds its images. The key mixes the API key and public model, so
// one caller cannot continue another caller's stored response.
type responsesImageStoreBinding struct {
	KeyHash       string `gorm:"primaryKey"`
	RouteID       string
	ProviderID    string
	ProviderModel string
	ResourceID    string
	UpstreamID    string
	InputBoundary string
	ExpiresAt     int64
}

func (responsesImageStoreBinding) TableName() string {
	return "responses_image_store_bindings"
}

func responsesImageStoreMigration() dbschema.Migration {
	return dbschema.Migration{Version: 11, Name: "add-responses-image-store-bindings", Statements: []string{
		`CREATE TABLE IF NOT EXISTS responses_image_store_bindings (key_hash text PRIMARY KEY, route_id text NOT NULL, provider_id text NOT NULL, provider_model text NOT NULL, resource_id text NOT NULL, upstream_id text NOT NULL, input_boundary text NOT NULL, expires_at bigint NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_responses_image_store_bindings_expiry ON responses_image_store_bindings (expires_at)`,
	}, StatementBudget: 10}
}

type responsesImageStore interface {
	LoadResponsesImageStore(context.Context, string) (responsesImageStoreBinding, bool, error)
	SaveResponsesImageStore(context.Context, responsesImageStoreBinding) error
	DeleteResponsesImageStore(context.Context, string) error
}

func (s *GormStore) LoadResponsesImageStore(ctx context.Context, key string) (responsesImageStoreBinding, bool, error) {
	var binding responsesImageStoreBinding
	err := s.db.WithContext(ctx).Where("key_hash = ? AND expires_at > ?", key, time.Now().Unix()).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, false, nil
	}
	return binding, err == nil, err
}

func (s *GormStore) SaveResponsesImageStore(ctx context.Context, binding responsesImageStoreBinding) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return saveResponsesImageStoreBinding(tx, binding)
	})
}

func (s *GormStore) DeleteResponsesImageStore(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	return s.db.WithContext(ctx).Where("key_hash = ?", key).Delete(&responsesImageStoreBinding{}).Error
}

func saveResponsesImageStoreBinding(tx *gorm.DB, binding responsesImageStoreBinding) error {
	// Bound each cleanup batch; expiration never requires an unbounded scan.
	if err := tx.Exec("DELETE FROM responses_image_store_bindings WHERE key_hash IN (SELECT key_hash FROM responses_image_store_bindings WHERE expires_at <= ? LIMIT 100)", time.Now().Unix()).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"route_id", "provider_id", "provider_model", "resource_id", "upstream_id", "input_boundary", "expires_at"}),
	}).Create(&binding).Error
}

// responsesDecodeLimit admits a Responses replay that is larger than the body
// TokenHub is willing to forward on a store-capable route. ChatGPT subscription
// and Codex routes forward up to this same ceiling, because those APIs reject
// store and cannot drop already seen images.
func (s *Server) responsesDecodeLimit() int64 {
	limit := max(s.configuredMultimodalLimit(), int64(responsesImageReplayBytes))
	if limit > maxConfigurableRequestBytes {
		return maxConfigurableRequestBytes
	}
	return limit
}

func (s *Server) configuredMultimodalLimit() int64 {
	if s.config.MaxMultimodalRequestBytes > 0 {
		return s.config.MaxMultimodalRequestBytes
	}
	return defaultMaxMultimodalRequestBytes
}

func (s *Server) applyResponsesImageStore(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	if err := s.prepareResponsesImageStore(ctx, routed, req, headers); err != nil {
		return err
	}
	var routes []RouteSelection
	if routed != nil {
		routes = routed.Routes
	}
	return s.rejectOversizedResponsesRequest(*req, routes)
}

func (s *Server) prepareResponsesImageStore(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	if req == nil || routed == nil {
		return nil
	}
	// The caller already owns continuation state. conversation and
	// previous_response_id cannot be sent together.
	if responsesRawStringField(*req, "previous_response_id") != "" || responsesRawFieldPresent(*req, "conversation") {
		return nil
	}
	if !routesAllowResponsesImageStore(routed.Routes) {
		return nil
	}
	key, ok := s.responsesImageStoreKey(routed.Call, *req, headers)
	if !ok {
		return nil
	}
	store, ok := s.store.(responsesImageStore)
	if !ok {
		return nil
	}
	binding, found, err := store.LoadResponsesImageStore(ctx, key)
	if err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be read")
	}
	if found {
		suffix, matched := responsesImageStoreNewItems(req.Input, binding.InputBoundary)
		boundary := ""
		if matched {
			boundary = responsesImageStoreExtendedBoundary(binding.InputBoundary, suffix)
		}
		if matched && boundary != "" && pinResponsesImageStoreRoute(routed, binding) {
			original := cloneResponsesRequest(*req)
			forceResponsesStore(req)
			setResponsesPreviousResponseID(req, binding.UpstreamID)
			assignResponsesInput(req, suffix)
			req.imageStoreBoundary = boundary
			req.imageContinuation = &responsesImageContinuation{
				key: key, boundary: boundary, rewritten: true, original: original,
			}
			return nil
		}
	}
	// No usable stored response. Keep every image. Ask the upstream to retain
	// this turn only when it actually carries inline images; otherwise an
	// oversized body fails with the configured limit instead of being trimmed.
	if !responsesRequestHasInlineImages(*req) {
		return nil
	}
	forceResponsesStore(req)
	boundary := responsesImageStoreBoundary(req.Input)
	req.imageStoreBoundary = boundary
	req.imageContinuation = &responsesImageContinuation{key: key, boundary: boundary}
	return nil
}

func (s *Server) rejectOversizedResponsesRequest(req ResponsesRequest, routes []RouteSelection) error {
	limit := s.responsesForwardLimit(routes)
	if int64(responsesMarshaledBytes(req)) > limit {
		return NewHTTPError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds "+strconv.FormatInt(limit, 10)+" bytes")
	}
	return nil
}

// responsesForwardLimit keeps the configured multimodal cap unless every
// candidate is a ChatGPT subscription or Codex route. A mixed failover set
// stays at that cap so a store-capable target never receives an unshrunk body.
func (s *Server) responsesForwardLimit(routes []RouteSelection) int64 {
	if routesForwardFullResponsesReplay(routes) {
		return s.responsesDecodeLimit()
	}
	return s.configuredMultimodalLimit()
}

func routesForwardFullResponsesReplay(routes []RouteSelection) bool {
	if len(routes) == 0 {
		return false
	}
	for _, route := range routes {
		switch strings.TrimSpace(route.Provider.Type) {
		case ProviderOpenAICodex, ProviderChatGPTSubscription:
		default:
			return false
		}
	}
	return true
}

func (s *Server) rememberResponsesImageStore(ctx context.Context, call CallContext, route RouteSelection, headers http.Header, req ResponsesRequest, response any) error {
	pending, err := s.pendingResponsesImageStore(call, route, headers, req, response)
	if err != nil || pending == nil {
		return err
	}
	store, ok := s.store.(responsesImageStore)
	if !ok {
		return nil
	}
	if err := store.SaveResponsesImageStore(ctx, *pending); err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be saved")
	}
	return nil
}

// pendingResponsesImageStore captures a binding without publishing it. Background
// jobs commit that binding in the same transaction as an approved completion.
func (s *Server) pendingResponsesImageStore(call CallContext, route RouteSelection, headers http.Header, req ResponsesRequest, response any) (*responsesImageStoreBinding, error) {
	captureResponsesImageOutput(&req, response)
	boundary := responsesImageBoundaryForSave(req)
	if boundary == "" || req.Store == nil || !*req.Store || !routeAllowsResponsesImageStore(route) {
		return nil, nil
	}
	upstreamID := strings.TrimSpace(jevResponseID(response))
	if upstreamID == "" {
		return nil, nil
	}
	key, ok := s.responsesImageStoreKey(call, req, headers)
	if !ok {
		return nil, nil
	}
	if _, ok := s.store.(responsesImageStore); !ok {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be saved")
	}
	return &responsesImageStoreBinding{
		KeyHash:       key,
		RouteID:       route.Route.ID,
		ProviderID:    route.Provider.ID,
		ProviderModel: route.ProviderModel,
		ResourceID:    routeResourceID(route),
		UpstreamID:    upstreamID,
		InputBoundary: boundary,
		ExpiresAt:     time.Now().Add(responsesImageStoreTTL).Unix(),
	}, nil
}

func (s *Server) responsesImageStoreKey(call CallContext, req ResponsesRequest, headers http.Header) (string, bool) {
	session, ok := responsesImageStoreSessionID(headers, req)
	if !ok || validateSessionIdentifier(session, "session_id_invalid", "Session identifier") != nil {
		return "", false
	}
	model := strings.TrimSpace(call.Model.Name)
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	if model == "" {
		return "", false
	}
	return deriveSessionAffinityKey(s.config.SecretKey, call.Key.ID, "responses-image-store\x00"+model+"\x00"+session), true
}

// routesAllowResponsesImageStore requires every candidate to accept stored
// continuations. One store:false account in the failover set would otherwise
// receive a transcript whose images had already been removed.
func routesAllowResponsesImageStore(routes []RouteSelection) bool {
	if len(routes) == 0 {
		return false
	}
	for _, route := range routes {
		if !routeAllowsResponsesImageStore(route) {
			return false
		}
	}
	return true
}

func routeAllowsResponsesImageStore(route RouteSelection) bool {
	// Subscription adapters delete previous_response_id and force store false.
	// Stripping images on those routes would drop the only copy the model has.
	switch strings.TrimSpace(route.Provider.Type) {
	case ProviderOpenAICodex, ProviderChatGPTSubscription:
		return false
	}
	if value, ok := responsesStoreSwitch(route); ok {
		return value == "on"
	}
	// OpenAI and Azure document stored responses. Other APIs, including
	// OpenAI-compatible ones, stay off until responses_store=on.
	switch strings.TrimSpace(route.Provider.Type) {
	case ProviderOpenAI, ProviderAzureOpenAI:
		return true
	default:
		return false
	}
}

// responsesStoreSwitch reads responses_store. A resource value wins over the
// provider value. Only "on" and "off" are meaningful.
func responsesStoreSwitch(route RouteSelection) (string, bool) {
	if route.Resource != nil {
		if value, ok := responsesStoreSwitchValue(route.Resource.Options); ok {
			return value, true
		}
	}
	return responsesStoreSwitchValue(route.Provider.Options)
}

func responsesStoreSwitchValue(options map[string]string) (string, bool) {
	return namedOptionSwitch(options, "responses_store")
}

func pinResponsesImageStoreRoute(routed *RoutedCall, binding responsesImageStoreBinding) bool {
	for _, route := range routed.Routes {
		if route.Route.ID == binding.RouteID && route.Provider.ID == binding.ProviderID && route.ProviderModel == binding.ProviderModel && routeResourceID(route) == binding.ResourceID && binding.UpstreamID != "" {
			routed.Routes = []RouteSelection{route}
			return true
		}
	}
	return false
}

func forceResponsesStore(req *ResponsesRequest) {
	if req == nil {
		return
	}
	enabled := true
	req.Store = &enabled
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "store", true, true)
}

func setResponsesPreviousResponseID(req *ResponsesRequest, id string) {
	if req == nil || strings.TrimSpace(id) == "" {
		return
	}
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "previous_response_id", id, true)
}

func assignResponsesInput(req *ResponsesRequest, input any) {
	if req == nil {
		return
	}
	req.Input = input
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "input", input, input != nil)
}

func responsesMarshaledBytes(req ResponsesRequest) int {
	data, err := json.Marshal(req)
	if err != nil {
		return 0
	}
	return len(data)
}

// responsesImageStoreSessionID accepts an explicit Codex session. prompt_cache_key
// is a cache-routing hint and may be shared by unrelated conversations.
func responsesImageStoreSessionID(headers http.Header, request ResponsesRequest) (string, bool) {
	for _, value := range []string{
		headers.Get("session-id"),
		headers.Get("session_id"),
		compatibilityClientMetadataSessionID(request),
		headers.Get("thread-id"),
		headers.Get("x-client-request-id"),
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value, true
		}
	}
	return "", false
}

func responsesRawFieldPresent(request ResponsesRequest, key string) bool {
	if request.raw == nil {
		return false
	}
	raw, ok := request.raw[key]
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// responsesImageStoreNewItems returns the items that were not already accepted
// into the stored response. Input items must match in order. Assistant and
// tool-call echoes are removed only when they match the stored output; a
// different answer falls back to the full replay. Omitting those echoes is
// allowed because the stored response already contains them. A boundary saved
// as a JSON array of hashes predates output identity and still skips echoes.
func responsesImageStoreNewItems(input any, boundary string) ([]any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	decoded, ok := decodeResponsesImageBoundary(boundary)
	if !ok || len(decoded.Turns) == 0 {
		return nil, false
	}
	index := 0
	for _, turn := range decoded.Turns {
		if len(turn.Input) == 0 {
			return nil, false
		}
		if turn.Legacy {
			next, matched := matchLegacyImageHashes(items, index, turn.Input)
			if !matched {
				return nil, false
			}
			index = next
			continue
		}
		for _, hash := range turn.Input {
			if index >= len(items) || responsesInputItemHash(items[index]) != hash {
				return nil, false
			}
			index++
		}
		if index < len(items) && responsesImageStoreReplayEcho(items[index]) {
			for _, want := range turn.Output {
				if index >= len(items) || !responsesImageStoreReplayEcho(items[index]) || responsesEchoIdentity(items[index]) != want {
					return nil, false
				}
				index++
			}
			if index < len(items) && responsesImageStoreReplayEcho(items[index]) {
				return nil, false
			}
		}
	}
	if index >= len(items) || responsesImageStoreReplayEcho(items[index]) {
		return nil, false
	}
	return append([]any{}, items[index:]...), true
}

func responsesImageStoreBoundary(input any) string {
	items, ok := input.([]any)
	if !ok {
		if input == nil {
			return ""
		}
		items = []any{input}
	}
	if len(items) == 0 {
		return ""
	}
	return responsesImageStoreExtendedBoundary(emptyResponsesImageStoreBoundary(), items)
}

func emptyResponsesImageStoreBoundary() string {
	return `{"turns":[]}`
}

func responsesImageStoreExtendedBoundary(existing string, suffix []any) string {
	decoded, ok := decodeResponsesImageBoundary(existing)
	if !ok {
		return ""
	}
	hashes := make([]string, 0, len(suffix))
	for _, item := range suffix {
		hash := responsesInputItemHash(item)
		if hash == "" {
			return ""
		}
		hashes = append(hashes, hash)
	}
	if len(hashes) == 0 {
		return ""
	}
	decoded.Turns = append(decoded.Turns, responsesImageTurn{Input: hashes, Output: []string{}})
	return encodeResponsesImageBoundary(decoded)
}

func responsesInputItemHash(item any) string {
	data, err := json.Marshal(item)
	if err != nil || len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// responsesImageStoreReplayEcho reports items the stored response already
// contains as model output. User messages and tool results are new input.
func responsesImageStoreReplayEcho(item any) bool {
	message, ok := item.(map[string]any)
	if !ok {
		return false
	}
	if role, _ := message["role"].(string); strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return true
	}
	kind, _ := message["type"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "reasoning", "function_call", "custom_tool_call", "web_search_call", "file_search_call", "computer_call", "image_generation_call", "mcp_call", "local_shell_call", "tool_call":
		return true
	default:
		return false
	}
}

func responsesRequestHasInlineImages(req ResponsesRequest) bool {
	return responsesValueHasInlineImage(req.Input)
}

func responsesValueHasInlineImage(value any) bool {
	switch typed := value.(type) {
	case string:
		return inlineImageDataURL(typed)
	case map[string]any:
		for _, child := range typed {
			if responsesValueHasInlineImage(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if responsesValueHasInlineImage(child) {
				return true
			}
		}
	}
	return false
}

func inlineImageDataURL(value string) bool {
	if len(value) <= responsesInlineImageMinChars {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:image/")
}

// responsesImageContinuation is shared by copies of one request. Background
// completion and a missing-response retry both observe the same pointer.
type responsesImageContinuation struct {
	key          string
	boundary     string
	output       []string
	outputOK     bool
	outputFailed bool
	rewritten    bool
	tried        bool
	original     ResponsesRequest
}

type responsesImageTurn struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
	// Legacy marks hashes stored before output identity existed. Those turns
	// skip assistant and tool-call echoes instead of verifying them.
	Legacy bool `json:"legacy,omitempty"`
}

type responsesImageBoundary struct {
	Turns []responsesImageTurn `json:"turns"`
}

func decodeResponsesImageBoundary(raw string) (responsesImageBoundary, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return responsesImageBoundary{}, false
	}
	var decoded responsesImageBoundary
	if json.Unmarshal([]byte(trimmed), &decoded) == nil && decoded.Turns != nil {
		return decoded, true
	}
	return decodeLegacyImageBoundary(trimmed)
}

// decodeLegacyImageBoundary reads the hash array written by the previous head.
// An empty array, a blank hash, or a non-string element stays invalid.
func decodeLegacyImageBoundary(raw string) (responsesImageBoundary, bool) {
	if !strings.HasPrefix(raw, "[") {
		return responsesImageBoundary{}, false
	}
	var hashes []string
	if json.Unmarshal([]byte(raw), &hashes) != nil || len(hashes) == 0 {
		return responsesImageBoundary{}, false
	}
	for _, hash := range hashes {
		if hash == "" {
			return responsesImageBoundary{}, false
		}
	}
	return responsesImageBoundary{Turns: []responsesImageTurn{{
		Input: hashes, Output: []string{}, Legacy: true,
	}}}, true
}

// matchLegacyImageHashes matches input hashes while skipping echoes between
// them and after the last one. A non-echo that is not the next hash fails.
func matchLegacyImageHashes(items []any, index int, hashes []string) (int, bool) {
	for _, hash := range hashes {
		if hash == "" {
			return 0, false
		}
		matched := false
		for index < len(items) {
			if responsesInputItemHash(items[index]) == hash {
				matched = true
				index++
				break
			}
			if !responsesImageStoreReplayEcho(items[index]) {
				return 0, false
			}
			index++
		}
		if !matched {
			return 0, false
		}
	}
	for index < len(items) && responsesImageStoreReplayEcho(items[index]) {
		index++
	}
	return index, true
}

func encodeResponsesImageBoundary(boundary responsesImageBoundary) string {
	if len(boundary.Turns) == 0 {
		return ""
	}
	encoded, err := json.Marshal(boundary)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func responsesImageStoreApplyOutput(boundary string, output []string) string {
	decoded, ok := decodeResponsesImageBoundary(boundary)
	if !ok || len(decoded.Turns) == 0 {
		return ""
	}
	if output == nil {
		output = []string{}
	}
	decoded.Turns[len(decoded.Turns)-1].Output = output
	return encodeResponsesImageBoundary(decoded)
}

func responsesImageStoreWithOutput(boundary string, response any) string {
	identities, ok := responsesOutputIdentities(response)
	if !ok {
		return ""
	}
	return responsesImageStoreApplyOutput(boundary, identities)
}

func responsesImageBoundaryForSave(req ResponsesRequest) string {
	state := req.imageContinuation
	if state == nil || state.outputFailed || !state.outputOK {
		return ""
	}
	boundary := state.boundary
	if boundary == "" {
		boundary = req.imageStoreBoundary
	}
	return responsesImageStoreApplyOutput(boundary, state.output)
}

func captureResponsesImageOutput(req *ResponsesRequest, response any) {
	if req == nil || req.imageContinuation == nil || req.imageContinuation.outputOK || req.imageContinuation.outputFailed {
		return
	}
	identities, ok := responsesOutputIdentities(response)
	if !ok {
		req.imageContinuation.outputFailed = true
		req.imageContinuation.boundary = ""
		return
	}
	req.imageContinuation.output = identities
	req.imageContinuation.outputOK = true
}

func responsesOutputIdentities(response any) ([]string, bool) {
	data, err := json.Marshal(response)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return nil, false
	}
	raw, ok := envelope["output"]
	if !ok || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil, false
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	identities := make([]string, 0, len(items))
	for _, item := range items {
		if !responsesImageStoreReplayEcho(item) {
			continue
		}
		identity := responsesEchoIdentity(item)
		if identity == "" {
			return nil, false
		}
		identities = append(identities, identity)
	}
	return identities, true
}

// prepareResponsesImageStoreRetry drops a stored response the upstream no longer
// has and returns the original transcript for one retry. The retry stays under
// the normal forward limit. A second miss does not retry again.
func (s *Server) prepareResponsesImageStoreRetry(ctx context.Context, route RouteSelection, req *ResponsesRequest, err error) (ResponsesRequest, bool, error) {
	if req == nil || req.imageContinuation == nil || !req.imageContinuation.rewritten || req.imageContinuation.tried || !responsesStoredResponseMissing(err) {
		return ResponsesRequest{}, false, nil
	}
	state := req.imageContinuation
	state.tried = true
	store, ok := s.store.(responsesImageStore)
	if !ok {
		return ResponsesRequest{}, false, NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be cleared")
	}
	if deleteErr := store.DeleteResponsesImageStore(ctx, state.key); deleteErr != nil {
		return ResponsesRequest{}, false, NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be cleared")
	}
	original := state.original
	if responsesRequestHasInlineImages(original) && routeAllowsResponsesImageStore(route) {
		forceResponsesStore(&original)
	}
	if limitErr := s.rejectOversizedResponsesRequest(original, []RouteSelection{route}); limitErr != nil {
		state.boundary = ""
		state.outputFailed = true
		return ResponsesRequest{}, false, limitErr
	}
	state.output = nil
	state.outputOK = false
	state.outputFailed = false
	if original.Store != nil && *original.Store {
		boundary := responsesImageStoreBoundary(original.Input)
		state.boundary = boundary
		original.imageStoreBoundary = boundary
	} else {
		state.boundary = ""
		state.outputFailed = true
	}
	original.imageContinuation = state
	return original, true, nil
}

func responsesStoredResponseMissing(err error) bool {
	httpErr := AsHTTPError(err)
	if httpErr == nil || (httpErr.UpstreamStatus != http.StatusBadRequest && httpErr.Status != http.StatusBadRequest) {
		return false
	}
	text := strings.ToLower(httpErr.Message)
	if strings.Contains(text, "previous_response_not_found") {
		return true
	}
	if !strings.Contains(text, "previous_response") && !strings.Contains(text, "previous response") {
		return false
	}
	return strings.Contains(text, "not found") || strings.Contains(text, "not_found") || strings.Contains(text, "expired") || strings.Contains(text, "does not exist") || strings.Contains(text, "no longer available")
}

func cloneResponsesRequest(req ResponsesRequest) ResponsesRequest {
	cloned := req
	cloned.imageContinuation = nil
	cloned.imageStoreBoundary = ""
	if req.Store != nil {
		value := *req.Store
		cloned.Store = &value
	}
	if req.Reasoning != nil {
		value := *req.Reasoning
		cloned.Reasoning = &value
	}
	if req.raw != nil {
		cloned.raw = cloneRawJSON(req.raw, 0)
	}
	return cloned
}

func responsesEchoIdentity(item any) string {
	message, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	canonical := canonicalResponsesEcho(message)
	if canonical == nil {
		return ""
	}
	return responsesInputItemHash(canonical)
}

func canonicalResponsesEcho(message map[string]any) map[string]any {
	cloned := cloneJSONMap(message)
	if cloned == nil || !responsesImageStoreReplayEcho(cloned) {
		return nil
	}
	// id and status change between the stored response and a client replay.
	// The remaining payload is what distinguishes one answer from another.
	delete(cloned, "id")
	delete(cloned, "status")
	delete(cloned, "annotations")
	kind := strings.ToLower(strings.TrimSpace(responsesScalarString(cloned["type"])))
	role := strings.ToLower(strings.TrimSpace(responsesScalarString(cloned["role"])))
	if kind == "" && role == "assistant" {
		kind = "message"
	}
	if kind != "" {
		cloned["type"] = kind
	}
	switch kind {
	case "message":
		if role != "" && role != "assistant" {
			return nil
		}
		cloned["role"] = "assistant"
		cloned["content"] = canonicalEchoContent(cloned["content"])
	case "reasoning":
		if _, ok := cloned["content"]; ok {
			cloned["content"] = canonicalEchoContent(cloned["content"])
		}
		if _, ok := cloned["summary"]; ok {
			cloned["summary"] = canonicalEchoContent(cloned["summary"])
		}
	}
	if role != "" && kind != "message" {
		cloned["role"] = role
	}
	return cloned
}

func canonicalEchoContent(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return []any{map[string]any{"type": "output_text", "text": typed}}
	case []any:
		parts := make([]any, 0, len(typed))
		for _, part := range typed {
			payload, ok := part.(map[string]any)
			if !ok {
				continue
			}
			cloned := cloneJSONMap(payload)
			if cloned == nil {
				continue
			}
			delete(cloned, "id")
			delete(cloned, "status")
			delete(cloned, "annotations")
			kind := strings.ToLower(strings.TrimSpace(responsesScalarString(cloned["type"])))
			switch kind {
			case "", "output_text", "input_text", "text", "summary_text":
				cloned["type"] = "output_text"
			default:
				if kind != "" {
					cloned["type"] = kind
				}
			}
			parts = append(parts, cloned)
		}
		return parts
	default:
		return nil
	}
}

func cloneJSONMap(value map[string]any) map[string]any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	if json.Unmarshal(data, &cloned) != nil {
		return nil
	}
	return cloned
}

func responsesScalarString(value any) string {
	text, _ := value.(string)
	return text
}
