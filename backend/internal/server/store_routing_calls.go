package server

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) TestProvider(id string) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	healthy := provider.Status == StatusActive
	if err := s.db.Model(&Provider{}).Where("id = ?", id).Update("healthy", healthy).Error; err != nil {
		return Provider{}, err
	}
	provider.Healthy = healthy
	provider.APIKey = ""
	return provider, nil
}

func (s *GormStore) TestProviderResource(id string) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	now := time.Now().UTC()
	healthy := resource.Status == StatusActive
	updates := map[string]any{
		"healthy":         healthy,
		"last_checked_at": now,
		"updated_at":      now,
	}
	if healthy {
		updates["failure_count"] = 0
		updates["cooldown_until"] = nil
	}
	if err := s.db.Model(&ProviderResource{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return ProviderResource{}, err
	}
	resource.Healthy = healthy
	resource.LastCheckedAt = &now
	resource.FailureCount = 0
	resource.CooldownUntil = nil
	resource.UpdatedAt = now
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) ListModels() []Model {
	var items []Model
	_ = s.db.Order("name asc").Find(&items).Error
	return items
}

func (s *GormStore) UpdateModel(name string, patch Model) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var updated Model
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var model Model
		if err := tx.First(&model, "name = ?", name).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		originalID := model.ID
		originalName := model.Name
		renamed := patch.Name != "" && patch.Name != name
		if renamed {
			model.Name = patch.Name
			model.ID = patch.Name
		}
		if patch.Family != "" {
			model.Family = patch.Family
		}
		if patch.Modality != "" {
			model.Modality = patch.Modality
		}
		if patch.ContextWindow != 0 {
			model.ContextWindow = patch.ContextWindow
		}
		model.InputPriceUSDPer1M = patch.InputPriceUSDPer1M
		model.CacheReadPriceUSDPer1M = patch.CacheReadPriceUSDPer1M
		model.OutputPriceUSDPer1M = patch.OutputPriceUSDPer1M
		model.EmbeddingPriceUSDPer1M = patch.EmbeddingPriceUSDPer1M
		if model.Modality == "embedding" {
			model.CacheReadPriceUSDPer1M = 0
		}
		if patch.InputModalities != nil {
			model.InputModalities = patch.InputModalities
		}
		if patch.OutputModalities != nil {
			model.OutputModalities = patch.OutputModalities
		}
		if patch.Capabilities != nil {
			model.Capabilities = patch.Capabilities
		}
		if patch.SupportedParameters != nil {
			model.SupportedParameters = patch.SupportedParameters
		}
		if patch.Metadata != nil {
			model.Metadata = patch.Metadata
		}
		if patch.Status != "" {
			model.Status = patch.Status
		}
		if renamed {
			if err := tx.Delete(&Model{}, "id = ?", originalID).Error; err != nil {
				return err
			}
			if err := tx.Create(&model).Error; err != nil {
				return writeConflict(err, "model_conflict", "Model already exists")
			}
			if err := tx.Model(&ModelRoute{}).Where("model_name = ?", originalName).Update("model_name", model.Name).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&model).Error; err != nil {
			return err
		}
		updated = model
		return nil
	})
	return updated, err
}

func (s *GormStore) DeleteModel(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var model Model
		if err := tx.First(&model, "name = ?", name).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		if err := tx.Where("model_name = ?", name).Delete(&ModelRoute{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model).Error
	})
}

func (s *GormStore) ListRoutes() []ModelRoute {
	var items []ModelRoute
	_ = s.db.Order("model_name asc, priority asc").Find(&items).Error
	for index := range items {
		items[index].ProjectScope, items[index].ProjectIDs = normalizeRouteProjectScope(items[index].ProjectScope, items[index].ProjectIDs)
	}
	return items
}

