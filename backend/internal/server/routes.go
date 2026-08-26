package server

import "net/http"

// The gateway's URL surface, kept apart from server construction so adding an
// endpoint and changing how the server is built stay separate edits.

func (s *Server) routes() {
	s.registerSingleMethodRoute(http.MethodGet, "/livez", s.handleLive, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/readyz", s.handleHealth, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/healthz", s.handleHealth, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/metrics", s.handleMetrics, s.metricsMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/docs", s.handleDocs, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/docs/swagger-ui.css", s.handleDocsSwaggerUICSS, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/docs/swagger-ui-bundle.js", s.handleDocsSwaggerUIBundle, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/docs/swagger-ui-bundle.js.LICENSE.txt", s.handleDocsSwaggerUIBundleLicense, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/docs/swagger-ui-dist.LICENSE", s.handleDocsSwaggerUIDistLicense, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/openapi.json", s.handleOpenAPIJSON, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/openapi.yaml", s.handleOpenAPIYAML, jsonMethodNotAllowed(http.MethodGet))
	s.registerModelRoutes()
	// The in-flight gauge covers exactly the endpoints that route to an upstream, so
	// it stays comparable with requests_total. Catalog lookups and count_tokens are
	// local and never produce a request count, and admin traffic and scrapes are not
	// gateway load at all.
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/chat/completions", s.gatewayInFlight(s.handleChatCompletions), jsonMethodNotAllowed(http.MethodPost))
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/messages", s.gatewayInFlight(s.handleAnthropicMessages), anthropicMethodNotAllowed(http.MethodPost))
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/messages/count_tokens", s.handleAnthropicCountTokens, anthropicMethodNotAllowed(http.MethodPost))
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/responses", s.gatewayInFlight(s.handleResponses), jsonMethodNotAllowed(http.MethodPost))
	s.registerPublicDynamicGETRoute("/v1/responses/{response_id}", s.handleResponseJobGet, responseJobGetHeadFallback)
	s.registerDynamicGETRoute("/v1/responses/{response_id}/{$}", s.handleResponseJobGet, responseJobGetHeadFallback)
	s.registerPublicDynamicMethodRoute(http.MethodPost, "/v1/responses/{response_id}/cancel", s.handleResponseJobCancel)
	s.registerDynamicMethodRoute(http.MethodPost, "/v1/responses/{response_id}/cancel/{$}", s.handleResponseJobCancel)
	s.registerPublicDirectMethodRoute(http.MethodPost, "/v1/responses/compact", s.gatewayInFlight(s.handleResponsesCompact))
	s.mux.HandleFunc(http.MethodGet+" /v1/responses/compact", jsonMethodNotAllowed(http.MethodPost))
	s.mux.HandleFunc("/v1/responses/", s.handleResponseJob)
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/embeddings", s.gatewayInFlight(s.handleEmbeddings), jsonMethodNotAllowed(http.MethodPost))
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/images/generations", s.handleImageGenerations, jsonMethodNotAllowed(http.MethodPost))
	s.registerPublicSingleMethodRoute(http.MethodPost, "/v1/images/edits", s.handleImageEdits, jsonMethodNotAllowed(http.MethodPost))
	s.registerPublicDynamicGETRoute("/v1/image-jobs/{job_id}", s.handleImageJobGet, jsonMethodNotAllowed(http.MethodGet))
	s.registerDynamicGETRoute("/v1/image-jobs/{job_id}/{$}", s.handleImageJobGet, jsonMethodNotAllowed(http.MethodGet))
	s.mux.HandleFunc("/v1/image-jobs/", s.handleImageJob)
	s.registerPublicDynamicGETRoute("/v1/image-assets/{asset_id}/content", s.handleImageAssetGet, jsonMethodNotAllowed(http.MethodGet))
	s.registerDynamicGETRoute("/v1/image-assets/{asset_id}/content/{$}", s.handleImageAssetGet, jsonMethodNotAllowed(http.MethodGet))
	s.mux.HandleFunc("/v1/image-assets/", s.handleImageAsset)
	s.registerSingleMethodRoute(http.MethodGet, "/api/v1/analytics/token-costs", s.handleTokenCostAnalytics, jsonMethodNotAllowed(http.MethodGet))

	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/auth/login", s.handleAdminLogin, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/auth/logout", s.handleAdminLogout, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/auth/me", s.handleAdminMe, s.adminAuthenticationMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/auth/reset-password", s.handleAdminResetPassword, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/auth/identity-providers", s.handleAdminAuthIdentityProviders, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/auth/oauth/start", s.handleAdminOAuthStart, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/auth/oauth/callback", s.handleAdminOAuthCallback, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/auth/oauth/exchange", s.handleAdminOAuthExchange, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/overview", s.handleAdminOverview, s.adminMethodNotAllowed("overview", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/playground/chat", s.handleAdminPlaygroundChat, s.adminMethodNotAllowed("playground", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/playground/chat/stream", s.handleAdminPlaygroundChatStream, s.adminMethodNotAllowed("playground", http.MethodPost))
	s.registerMethodRoutes("/api/admin/projects", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("project", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminProjectsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminProjectsPost},
	)
	s.registerMethodRoutes("/api/admin/projects/{project_id}", func(allowedMethods string) http.HandlerFunc {
		return s.adminProjectMethodNotAllowed("project", allowedMethods)
	},
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminProjectPatch},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminProjectDelete},
	)
	s.registerMethodRoutes("/api/admin/projects/{project_id}/keys", func(allowedMethods string) http.HandlerFunc {
		return s.adminProjectMethodNotAllowed("api_key", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminProjectKeysGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminProjectKeysPost},
	)
	s.registerMethodRoutes("/api/admin/projects/{project_id}/teams", s.adminProjectTeamsMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminProjectTeamsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminProjectTeamsPost},
	)
	s.registerMethodRoutes("/api/admin/projects/{project_id}/teams/{team_id}", s.adminProjectTeamMethodNotAllowed,
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminProjectTeamPatch},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminProjectTeamDelete},
	)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/projects/{project_id}/quota-increase", s.handleAdminProjectQuotaIncreasePost, s.adminProjectMethodNotAllowed("approval", http.MethodPost))
	s.mux.HandleFunc("/api/admin/projects/", s.handleAdminProjectNested)
	s.registerAdminGuardrailPolicyRoutes()
	s.registerMethodRoutes("/api/admin/users", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("identity", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminUsersGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminUsersPost},
	)
	userImportMethodNotAllowed := s.adminMethodNotAllowed("identity", http.MethodPost)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/users/import", s.handleAdminUsersImportPost)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/users/import", userImportMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/users/import", userImportMethodNotAllowed)
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/users/{user_id}", s.handleAdminUserPatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/users/{user_id}", s.handleAdminUserDelete)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/users/{user_id}/reset-password-email", s.handleAdminUserResetPasswordEmailPost, s.adminUserMethodNotAllowed(http.MethodPost))
	s.mux.HandleFunc("/api/admin/users/", s.handleAdminUserItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-catalog", s.handleAdminProviderCatalogGet, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-egress/test", s.handleAdminProviderEgressTestPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	// Catalog discovery has a few IDs with a second, credential-backed POST
	// operation. Only their method patterns are explicit: the subtree handler
	// remains the ID-aware fallback for other methods and legacy path shapes.
	catalogMultiMethodNotAllowed := s.adminMethodNotAllowed("provider", http.MethodGet+", "+http.MethodPost)
	for _, catalogID := range []string{codexProviderCatalogID, "custom", ProviderKronk} {
		pattern := "/api/admin/provider-catalog/" + catalogID
		s.mux.HandleFunc(http.MethodGet+" "+pattern, s.handleAdminProviderCatalogItem)
		s.mux.HandleFunc(http.MethodPost+" "+pattern, s.handleAdminProviderCatalogItem)
		s.mux.HandleFunc(http.MethodHead+" "+pattern, catalogMultiMethodNotAllowed)
	}
	s.registerDynamicGETRoute("/api/admin/provider-catalog/{catalog_id}", s.handleAdminProviderCatalogItem, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.mux.HandleFunc("/api/admin/provider-catalog/", s.handleAdminProviderCatalogItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-adapters", s.handleAdminProviderAdapters, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/plugins", s.handleAdminPlugins, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/plugins/install", s.handleAdminPluginInstallPost, s.adminMethodNotAllowed("providers", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/plugins/{plugin_id}/update", s.handleAdminPluginUpdatePost, s.adminMethodNotAllowed("providers", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/plugin-chain", s.handleAdminPluginChain, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/plugin-ui-manifest", s.handleAdminPluginUIManifest, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/plugin-actions", s.handleAdminPluginActions, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/plugin-background-jobs", s.handleAdminPluginBackgroundJobs, s.adminMethodNotAllowed("providers", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPatch, "/api/admin/plugins/{plugin_id}/state", s.handleAdminPluginStatePatch, s.adminMethodNotAllowed("providers", http.MethodPatch))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/plugins/{plugin_id}/actions/{action_id}", s.handleAdminPluginActionPost, s.adminMethodNotAllowed("providers", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", s.handleAdminOpenAIAccountOAuthGenerateAuthURL, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-account-oauth/openai/exchange-code", s.handleAdminOpenAIAccountOAuthExchangeCode, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback", s.handleOpenAIAccountOAuthCallbackGet, jsonMethodNotAllowed(http.MethodGet))
	s.registerMethodRoutes("/api/admin/api-keys", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("api_key", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminAPIKeysGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminAPIKeysPost},
	)
	s.registerMethodRoutes("/api/admin/api-keys/{key_id}", s.adminAPIKeyMethodNotAllowed,
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminAPIKeyPatch},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminAPIKeyDelete},
	)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/api-keys/{key_id}/usage", s.handleAdminAPIKeyUsageGet, s.adminAPIKeyMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/api-keys/{key_id}/rotate", s.handleAdminAPIKeyRotatePost, s.adminAPIKeyMethodNotAllowed(http.MethodPost))
	s.mux.HandleFunc("/api/admin/api-keys/", s.handleAdminAPIKeyItem)
	s.registerAdminAnalyticsCredentialRoutes()
	s.registerMethodRoutes("/api/admin/providers", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("provider", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminProvidersGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminProvidersPost},
	)
	// These static paths intentionally avoid path-only fallbacks: such a
	// fallback conflicts with the method-specific {provider_id} pattern. The
	// subtree handler below remains the ID-aware fallback for other methods.
	providerMonitoringMethodNotAllowed := s.adminMethodNotAllowed("provider", http.MethodGet)
	s.mux.HandleFunc(http.MethodGet+" /api/admin/providers/monitoring", s.handleAdminProviderMonitoring)
	s.mux.HandleFunc(http.MethodHead+" /api/admin/providers/monitoring", providerMonitoringMethodNotAllowed)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/providers/monitoring", providerMonitoringMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/providers/monitoring", providerMonitoringMethodNotAllowed)
	providerConnectionMethodNotAllowed := s.adminMethodNotAllowed("provider", http.MethodPost)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/providers/test-connection", s.handleAdminProviderTestConnectionPost)
	s.mux.HandleFunc(http.MethodHead+" /api/admin/providers/test-connection", providerConnectionMethodNotAllowed)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/providers/test-connection", providerConnectionMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/providers/test-connection", providerConnectionMethodNotAllowed)
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/providers/{provider_id}", s.handleAdminProviderPatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/providers/{provider_id}", s.handleAdminProviderDelete)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/providers/{provider_id}/health", s.handleAdminProviderHealthPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/providers/{provider_id}/test", s.handleAdminProviderTestPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/providers/{provider_id}/refresh-token", s.handleAdminProviderRefreshTokenPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.mux.HandleFunc("/api/admin/providers/", s.handleAdminProviderNested)
	s.registerMethodRoutes(
		"/api/admin/provider-resources",
		func(allowedMethods string) http.HandlerFunc {
			return s.adminMethodNotAllowed("provider", allowedMethods)
		},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminProviderResourcesGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminProviderResourcesPost},
	)
	providerResourceStaticMethodNotAllowed := s.adminMethodNotAllowed("provider", http.MethodPost)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/provider-resources/bulk", s.handleAdminProviderResourceBulkPost)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/provider-resources/bulk", providerResourceStaticMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/provider-resources/bulk", providerResourceStaticMethodNotAllowed)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/provider-resources/import", s.handleAdminProviderResourceImportPost)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/provider-resources/import", providerResourceStaticMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/provider-resources/import", providerResourceStaticMethodNotAllowed)
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/provider-resources/{resource_id}", s.handleAdminProviderResourcePatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/provider-resources/{resource_id}", s.handleAdminProviderResourceDelete)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-resources/{resource_id}/quota", s.handleAdminProviderResourceQuotaGet, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-resources/{resource_id}/quota/reset-credits", s.handleAdminProviderResourceQuotaResetCreditsGet, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/quota/reset", s.handleAdminProviderResourceQuotaResetPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/health", s.handleAdminProviderResourceHealthPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/test", s.handleAdminProviderResourceTestPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/image-capability", s.handleAdminProviderResourceImageCapabilityPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/refresh-token", s.handleAdminProviderResourceRefreshTokenPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.mux.HandleFunc("/api/admin/provider-resources/", s.handleAdminProviderResourceNested)
	providerModelImportMethodNotAllowed := s.adminMethodNotAllowed("model", http.MethodPost)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/provider-models/import", s.handleAdminProviderModelImport)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/provider-models/import", providerModelImportMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/provider-models/import", providerModelImportMethodNotAllowed)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-models", s.handleAdminProviderModels, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/provider-models/{provider_model_id}", s.handleAdminProviderModelPatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/provider-models/{provider_model_id}", s.handleAdminProviderModelDelete)
	s.mux.HandleFunc("/api/admin/provider-models/", s.handleAdminProviderModelItem)
	s.registerMethodRoutes("/api/admin/models", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("model", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminModelsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminModelsPost},
	)
	modelRestoreMethodNotAllowed := s.adminMethodNotAllowed("model", http.MethodPost)
	s.mux.HandleFunc(http.MethodPost+" /api/admin/models/restore-defaults", s.handleAdminModelsRestoreDefaults)
	s.mux.HandleFunc(http.MethodPatch+" /api/admin/models/restore-defaults", modelRestoreMethodNotAllowed)
	s.mux.HandleFunc(http.MethodDelete+" /api/admin/models/restore-defaults", modelRestoreMethodNotAllowed)
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/models/{model_name}", s.handleAdminModelPatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/models/{model_name}", s.handleAdminModelDelete)
	s.mux.HandleFunc("/api/admin/models/", s.handleAdminModelItem)
	s.registerSingleMethodRoute(http.MethodPatch, "/api/admin/model-routing-policies/{model_name}", s.handleAdminModelRoutingPolicyPatch, s.adminMethodNotAllowed("routing", http.MethodPatch))
	s.mux.HandleFunc("/api/admin/model-routing-policies/", s.handleAdminModelRoutingPolicy)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/routing-policies/simulate", s.handleAdminRoutingPolicySimulation, s.adminMethodNotAllowed("routing", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/routing-policies/{policy_id}/bind", s.handleAdminRoutingPolicyBindPost, s.adminMethodNotAllowed("routing", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/routing-policies/{policy_id}/unbind", s.handleAdminRoutingPolicyUnbindPost, s.adminMethodNotAllowed("routing", http.MethodPost))
	s.mux.HandleFunc("/api/admin/routing-policies/", s.handleAdminRoutingPolicyAction)
	s.registerMethodRoutes("/api/admin/routing-rules", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("routing", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminRoutesGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminRoutesPost},
	)
	s.registerDynamicMethodRoute(http.MethodPatch, "/api/admin/routing-rules/{route_id}", s.handleAdminRoutePatch)
	s.registerDynamicMethodRoute(http.MethodDelete, "/api/admin/routing-rules/{route_id}", s.handleAdminRouteDelete)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/routing-rules/{route_id}/explain", s.handleAdminRouteExplainGet, s.adminMethodNotAllowed("routing", http.MethodGet))
	s.mux.HandleFunc("/api/admin/routing-rules/", s.handleAdminRouteItem)
	s.registerMethodRoutes("/api/admin/resources/{kind}", s.adminResourceMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminResourceCollectionGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminResourceCollectionPost},
	)
	s.registerMethodRoutes("/api/admin/resources/{kind}/{resource_id}", s.adminResourceMethodNotAllowed,
		methodRoute{Method: http.MethodPatch, Handler: s.handleAdminResourcePatch},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminResourceDelete},
	)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/resources/invoices/{invoice_id}/confirm", s.handleAdminInvoiceConfirmPost, s.adminMethodNotAllowed("usage", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/resources/invoices/{invoice_id}/reject", s.handleAdminInvoiceRejectPost, s.adminMethodNotAllowed("usage", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/resources/monitors/{monitor_id}/run", s.handleAdminMonitorRunPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.mux.HandleFunc("/api/admin/resources/", s.handleAdminResources)
	s.registerMethodRoutes("/api/admin/sqlite/backups", func(allowedMethods string) http.HandlerFunc {
		return s.adminMethodNotAllowed("backup", allowedMethods)
	},
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminSQLiteBackupsGet},
		methodRoute{Method: http.MethodPost, Handler: s.handleAdminSQLiteBackupsPost},
	)
	s.registerMethodRoutes("/api/admin/sqlite/backups/{backup_id}", s.adminSQLiteBackupMethodNotAllowed,
		methodRoute{Method: http.MethodGet, Handler: s.handleAdminSQLiteBackupGet},
		methodRoute{Method: http.MethodDelete, Handler: s.handleAdminSQLiteBackupDelete},
	)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/sqlite/backups/{backup_id}/download", s.handleAdminSQLiteBackupDownloadGet, s.adminSQLiteBackupMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/sqlite/backups/{backup_id}/restore", s.handleAdminSQLiteBackupRestorePost, s.adminSQLiteBackupMethodNotAllowed(http.MethodPost))
	s.mux.HandleFunc("/api/admin/sqlite/backups/", s.handleAdminSQLiteBackupItem)
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/billing/generate", s.handleAdminGenerateBilling, s.adminMethodNotAllowed("usage", http.MethodPost))
	s.registerBillingRoutes()
	s.registerDynamicGETRoute("/api/admin/export/{dataset}", s.handleAdminExportGet, s.adminMethodNotAllowed("usage", http.MethodGet))
	s.mux.HandleFunc("/api/admin/export/", s.handleAdminExport)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/usage/summary", s.handleAdminUsageSummary, s.adminMethodNotAllowed("usage", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/usage/daily", s.handleAdminUsageDaily, s.adminMethodNotAllowed("usage", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/usage/breakdown", s.handleAdminUsageBreakdown, s.adminMethodNotAllowed("usage", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/usage/timeseries", s.handleAdminUsageTimeseries, s.adminMethodNotAllowed("usage", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/audit/requests", s.handleAdminRequestLogs, s.adminMethodNotAllowed("audit", http.MethodGet))
	s.registerDynamicGETRoute("/api/admin/audit/requests/{request_id}", s.handleAdminRequestDetailGet, s.adminMethodNotAllowed("audit", http.MethodGet))
	s.mux.HandleFunc("/api/admin/audit/requests/", s.handleAdminRequestDetail)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/audit/image-jobs", s.handleAdminImageJobs, s.adminMethodNotAllowed("audit", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/audit/events", s.handleAdminAuditEvents, s.adminMethodNotAllowed("admin_audit", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/alerts", s.handleAdminAlerts, s.adminMethodNotAllowed("alert", http.MethodGet))
	s.registerDynamicMethodRoute(http.MethodPost, "/api/admin/alerts/{alert_id}/deliver", s.handleAdminAlertDeliverPost)
	s.mux.HandleFunc("/api/admin/alerts/", s.handleAdminAlertItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/alert-deliveries", s.handleAdminAlertDeliveries, s.adminMethodNotAllowed("alert", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/approvals", s.handleAdminApprovals, s.adminMethodNotAllowed("approval", http.MethodGet))
	s.registerDynamicMethodRoute(http.MethodPost, "/api/admin/approvals/{approval_id}/approve", s.handleAdminApprovalApprovePost)
	s.registerDynamicMethodRoute(http.MethodPost, "/api/admin/approvals/{approval_id}/reject", s.handleAdminApprovalRejectPost)
	s.mux.HandleFunc("/api/admin/approvals/", s.handleAdminApprovalItem)
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/system/db-status", s.handleAdminSystemDBStatus, plainTextMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/system/schema-status", s.handleAdminSchemaStatus, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/system/version", s.handleAdminSystemVersion, jsonMethodNotAllowed(http.MethodGet))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/system/update", s.handleAdminSystemUpdate, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/system/rollback", s.handleAdminSystemRollback, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/system/restart", s.handleAdminSystemRestart, jsonMethodNotAllowed(http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/system/rollback-versions", s.handleAdminRollbackVersions, jsonMethodNotAllowed(http.MethodGet))
}