func (s *GormStore) UpdateRoute(id string, patch ModelRoute) (ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var route ModelRoute
	if err := s.db.First(&route, "id = ?", id).Error; err != nil {
		return ModelRoute{}, notFound(err, "route_not_found", "Route not found")
	}
	if patch.ModelName != "" {
		route.ModelName = patch.ModelName
	}
	if patch.ProviderID != "" {
		route.ProviderID = patch.ProviderID
	}
	route.ProviderResourceID = patch.ProviderResourceID
	route.ResourceGroup = patch.ResourceGroup
	route.StickySession = patch.StickySession
	if patch.ProviderModel != "" {
		route.ProviderModel = patch.ProviderModel
	}
	if patch.Priority != 0 {
		route.Priority = patch.Priority
	}
	if patch.Weight != 0 {
		route.Weight = patch.Weight
	}
	if patch.QualityScore != 0 {
		route.QualityScore = patch.QualityScore
	}
	if patch.CostScore != 0 {
		route.CostScore = patch.CostScore
	}
	if patch.Status != "" {
		route.Status = patch.Status
	}
	if patch.Strategy != "" {
		route.Strategy = patch.Strategy
	}
	if patch.ProjectScope != "" || patch.ProjectIDs != nil {
		route.ProjectScope, route.ProjectIDs = normalizeRouteProjectScope(patch.ProjectScope, patch.ProjectIDs)
	}
	return route, s.db.Save(&route).Error
}

func (s *GormStore) UpdateModelRoutePolicy(modelName string, policy ModelRoutePolicy) ([]ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	modelName = strings.TrimSpace(modelName)
	var updated []ModelRoute
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var routes []ModelRoute
		if err := tx.Where("model_name = ?", modelName).Order("priority asc, created_at asc, id asc").Find(&routes).Error; err != nil {
			return err
		}
		if len(routes) == 0 {
			return NewHTTPError(http.StatusNotFound, "model_routes_not_found", "Model has no routing rules")
		}
		if len(policy.Routes) != len(routes) {
			return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy must include every route for the model")
		}

		routeByID := make(map[string]*ModelRoute, len(routes))
		for index := range routes {
			routeByID[routes[index].ID] = &routes[index]
		}
		seen := make(map[string]bool, len(policy.Routes))
		for _, patch := range policy.Routes {
			if seen[patch.RouteID] || routeByID[patch.RouteID] == nil {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy contains an unknown or duplicate route")
			}
			if patch.Weight <= 0 || patch.QualityScore < 1 || patch.QualityScore > 100 || patch.CostScore < 1 || patch.CostScore > 100 {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_parameters", "Weight must be positive and route scores must be between 1 and 100")
			}
			seen[patch.RouteID] = true
		}

		updated = make([]ModelRoute, 0, len(routes))
		for index, patch := range policy.Routes {
			route := routeByID[patch.RouteID]
			route.Strategy = policy.Strategy
			route.Weight = patch.Weight
			route.QualityScore = patch.QualityScore
			route.CostScore = patch.CostScore
			if policy.Strategy == RouteStrategyPriorityOnly {
				route.Priority = index + 1
			} else {
				route.Priority = 1
			}
			if err := tx.Save(route).Error; err != nil {
				return err
			}
			updated = append(updated, *route)
		}
		return nil
	})
	return updated, err
}

func (s *GormStore) DeleteRoute(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var route ModelRoute
	if err := s.db.First(&route, "id = ?", id).Error; err != nil {
		return notFound(err, "route_not_found", "Route not found")
	}
	return s.db.Delete(&route).Error
}

func (s *GormStore) SelectRoute(modelName string) (RouteSelection, error) {
	routes, err := s.SelectRouteCandidates(modelName)
	if err != nil {
		return RouteSelection{}, err
	}
	if len(routes) == 0 {
		return RouteSelection{}, ErrProviderMissing
	}
	return routes[0], nil
}

func (s *GormStore) SelectRouteCandidates(modelName string) ([]RouteSelection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var routes []ModelRoute
	if err := s.db.Where("model_name = ? AND status = ?", modelName, StatusActive).
		Order("priority asc, weight desc, created_at asc").
		Find(&routes).Error; err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	selections := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		var provider Provider
		if err := s.db.First(&provider, "id = ?", route.ProviderID).Error; err != nil {
			continue
		}
		if provider.Status != StatusActive || !provider.Healthy {
			continue
		}
		if route.ProviderResourceID != "" {
			var resource ProviderResource
			if err := s.db.First(&resource, "id = ? AND provider_id = ?", route.ProviderResourceID, provider.ID).Error; err != nil {
				continue
			}
			if resource.Status != StatusActive || !halfOpenEligible(resource, now) {
				continue
			}
			selections = append(selections, s.routeSelection(provider, &resource, route))
			continue
		}

		var resources []ProviderResource
		// Unhealthy resources whose cooldown has lapsed are admitted as half-open
		// candidates. Admission still gates them to a single trial (see
		// CheckProviderResourceCapacity); this query only makes them reachable, which
		// is what lets a parked resource ever be retried.
		query := s.db.Where("provider_id = ? AND status = ? AND (healthy = ? OR cooldown_until <= ?)",
			provider.ID, StatusActive, true, now)
		if strings.TrimSpace(route.ResourceGroup) != "" {
			query = query.Where("\"group\" = ?", strings.TrimSpace(route.ResourceGroup))
		}
		if err := query.Order("priority asc, weight desc, created_at asc").
			Find(&resources).Error; err != nil {
			return nil, err
		}
		if len(resources) == 0 {
			selections = append(selections, s.routeSelection(provider, nil, route))
			continue
		}
		for _, resource := range resources {
			resourceRoute := route
			resourceRoute.ProviderResourceID = resource.ID
			if resource.Weight > 0 {
				resourceRoute.Weight = resource.Weight
			}
			selections = append(selections, s.routeSelection(provider, &resource, resourceRoute))
		}
	}
	s.attachRouteRuntimeStats(selections, now)
	if len(selections) == 0 {
		return nil, ErrProviderMissing
	}
	return selections, nil
}

type routeRuntimeStatsRow struct {
	RouteID            string
	ProviderResourceID string
	Samples            int64
	Successes          int64
	LatencyMS          float64
}

func (s *GormStore) attachRouteRuntimeStats(selections []RouteSelection, now time.Time) {
	routeIDs := make([]string, 0, len(selections))
	seen := map[string]bool{}
	for _, selection := range selections {
		if routeStrategy(selection.Route) != RouteStrategyAdaptive || seen[selection.Route.ID] {
			continue
		}
		seen[selection.Route.ID] = true
		routeIDs = append(routeIDs, selection.Route.ID)
	}
	if len(routeIDs) == 0 {
		return
	}

	var rows []routeRuntimeStatsRow
	err := s.db.Model(&RouteAttemptLog{}).
		Select(`route_id, provider_resource_id, COUNT(*) AS samples,
			SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END) AS successes,
			COALESCE(AVG(CASE WHEN status_code >= 200 AND status_code < 400 THEN latency_ms ELSE NULL END), 0) AS latency_ms`).
		Where("invoked = ? AND created_at >= ? AND route_id IN ?", true, now.Add(-adaptiveRoutingWindow), routeIDs).
		Group("route_id, provider_resource_id").
		Scan(&rows).Error
	if err != nil {
		log.Printf("[tokenhub] failed to load adaptive routing observations: %v", err)
		return
	}
	stats := make(map[string]RouteRuntimeStats, len(rows))
	for _, row := range rows {
		successRate := float64(0)
		if row.Samples > 0 {
			successRate = float64(row.Successes) / float64(row.Samples)
		}
		stats[routeRuntimeStatsKey(row.RouteID, row.ProviderResourceID)] = RouteRuntimeStats{
			Samples:     row.Samples,
			SuccessRate: successRate,
			LatencyMS:   int64(math.Round(row.LatencyMS)),
		}
	}
	for index := range selections {
		selection := &selections[index]
		selection.Runtime = stats[routeRuntimeStatsKey(selection.Route.ID, routeResourceID(*selection))]
	}
}

func routeRuntimeStatsKey(routeID string, resourceID string) string {
	return routeID + "\x00" + resourceID
}

func (s *GormStore) routeSelection(provider Provider, resource *ProviderResource, route ModelRoute) RouteSelection {
	provider.APIKey = s.decryptSecret(provider.APIKey)
	if resource == nil {
		return RouteSelection{
			Provider:      provider,
			ProviderModel: route.ProviderModel,
			Route:         route,
		}
	}
	effective := provider
	if resource.BaseURL != "" {
		effective.BaseURL = resource.BaseURL
	}
	if resource.APIKey != "" {
		effective.APIKey = s.decryptSecret(resource.APIKey)
	}
	if len(resource.Headers) > 0 {
		headers := map[string]string{}
		for key, value := range provider.Headers {
			headers[key] = value
		}
		for key, value := range resource.Headers {
			headers[key] = value
		}
		effective.Headers = headers
	}
	if len(resource.Options) > 0 {
		options := map[string]string{}
		for key, value := range provider.Options {
			options[key] = value
		}
		for key, value := range resource.Options {
			options[key] = value
		}
		effective.Options = options
	}
	publicResource := *resource
	redactProviderResourceSecrets(&publicResource)
	return RouteSelection{
		Provider:      effective,
		Resource:      &publicResource,
		ProviderModel: route.ProviderModel,
		Route:         route,
	}
}

func (s *GormStore) MarkRouteUsed(routeID string) {
	if routeID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	_ = s.db.Model(&ModelRoute{}).Where("id = ?", routeID).Update("last_used_at", now).Error
}

func (s *GormStore) MarkProviderResourceUsed(resourceID string) {
	if resourceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	_ = s.db.Model(&ProviderResource{}).
		Where("id = ?", resourceID).
		Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
}

func (s *GormStore) StartCall(ctx context.Context, project Project, key APIKey, modelName string) (CallContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var call CallContext
	requestID := NewID("req")
	leaseAcquired := false
	var leaseConfirmedFor time.Duration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "api_key", key.ID); err != nil {
			return err
		}
		var privateKey APIKey
		if err := tx.First(&privateKey, "id = ?", key.ID).Error; err != nil {
			return ErrInvalidAPIKey
		}
		hydrateAPIKey(&privateKey)
		var model Model
		if err := tx.First(&model, "name = ? AND status = ?", modelName, StatusActive).Error; err != nil {
			return ErrModelNotAllowed
		}
		if len(privateKey.AllowedModels) > 0 && !privateKey.AllowedModels[modelName] {
			return ErrModelNotAllowed
		}
		effectiveLimits := mergeQuotaLimits(privateKey.Limits, quotaPolicyLimits(tx, project, privateKey))
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		// Taken next to the database reading so the two describe the same instant,
		// and the local reference does not also absorb the admission work below.
		measuredAt := time.Now()
		dayCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "day", dayBucket(now))
		if err != nil {
			return err
		}
		monthCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "month", monthBucket(now))
		if err != nil {
			return err
		}
		if effectiveLimits.MaxConcurrency > 0 {
			confirmedFor, err := s.acquireInFlightLease(tx, "api_key", privateKey.ID, effectiveLimits.MaxConcurrency, requestID)
			if err != nil {
				return err
			}
			leaseConfirmedFor = confirmedFor
			leaseAcquired = true
		}
		if exceedsRequestQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
			exceedsTokenQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
			exceedsCostQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) {
			return ErrQuotaExceeded
		}
		if err := s.checkRuntimeBudget(tx, project); err != nil {
			return err
		}
		dayCounter.Requests++
		monthCounter.Requests++
		if err := tx.Save(&dayCounter).Error; err != nil {
			return err
		}
		if err := tx.Save(&monthCounter).Error; err != nil {
			return err
		}
		call = CallContext{
			RequestID:      requestID,
			Project:        project,
			Key:            publicKey(privateKey),
			Model:          model,
			StartedAt:      now,
			measuredAt:     measuredAt,
			requestContext: ctx,
		}
		return nil
	})
	if err != nil {
		return CallContext{}, err
	}
	if leaseAcquired {
		call.requestContext = s.startInFlightLeaseHeartbeat(ctx, requestID, leaseConfirmedFor)
	}
	return call, nil
}

func (s *GormStore) FinishCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string) {
	// Measured here rather than inside the deferred observation, so latency reflects
	// what the client waited for and excludes the persistence and lock time that
	// follows. FinishCall is invoked after the last streamed byte is written. The
	// same value is threaded into the transaction below so the persisted latency
	// and the reported metric describe the same interval.
	elapsed := call.elapsed()
	_ = s.stopInFlightLeaseHeartbeat(call.RequestID)
	// priceUsage is pure, so it runs outside the lock and its result is final here.
	usage = priceUsage(call.Model, usage)
	usage.ProviderCostUSD = s.providerCostUSD(route, usage)
	// Registered before the lock is taken, so LIFO ordering runs it *after* the
	// unlock: reporting metrics must not extend how long this request holds the
	// store-wide mutex. Deferring also means the request is still counted when the
	// transaction below fails or panics — losing persistence must not also lose the
	// observation that the request happened.
	defer s.observeGatewayCall(call, route, usage, statusCode, errorCode, elapsed)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.finishCallTransaction(tx, call, route, usage, statusCode, errorCode, clientIP, userAgent, now, elapsed)
	})
	if err != nil {
		log.Printf("[tokenhub] failed to finish call request=%s: %v", call.RequestID, err)
		if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error; releaseErr != nil {
			log.Printf("[tokenhub] failed to release API key concurrency lease request=%s: %v", call.RequestID, releaseErr)
		}
	}
}

func (s *GormStore) finishCallTransaction(tx *gorm.DB, call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string, now time.Time, elapsed time.Duration) error {
	if call.Key.ID != "" {
		if err := s.lockScopeForUpdate(tx, "api_key", call.Key.ID); err != nil {
			return err
		}
	}
	var key APIKey
	if err := tx.First(&key, "id = ?", call.Key.ID).Error; err == nil {
		dayCounter, err := s.quotaBucketForUpdate(tx, key.ID, "day", dayBucket(now))
		if err != nil {
			return err
		}
		monthCounter, err := s.quotaBucketForUpdate(tx, key.ID, "month", monthBucket(now))
		if err != nil {
			return err
		}
		addUsage(&dayCounter.QuotaCounter, usage)
		addUsage(&monthCounter.QuotaCounter, usage)
		if err := tx.Save(&dayCounter).Error; err != nil {
			return err
		}
		if err := tx.Save(&monthCounter).Error; err != nil {
			return err
		}
		if err := raiseQuotaAlerts(tx, key, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter); err != nil {
			return err
		}
	}
	if usage.TotalTokens > 0 || usage.CostUSD > 0 {
		if err := tx.Create(newUsageRecord(call, route, usage, now)).Error; err != nil {
			return err
		}
	}
	if err := tx.Create(&RequestLog{
		ID:                 NewID("log"),
		RequestID:          call.RequestID,
		ProjectID:          call.Project.ID,
		APIKeyID:           call.Key.ID,
		ModelName:          call.Model.Name,
		ProviderID:         route.Provider.ID,
		ProviderResourceID: routeResourceID(route),
		ProviderModel:      route.ProviderModel,
		UpstreamRequestID:  usage.UpstreamRequestID,
		ServedModel:        usage.ServedModel,
		ModelETag:          usage.ModelETag,
		Transport:          usage.Transport,
		StatusCode:         statusCode,
		ErrorCode:          errorCode,
		LatencyMS:          elapsed.Milliseconds(),
		ClientIP:           clientIP,
		UserAgent:          userAgent,
		CreatedAt:          now,
	}).Error; err != nil {
		return err
	}
	if route.Provider.ID != "" {
		if err := tx.Create(&ProviderObservation{
			ID:          NewID("pob"),
			ProviderID:  route.Provider.ID,
			ResourceID:  routeResourceID(route),
			AdapterType: route.Provider.Type,
			Source:      "gateway_request",
			Operation:   "inference",
			Success:     providerObservationSuccess(statusCode, errorCode),
			LatencyMS:   elapsed.Milliseconds(),
			ErrorCode:   errorCode,
			ObservedAt:  now,
		}).Error; err != nil {
			return err
		}
	}
	if resourceID := routeResourceID(route); resourceID != "" && (len(usage.ResponseHeaders) > 0 || usage.UpstreamRequestID != "" || usage.ServedModel != "") {
		observation := ProviderResourceObservation{
			ResourceID:        resourceID,
			AdapterType:       route.Provider.Type,
			RateLimitHeaders:  codexRateLimitHeaders(usage.ResponseHeaders),
			UpstreamRequestID: usage.UpstreamRequestID,
			ServedModel:       usage.ServedModel,
			ModelETag:         usage.ModelETag,
			Transport:         usage.Transport,
			UpdatedAt:         now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "resource_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"adapter_type",
				"rate_limit_headers",
				"upstream_request_id",
				"served_model",
				"model_e_tag",
				"transport",
				"updated_at",
			}),
		}).Create(&observation).Error; err != nil {
			return err
		}
	}
	return tx.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error
}

func (s *GormStore) RecordPlaygroundRequest(call CallContext, route RouteSelection, statusCode int, errorCode string, clientIP string, userAgent string) {
	// Measured before the lock, matching FinishCall: the recorded latency is what
	// the caller waited for and excludes contention on the store-wide mutex.
	elapsed := call.elapsed()
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.db.Create(&RequestLog{
		ID:                 NewID("log"),
		RequestID:          call.RequestID,
		ProjectID:          call.Project.ID,
		APIKeyID:           call.Key.ID,
		ModelName:          call.Model.Name,
		ProviderID:         route.Provider.ID,
		ProviderResourceID: routeResourceID(route),
		ProviderModel:      route.ProviderModel,
		StatusCode:         statusCode,
		ErrorCode:          errorCode,
		LatencyMS:          elapsed.Milliseconds(),
		ClientIP:           clientIP,
		UserAgent:          userAgent,
		CreatedAt:          time.Now().UTC(),
	}).Error
}

func (s *GormStore) RecordRouteAttempts(requestID string, attempts []RouteAttempt) {
	if requestID == "" || len(attempts) == 0 {
		return
	}
	now := time.Now().UTC()
	items := make([]RouteAttemptLog, 0, len(attempts))
	for index, attempt := range attempts {
		items = append(items, newRouteAttemptLog(requestID, index, attempt, now))
	}
	_ = s.db.Create(&items).Error
}

func (s *GormStore) RecordRejectedRequest(project Project, key APIKey, modelName string, stream bool, statusCode int, errorCode string, clientIP string, userAgent string) string {
	requestID := NewID("req")
	_ = s.db.Create(&RequestLog{
		ID:         NewID("log"),
		RequestID:  requestID,
		ProjectID:  project.ID,
		APIKeyID:   key.ID,
		ModelName:  modelName,
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		ClientIP:   clientIP,
		UserAgent:  userAgent,
		CreatedAt:  time.Now().UTC(),
	}).Error
	// A rejected request never reached a provider, so it contributes to the request
	// counter only: no duration, no tokens, no cost. Emitting zeroes for those would
	// create series that dilute every rate() over them.
	//
	// The model name here is unvalidated client input — this path is reached precisely
	// when a request is refused, including for naming a model that does not exist.
	// Using it verbatim as a label would let anyone mint unbounded series by looping
	// over random model names, so it is collapsed unless the catalog knows it.
	s.metrics.ObserveGatewayCall(GatewayCallSample{
		Model:      s.knownModelLabel(modelName),
		ProjectID:  project.ID,
		Stream:     stream,
		StatusCode: statusCode,
		ErrorCode:  errorCode,
	})
	return requestID
}

// observeGatewayCall reports a completed gateway request. Kept separate from FinishCall
// so the deferred call reads as one statement and the label mapping lives in one place.
func (s *GormStore) observeGatewayCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, elapsed time.Duration) {
	if s.metrics == nil {
		return
	}
	sample := GatewayCallSample{
		Model:        call.Model.Name,
		ProviderType: route.Provider.Type,
		ProviderID:   route.Provider.ID,
		ResourceID:   routeResourceID(route),
		ProjectID:    call.Project.ID,
		StatusCode:   statusCode,
		ErrorCode:    errorCode,
		Stream:       call.Stream,
		Usage:        usage,
		Duration:     elapsed,
	}
	s.metrics.ObserveGatewayCall(sample)
}

// knownModelLabel keeps a model name as a label only when the catalog knows it,
// bounding the label to configured models instead of arbitrary client input.
func (s *GormStore) knownModelLabel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || s.metrics == nil {
		return ""
	}
	var count int64
	if err := s.db.Model(&Model{}).Where("name = ?", modelName).Limit(1).Count(&count).Error; err != nil || count == 0 {
		return "unknown"
	}
	return modelName
}

func (s *GormStore) RecordRequestPayload(requestID string, requestBody string, requestTruncated bool, responseBody string, responseTruncated bool) {
	if requestID == "" {
		return
	}
	_ = s.db.Create(&RequestPayloadLog{
		ID:                NewID("pay"),
		RequestID:         requestID,
		RequestBody:       requestBody,
		ResponseBody:      responseBody,
		RequestTruncated:  requestTruncated,
		ResponseTruncated: responseTruncated,
		CreatedAt:         time.Now().UTC(),
	}).Error
}

func (s *GormStore) CreateImageJob(job ImageJob, prompt string) (ImageJob, error) {
	if strings.TrimSpace(job.ID) == "" {
		job.ID = NewID("imgjob")
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = "queued"
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.PromptCiphertext = s.encryptSecret(prompt)
	job.Prompt = prompt
	if err := s.db.Create(&job).Error; err != nil {
		return ImageJob{}, err
	}
	return job, nil
}

func (s *GormStore) ClaimImageJob(id string) (ImageJob, bool, error) {
	now := time.Now().UTC()
	result := s.db.Model(&ImageJob{}).
		Where("id = ? AND status = ?", id, "queued").
		Updates(map[string]any{"status": "running", "started_at": now})
	if result.Error != nil {
		return ImageJob{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return ImageJob{}, false, nil
	}
	var job ImageJob
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return ImageJob{}, false, err
	}
	job.Prompt = s.decryptSecret(job.PromptCiphertext)
	job.RevisedPrompt = s.decryptSecret(job.RevisedPromptCiphertext)
	return job, true, nil
}

func (s *GormStore) GetImageJob(id string) (ImageJob, bool) {
	var job ImageJob
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return ImageJob{}, false
	}
	job.Prompt = s.decryptSecret(job.PromptCiphertext)
	job.RevisedPrompt = s.decryptSecret(job.RevisedPromptCiphertext)
	return job, true
}

func (s *GormStore) ListImageJobs(limit int) []ImageJob {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var jobs []ImageJob
	if err := s.db.Order("created_at desc").Limit(limit).Find(&jobs).Error; err != nil {
		return nil
	}
	for index := range jobs {
		jobs[index].Prompt = s.decryptSecret(jobs[index].PromptCiphertext)
		jobs[index].RevisedPrompt = s.decryptSecret(jobs[index].RevisedPromptCiphertext)
	}
	return jobs
}

func (s *GormStore) FailUnfinishedImageJobs(code string, message string) ([]ImageJob, error) {
	now := time.Now().UTC()
	var jobs []ImageJob
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("status IN ?", []string{"queued", "running"}).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		if err := tx.Model(&ImageJob{}).
			Where("status IN ?", []string{"queued", "running"}).
			Updates(map[string]any{
				"status":        "failed",
				"error_code":    code,
				"error_message": message,
				"completed_at":  now,
			}).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if strings.TrimSpace(job.RequestID) == "" {
				continue
			}
			if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&RequestLog{}).Where("request_id = ?", job.RequestID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&RequestLog{
					ID:         NewID("log"),
					RequestID:  job.RequestID,
					ProjectID:  job.ProjectID,
					APIKeyID:   job.APIKeyID,
					ModelName:  job.Model,
					StatusCode: http.StatusServiceUnavailable,
					ErrorCode:  code,
					LatencyMS:  latencyMillis(now.Sub(job.CreatedAt)),
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range jobs {
		jobs[index].Status = "failed"
		jobs[index].ErrorCode = code
		jobs[index].ErrorMessage = message
		jobs[index].CompletedAt = &now
		jobs[index].Prompt = s.decryptSecret(jobs[index].PromptCiphertext)
		jobs[index].RevisedPrompt = s.decryptSecret(jobs[index].RevisedPromptCiphertext)
	}
	return jobs, nil
}

func (s *GormStore) UpdateImageJob(job ImageJob, revisedPrompt string) error {
	if strings.TrimSpace(revisedPrompt) != "" {
		job.RevisedPromptCiphertext = s.encryptSecret(revisedPrompt)
		job.RevisedPrompt = revisedPrompt
	}
	return s.db.Save(&job).Error
}

func (s *GormStore) CompleteImageJob(call CallContext, job ImageJob, revisedPrompt string, asset ImageAsset, route RouteSelection, usage Usage, clientIP string, userAgent string) error {
	elapsed := call.elapsed()
	_ = s.stopInFlightLeaseHeartbeat(call.RequestID)
	usage = priceUsage(call.Model, usage)
	usage.ProviderCostUSD = s.providerCostUSD(route, usage)

	now := time.Now().UTC()
	if job.CompletedAt == nil {
		job.CompletedAt = &now
	}
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = NewID("asset")
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	revisedPromptCiphertext := job.RevisedPromptCiphertext
	if strings.TrimSpace(revisedPrompt) != "" {
		revisedPromptCiphertext = s.encryptSecret(revisedPrompt)
	}

	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := s.finishCallTransaction(tx, call, route, usage, http.StatusOK, "", clientIP, userAgent, now, elapsed); err != nil {
				return err
			}
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			result := tx.Model(&ImageJob{}).
				Where("id = ? AND status = ?", job.ID, "running").
				Updates(map[string]any{
					"status":                    "completed",
					"provider_id":               job.ProviderID,
					"provider_resource_id":      job.ProviderResourceID,
					"provider_model":            job.ProviderModel,
					"upstream_request_id":       job.UpstreamRequestID,
					"input_tokens":              job.InputTokens,
					"cached_input_tokens":       job.CachedInputTokens,
					"output_tokens":             job.OutputTokens,
					"total_tokens":              job.TotalTokens,
					"revised_prompt_ciphertext": revisedPromptCiphertext,
					"error_code":                "",
					"error_message":             "",
					"completed_at":              job.CompletedAt,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("image job %s is not running", job.ID)
			}
			if route.Route.ID != "" {
				if err := tx.Model(&ModelRoute{}).Where("id = ?", route.Route.ID).Update("last_used_at", now).Error; err != nil {
					return err
				}
			}
			if resourceID := routeResourceID(route); resourceID != "" {
				if err := tx.Model(&ProviderResource{}).Where("id = ?", resourceID).
					Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}()
	if err != nil {
		if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error; releaseErr != nil {
			log.Printf("[tokenhub] failed to release API key concurrency lease request=%s: %v", call.RequestID, releaseErr)
		}
	} else {
		s.observeGatewayCall(call, route, usage, http.StatusOK, "", elapsed)
	}
	return err
}

func (s *GormStore) CreateImageAsset(asset ImageAsset) (ImageAsset, error) {
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = NewID("asset")
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now().UTC()
	}
	if err := s.db.Create(&asset).Error; err != nil {
		return ImageAsset{}, err
	}
	return asset, nil
}

func (s *GormStore) ListImageAssets(jobID string) []ImageAsset {
	var assets []ImageAsset
	_ = s.db.Where("job_id = ?", jobID).Order("created_at asc").Find(&assets).Error
	return assets
}

func (s *GormStore) GetImageAsset(id string) (ImageAsset, bool) {
	var asset ImageAsset
	if err := s.db.First(&asset, "id = ?", id).Error; err != nil {
		return ImageAsset{}, false
	}
	return asset, true
}

// newRouteAttemptLog is the persisted form of one routing attempt.
//
// It lives next to nothing else on purpose: the mapping is long, it belongs to the
// RouteAttempt type rather than to the store, and keeping it here means adding a
// field to an attempt touches one function instead of the store's largest file.
func newRouteAttemptLog(requestID string, index int, attempt RouteAttempt, now time.Time) RouteAttemptLog {
	return RouteAttemptLog{
		ID:                       NewID("rat"),
		RequestID:                requestID,
		AttemptIndex:             index + 1,
		RouteID:                  attempt.Selection.Route.ID,
		ProviderID:               attempt.Selection.Provider.ID,
		ProviderResourceID:       routeResourceID(attempt.Selection),
		ProviderModel:            attempt.Selection.ProviderModel,
		StatusCode:               attempt.Status,
		ErrorCode:                attempt.ErrorCode,
		ErrorMessage:             attempt.Error,
		Invoked:                  attempt.Invoked,
		LatencyMS:                attempt.LatencyMS,
		ServedModel:              attempt.Usage.ServedModel,
		UpstreamRequestID:        attempt.Usage.UpstreamRequestID,
		Transport:                attempt.Usage.Transport,
		InputTokens:              attempt.Usage.PromptTokens,
		CachedInputTokens:        attempt.Usage.CachedInputTokens,
		CacheWriteTokens:         attempt.Usage.CacheWriteInputTokens,
		InputAudioTokens:         attempt.Usage.InputAudioTokens,
		OutputTokens:             attempt.Usage.CompletionTokens,
		ReasoningTokens:          attempt.Usage.ReasoningOutputTokens,
		OutputAudioTokens:        attempt.Usage.OutputAudioTokens,
		AcceptedPredictionTokens: attempt.Usage.AcceptedPredictionTokens,
		RejectedPredictionTokens: attempt.Usage.RejectedPredictionTokens,
		TotalTokens:              attempt.Usage.TotalTokens,
		CostUSD:                  attempt.Usage.CostUSD,
		StartedAt:                attempt.StartedAt,
		EndedAt:                  attempt.EndedAt,
		CreatedAt:                now,
	}
}
