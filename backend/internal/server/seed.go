package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const defaultProjectID = "prj_default"

// RunStartupBootstrap reapplies idempotent startup data on every process start
// while serializing replicas through a cluster-wide lease. The contextual
// store makes lease loss cancel the database operations still in progress.
func RunStartupBootstrap(ctx context.Context, store *GormStore, config Config) error {
	operation := "bootstrap-base"
	seed := BootstrapBaseDataWithConfig
	if config.SeedDemo {
		operation = "bootstrap-demo"
		seed = SeedDemoDataWithConfig
	}
	return store.RunClusterOperation(ctx, operation, func(leaseCtx context.Context) error {
		contextual := store.WithContext(leaseCtx)
		if err := contextual.NormalizeProviderAdapterTypes(leaseCtx); err != nil {
			return err
		}
		return seed(contextual, config)
	})
}

func SeedDemoData(store Store) error {
	return SeedDemoDataWithConfig(store, ConfigFromEnv())
}

func SeedDemoDataWithConfig(store Store, config Config) error {
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		return err
	}

	project := store.CreateProject(Project{
		ID:     "prj_demo",
		Name:   "Demo Project",
		TeamID: "team_platform",
		Status: StatusActive,
	})

	_, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID:      "key_demo",
		Name:    "Demo Local Key",
		Allowed: []string{"gpt-4.1-mini", "text-embedding-3-small"},
		Limits: QuotaLimits{
			DailyRequests:   1000,
			MonthlyRequests: 30000,
			DailyTokens:     1_000_000,
			MonthlyTokens:   20_000_000,
			DailyCostUSD:    100,
			MonthlyCostUSD:  2000,
			MaxConcurrency:  20,
		},
		Status: StatusActive,
	}, "thk_demo_local")
	if err != nil {
		if AsHTTPError(err).Code != "api_key_conflict" {
			return err
		}
	}

	mock := store.AddProvider(Provider{
		ID:       "prv_mock",
		Name:     "Mock Provider",
		Type:     ProviderMock,
		Status:   StatusActive,
		Healthy:  true,
		Priority: 1,
	})
	mockResource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_mock_primary",
		ProviderID:   mock.ID,
		Name:         "Mock Primary Resource",
		ResourceType: "mock",
		Region:       "local",
		Environment:  "dev",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		return err
	}

	store.AddModel(Model{
		ID:                  "gpt-4.1-mini",
		Name:                "gpt-4.1-mini",
		Family:              "gpt",
		Modality:            "chat",
		ContextWindow:       128000,
		InputPriceUSDPer1M:  0.4,
		OutputPriceUSDPer1M: 1.6,
		Status:              StatusActive,
	})
	store.AddModel(Model{
		ID:                     "text-embedding-3-small",
		Name:                   "text-embedding-3-small",
		Family:                 "gpt",
		Modality:               "embedding",
		ContextWindow:          8192,
		EmbeddingPriceUSDPer1M: 0.02,
		Status:                 StatusActive,
	})

	store.AddRoute(ModelRoute{
		ID:                 "route_demo_chat",
		ModelName:          "gpt-4.1-mini",
		ProviderID:         mock.ID,
		ProviderResourceID: mockResource.ID,
		ProviderModel:      "mock-chat",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})
	store.AddRoute(ModelRoute{
		ID:                 "route_demo_embedding",
		ModelName:          "text-embedding-3-small",
		ProviderID:         mock.ID,
		ProviderResourceID: mockResource.ID,
		ProviderModel:      "mock-embedding",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})

	if err := seedAdminResources(store); err != nil {
		return err
	}

	if err := seedMockData(store); err != nil {
		return err
	}

	return nil
}

func BootstrapBaseData(store Store) error {
	return BootstrapBaseDataWithConfig(store, ConfigFromEnv())
}

func BootstrapBaseDataWithConfig(store Store, config Config) error {
	if err := seedDefaultOrgResources(store); err != nil {
		return err
	}
	if err := seedBootstrapAdmin(store, config.BootstrapAdminPassword); err != nil {
		return err
	}
	seedDefaultProject(store)
	if err := seedBuiltinProviderCatalog(store); err != nil {
		return err
	}
	if err := seedDefaultModelCatalog(store, config.ModelCatalogFile); err != nil {
		return err
	}
	return nil
}

func seedBootstrapAdmin(store Store, password string) error {
	admin := AdminUser{
		ID:       "usr_admin",
		Username: "admin",
		Name:     "Platform Admin",
		Email:    "admin@tokenhub.local",
		Role:     "admin",
		TeamID:   "team_platform",
		Status:   StatusActive,
	}
	for _, existing := range store.ListAdminUsers() {
		if existing.ID == admin.ID || existing.Username == admin.Username || existing.Email == admin.Email {
			return nil
		}
	}
	if _, err := store.CreateAdminUser(admin, password); err != nil {
		if AsHTTPError(err).Code != "admin_user_conflict" {
			return err
		}
	}
	return nil
}

func seedDefaultProject(store Store) {
	if _, ok := store.GetProject(defaultProjectID); ok {
		return
	}
	store.CreateProject(Project{
		ID:          defaultProjectID,
		Name:        "Default Project Space",
		TeamID:      "team_platform",
		OwnerUserID: "usr_admin",
		CostCenter:  "AI-PLATFORM",
		Status:      StatusActive,
	})
}

func seedDefaultModelCatalog(store Store, catalogFile string) error {
	models, err := defaultModelCatalog(catalogFile)
	if err != nil {
		return err
	}
	existingByName := make(map[string]Model)
	for _, model := range store.ListModels() {
		existingByName[model.Name] = model
	}
	for _, model := range models {
		if existing, ok := existingByName[model.Name]; ok {
			model.InputPriceUSDPer1M = existing.InputPriceUSDPer1M
			model.CacheReadPriceUSDPer1M = existing.CacheReadPriceUSDPer1M
			model.OutputPriceUSDPer1M = existing.OutputPriceUSDPer1M
			model.EmbeddingPriceUSDPer1M = existing.EmbeddingPriceUSDPer1M
		}
		if _, err := store.CreateModelWithRoutes(model, nil); err != nil {
			return fmt.Errorf("seed catalog model %q: %w", model.Name, err)
		}
	}
	return nil
}

func seedDefaultOrgResources(store Store) error {
	seedResourceIfMissing(store, "teams", AdminResource{
		ID:          "team_platform",
		Name:        "Platform Engineering Team",
		Description: "Owns internal AI Gateway onboarding and platform governance.",
		Status:      StatusActive,
		Fields: map[string]any{
			"owner":       "usr_admin",
			"cost_center": "AI-PLATFORM",
		},
	})
	seedResourceIfMissing(store, "cost-centers", AdminResource{
		ID:          "cc_ai_platform",
		Name:        "AI Platform Cost Center",
		Description: "Cost attribution for platform engineering and shared AI infrastructure.",
		Status:      StatusActive,
		Fields: map[string]any{
			"code":               "AI-PLATFORM",
			"department":         "Platform Engineering",
			"owner":              "usr_admin",
			"monthly_budget_usd": 5000,
		},
	})
	seedResourceIfMissing(store, "security-policies", AdminResource{
		ID:          "sec_ip_allowlist",
		Name:        "Production IP Allowlist Policy",
		Description: "Recommended model API IP allowlist, prompt masking, and sanitized error passthrough rules.",
		Status:      StatusActive,
		Fields: map[string]any{
			"mask_prompts":      true,
			"ip_allowlist":      "127.0.0.1/32\n10.0.0.0/8",
			"error_passthrough": "sanitized",
		},
	})
	if err := seedResourceIfMissingChecked(store, "settings", AdminResource{
		ID:          "cfg_gateway",
		Name:        "Gateway Base Settings",
		Description: "Public model API address, request timeout, and audit retention period.",
		Status:      StatusActive,
		Fields: map[string]any{
			"public_base_url":             "http://localhost:8080",
			"default_timeout":             "120s",
			"audit_retention":             "180d",
			"api_key_prefix":              DefaultAPIKeyPrefix,
			"api_key_random_length":       DefaultAPIKeyRandomLength,
			syntheticDNSEnabledField:      false,
			syntheticDNSCIDRsField:        defaultSyntheticDNSCIDRs,
			syntheticDNSAllowPrivateField: false,
		},
	}); err != nil {
		return fmt.Errorf("seed gateway settings: %w", err)
	}
	if err := ensureProviderSyntheticDNSSettings(store); err != nil {
		return err
	}
	seedResourceIfMissing(store, "identity-providers", AdminResource{
		ID:          "idp_oidc_template",
		Name:        "Enterprise OIDC/OAuth Identity Source",
		Description: "Template for connecting existing SSO, OAuth, or OIDC identity systems for unified login and user sync.",
		Status:      "draft",
		Fields: map[string]any{
			"provider_type":  "oidc",
			"issuer_url":     "https://sso.example.com",
			"client_id":      "tokenhub-admin",
			"client_secret":  "",
			"authorize_url":  "https://sso.example.com/oauth2/authorize",
			"token_url":      "https://sso.example.com/oauth2/token",
			"userinfo_url":   "https://sso.example.com/oauth2/userinfo",
			"redirect_uri":   "http://localhost:8080/api/admin/auth/oauth/callback",
			"scopes":         "openid, profile, email",
			"username_claim": "preferred_username",
			"email_claim":    "email",
			"team_claim":     "department",
		},
	})
	seedDefaultRoleConfigs(store)
	return nil
}

func seedDefaultRoleConfigs(store Store) {
	roles := []AdminResource{
		{
			ID:          "role_user",
			Name:        "Regular User",
			Description: "Can create personal API keys and view personal request logs and usage.",
			Status:      StatusActive,
			Fields: map[string]any{
				"role_key":     "user",
				"display_name": "Regular User",
				"data_scope":   "self",
				"assignable":   true,
			},
		},
		{
			ID:          "role_team_leader",
			Name:        "Team Leader",
			Description: "Manages team members and views team usage and cost.",
			Status:      StatusActive,
			Fields: map[string]any{
				"role_key":     "team_leader",
				"display_name": "Team Leader",
				"data_scope":   "team",
				"assignable":   true,
			},
		},
		{
			ID:          "role_admin",
			Name:        "Platform Admin",
			Description: "Manages platform settings, Providers, model routes, users, and governance policies.",
			Status:      StatusActive,
			Fields: map[string]any{
				"role_key":     "admin",
				"display_name": "Platform Admin",
				"data_scope":   "global",
				"assignable":   true,
			},
		},
	}
	for _, role := range roles {
		seedResourceIfMissing(store, "role-configs", role)
	}
}

func seedResourceIfMissing(store Store, kind string, resource AdminResource) {
	for _, existing := range store.ListResources(kind) {
		if existing.ID == resource.ID {
			return
		}
	}
	store.CreateResource(kind, resource)
}

func seedResourceIfMissingChecked(store Store, kind string, resource AdminResource) error {
	resources, err := store.ListResourcesChecked(kind)
	if err != nil {
		return err
	}
	for _, existing := range resources {
		if existing.ID == resource.ID {
			return nil
		}
	}
	_, err = store.CreateResourceChecked(kind, resource)
	return err
}

func seedAdminResources(store Store) error {
	if err := seedDefaultOrgResources(store); err != nil {
		return err
	}
	store.CreateResource("monitors", AdminResource{
		ID:          "mon_gateway",
		Name:        "Core Chat Model Heartbeat",
		Description: "Checks the gpt-4.1-mini routing chain every 60 seconds.",
		Status:      StatusActive,
		Fields: map[string]any{
			"target_type":      "model",
			"provider":         "mock",
			"model":            "gpt-4.1-mini",
			"interval_seconds": 60,
			"last_result":      "ok",
		},
	})
	store.CreateResource("proxies", AdminResource{
		ID:          "prx_direct",
		Name:        "Direct Egress",
		Description: "Default egress without a proxy.",
		Status:      StatusActive,
		Fields: map[string]any{
			"protocol": "direct",
			"host":     "-",
			"port":     0,
		},
	})
	store.CreateResource("announcements", AdminResource{
		ID:          "ann_mvp",
		Name:        "Internal Rollout Notice",
		Description: "TokenHub is available for internal rollout. Provider credentials must only be used for enterprise-authorized APIs.",
		Status:      StatusActive,
		Fields: map[string]any{
			"notify_mode": "silent",
			"target":      "all_admins",
		},
	})
	store.CreateResource("security-policies", AdminResource{
		ID:          "sec_default",
		Name:        "Default Security Policy",
		Description: "Records request metadata and masks sensitive request body fields.",
		Status:      StatusActive,
		Fields: map[string]any{
			"mask_prompts":      true,
			"ip_allowlist":      "",
			"error_passthrough": "sanitized",
		},
	})
	store.CreateResource("alert-rules", AdminResource{
		ID:          "alr_quota",
		Name:        "Quota Exhaustion Alert",
		Description: "Notifies administrators when key or project quota reaches the threshold.",
		Status:      StatusActive,
		Fields: map[string]any{
			"metric":    "daily_cost_usd",
			"threshold": "90%",
			"channel":   "console",
		},
	})
	store.CreateResource("quota-policies", AdminResource{
		ID:          "quo_default_project",
		Name:        "Default Project Quota",
		Description: "Default daily request, token, and cost limits for new projects.",
		Status:      StatusActive,
		Fields: map[string]any{
			"daily_requests":   1000,
			"daily_tokens":     1000000,
			"daily_cost_usd":   100,
			"max_concurrency":  20,
			"scope":            "project",
			"enforcement_mode": "hard",
		},
	})
	store.CreateResource("budgets", AdminResource{
		ID:          "bdg_ai_platform_monthly",
		Name:        "AI Platform Monthly Budget",
		Description: "AI usage budget and warning line by cost center.",
		Status:      StatusActive,
		Fields: map[string]any{
			"scope":         "cost_center",
			"scope_id":      "AI-PLATFORM",
			"period":        "monthly",
			"period_ref":    time.Now().UTC().Format("2006-01"),
			"amount_usd":    5000,
			"warn_percent":  80,
			"used_usd":      0,
			"usage_percent": 0,
		},
	})
	store.CreateResource("approval-flows", AdminResource{
		ID:          "apf_budget_change",
		Name:        "Budget Change Approval",
		Description: "Requires administrator approval when a budget change exceeds the threshold.",
		Status:      StatusActive,
		Fields: map[string]any{
			"trigger":       "budget_change",
			"approver_role": "admin",
			"threshold_usd": 1000,
			"sla_hours":     24,
		},
	})
	store.CreateResource("approval-flows", AdminResource{
		ID:          "apf_invoice_confirm",
		Name:        "Internal Invoice Confirmation Approval",
		Description: "Routes high-value internal invoice confirmations through approval.",
		Status:      StatusActive,
		Fields: map[string]any{
			"trigger":       "invoice_confirm",
			"approver_role": "admin",
			"threshold_usd": 1000,
			"sla_hours":     48,
		},
	})
	store.CreateResource("reports", AdminResource{
		ID:          "rpt_monthly_invoices",
		Name:        "Monthly Internal Invoice Export",
		Description: "Internal billing CSV for finance month-end close.",
		Status:      StatusActive,
		Fields: map[string]any{
			"dataset":    "invoices",
			"schedule":   "monthly",
			"recipients": "finance@example.com",
		},
	})
	return nil
}

func seedMockData(store Store) error {
	mockPasswordHash, err := hashPassword("mock123456")
	if err != nil {
		return err
	}
	seedMockResources(store)
	for i := 1; i <= 80; i++ {
		teamID := fmt.Sprintf("team_mock_%02d", ((i-1)%24)+1)
		projectID := fmt.Sprintf("prj_mock_%03d", i)
		project := store.CreateProject(Project{
			ID:              projectID,
			Name:            fmt.Sprintf("Mock App Project %03d", i),
			TeamID:          teamID,
			OwnerUserID:     fmt.Sprintf("usr_mock_%03d", ((i-1)%60)+1),
			Status:          activeEvery(i, 11),
			DefaultQuotaRef: fmt.Sprintf("quo_mock_%02d", ((i-1)%40)+1),
		})
		for keyIndex := 1; keyIndex <= 2; keyIndex++ {
			rawSecret := fmt.Sprintf("thk_mock_%03d_%d", i, keyIndex)
			_, _, err := store.CreateAPIKey(project.ID, APIKey{
				ID:      fmt.Sprintf("key_mock_%03d_%d", i, keyIndex),
				Name:    fmt.Sprintf("Mock Key %03d-%d", i, keyIndex),
				Allowed: mockAllowedModels(i + keyIndex),
				Limits: QuotaLimits{
					DailyRequests:   int64(800 + i*10),
					MonthlyRequests: int64(24000 + i*120),
					DailyTokens:     int64(900000 + i*12000),
					MonthlyTokens:   int64(18000000 + i*300000),
					DailyCostUSD:    float64(80 + i),
					MonthlyCostUSD:  float64(1200 + i*15),
					MaxConcurrency:  int64(6 + (i % 18)),
				},
				Status: activeEvery(i+keyIndex, 13),
			}, rawSecret)
			if err != nil && AsHTTPError(err).Code != "api_key_conflict" {
				return err
			}
		}
	}

	providerTypes := []string{
		ProviderOpenAICompatible,
		ProviderOpenAI,
		ProviderAzureOpenAI,
		ProviderAnthropic,
		ProviderGemini,
		"deepseek",
		"qwen",
		"local",
	}
	for i := 1; i <= 36; i++ {
		providerType := providerTypes[(i-1)%len(providerTypes)]
		provider := store.AddProvider(Provider{
			ID:       fmt.Sprintf("prv_mock_%03d", i),
			Name:     fmt.Sprintf("Mock Provider %03d", i),
			Type:     providerType,
			BaseURL:  fmt.Sprintf("https://mock-provider-%03d.internal/v1", i),
			Status:   activeEvery(i, 10),
			Healthy:  true,
			Priority: 1 + (i % 9),
			Headers: map[string]string{
				"x-mock-region": mockRegion(i),
			},
			Options: map[string]string{
				"tier": mockTier(i),
			},
		})
		for resourceIndex := 1; resourceIndex <= 2; resourceIndex++ {
			_, err := store.AddProviderResource(ProviderResource{
				ID:             fmt.Sprintf("rsrc_mock_%03d_%d", i, resourceIndex),
				ProviderID:     provider.ID,
				Name:           fmt.Sprintf("Mock Resource Instance %03d-%d", i, resourceIndex),
				ResourceType:   mockResourceType(providerType),
				BaseURL:        provider.BaseURL,
				Region:         mockRegion(i + resourceIndex),
				Environment:    mockEnvironment(resourceIndex),
				Status:         activeEvery(i+resourceIndex, 12),
				Healthy:        true,
				Priority:       resourceIndex,
				Weight:         120 - resourceIndex*20 - (i % 15),
				RateLimitRPM:   int64(600 + i*20 + resourceIndex*50),
				TokenLimitTPM:  int64(90000 + i*1500),
				MaxConcurrency: int64(10 + (i % 12)),
				Headers: map[string]string{
					"x-tokenhub-resource-region": mockRegion(i + resourceIndex),
				},
				Options: map[string]string{
					"owner": fmt.Sprintf("team_mock_%02d", ((i-1)%24)+1),
				},
			})
			if err != nil {
				return err
			}
		}
	}

	for i := 1; i <= 96; i++ {
		name := fmt.Sprintf("mock-model-%03d", i)
		modality := "chat"
		if i%7 == 0 {
			modality = "embedding"
		} else if i%19 == 0 {
			modality = "image"
		}
		store.AddModel(Model{
			ID:                     name,
			Name:                   name,
			Family:                 mockModelFamily(i),
			Modality:               modality,
			ContextWindow:          int64(8192 * (1 + (i % 16))),
			InputPriceUSDPer1M:     float64(10+(i%40)) / 100,
			OutputPriceUSDPer1M:    float64(20+(i%80)) / 100,
			EmbeddingPriceUSDPer1M: float64(1+(i%12)) / 100,
			Status:                 activeEvery(i, 17),
		})
		store.AddRoute(ModelRoute{
			ID:                 fmt.Sprintf("route_mock_%03d_primary", i),
			ModelName:          name,
			ProviderID:         fmt.Sprintf("prv_mock_%03d", ((i-1)%36)+1),
			ProviderResourceID: fmt.Sprintf("rsrc_mock_%03d_1", ((i-1)%36)+1),
			ProviderModel:      fmt.Sprintf("upstream-%s", name),
			Priority:           1,
			Weight:             100 - (i % 20),
			Status:             activeEvery(i, 23),
		})
		store.AddRoute(ModelRoute{
			ID:                 fmt.Sprintf("route_mock_%03d_backup", i),
			ModelName:          name,
			ProviderID:         fmt.Sprintf("prv_mock_%03d", ((i+7)%36)+1),
			ProviderResourceID: fmt.Sprintf("rsrc_mock_%03d_2", ((i+7)%36)+1),
			ProviderModel:      fmt.Sprintf("backup-%s", name),
			Priority:           2,
			Weight:             60 + (i % 30),
			Status:             activeEvery(i, 29),
		})
	}

	for i := 1; i <= 72; i++ {
		_, err := store.CreateAdminUser(AdminUser{
			ID:           fmt.Sprintf("usr_mock_%03d", i),
			Username:     fmt.Sprintf("mock.user%03d", i),
			Name:         fmt.Sprintf("Mock User %03d", i),
			Email:        fmt.Sprintf("mock.user%03d@tokenhub.local", i),
			Role:         mockRole(i),
			TeamID:       mockActiveTeamID(i),
			Status:       activeEvery(i, 16),
			PasswordHash: mockPasswordHash,
		}, "")
		if err != nil && AsHTTPError(err).Code != "admin_user_conflict" {
			return err
		}
	}

	seedMockUsage(store)
	return nil
}

func seedMockResources(store Store) {
	for i := 1; i <= 60; i++ {
		store.CreateResource("teams", AdminResource{
			ID:          fmt.Sprintf("team_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Business Team %02d", i),
			Description: fmt.Sprintf("Owns AI onboarding, quota governance, and cost attribution for product line %02d.", i),
			Status:      activeEvery(i, 14),
			Fields: map[string]any{
				"owner":       fmt.Sprintf("Mock Owner %02d", i),
				"cost_center": fmt.Sprintf("MOCK-CC-%03d", i),
			},
		})
	}
	for i := 1; i <= 70; i++ {
		store.CreateResource("quota-policies", AdminResource{
			ID:          fmt.Sprintf("quo_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Quota Policy %02d", i),
			Description: "Quota template for pagination, filtering, and policy editing tests.",
			Status:      activeEvery(i, 15),
			Fields: map[string]any{
				"scope":            quotaScope(i),
				"daily_requests":   500 + i*25,
				"daily_tokens":     500000 + i*20000,
				"daily_cost_usd":   20 + i,
				"max_concurrency":  4 + (i % 20),
				"enforcement_mode": "hard",
			},
		})
	}
	for i := 1; i <= 90; i++ {
		store.CreateResource("monitors", AdminResource{
			ID:          fmt.Sprintf("mon_mock_%03d", i),
			Name:        fmt.Sprintf("Mock Health Monitor %03d", i),
			Description: "Model route heartbeat and Provider availability monitor.",
			Status:      activeEvery(i, 18),
			Fields: map[string]any{
				"provider":         fmt.Sprintf("prv_mock_%03d", ((i-1)%36)+1),
				"model":            fmt.Sprintf("mock-model-%03d", ((i-1)%96)+1),
				"interval_seconds": 30 + (i%8)*15,
				"last_result":      monitorResult(i),
			},
		})
	}
	for i := 1; i <= 60; i++ {
		store.CreateResource("proxies", AdminResource{
			ID:          fmt.Sprintf("prx_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Proxy Egress %02d", i),
			Description: fmt.Sprintf("Egress policy for the %s region.", mockRegion(i)),
			Status:      activeEvery(i, 20),
			Fields: map[string]any{
				"protocol": proxyProtocol(i),
				"host":     fmt.Sprintf("proxy-%02d.internal", i),
				"port":     8000 + i,
			},
		})
	}
	for i := 1; i <= 45; i++ {
		store.CreateResource("announcements", AdminResource{
			ID:          fmt.Sprintf("ann_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Operations Notice %02d", i),
			Description: "Used to verify announcement lists, pagination, and editing behavior.",
			Status:      activeEvery(i, 9),
			Fields: map[string]any{
				"notify_mode": mockNotifyMode(i),
				"target":      fmt.Sprintf("team_mock_%02d", ((i-1)%24)+1),
			},
		})
	}
	for i := 1; i <= 50; i++ {
		store.CreateResource("settings", AdminResource{
			ID:          fmt.Sprintf("cfg_mock_%02d", i),
			Name:        fmt.Sprintf("Mock System Setting %02d", i),
			Description: "Used to test settings pagination and configuration editing.",
			Status:      activeEvery(i, 22),
			Fields: map[string]any{
				"public_base_url": fmt.Sprintf("https://gateway-%02d.internal", i),
				"default_timeout": fmt.Sprintf("%ds", 30+(i%8)*15),
				"audit_retention": fmt.Sprintf("%dd", 30+(i%12)*15),
			},
		})
	}
	for i := 1; i <= 65; i++ {
		store.CreateResource("security-policies", AdminResource{
			ID:          fmt.Sprintf("sec_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Security Policy %02d", i),
			Description: "Used to test security policy lists, editing, and pagination.",
			Status:      activeEvery(i, 13),
			Fields: map[string]any{
				"mask_prompts":      i%3 != 0,
				"ip_allowlist":      fmt.Sprintf("10.%d.0.0/16", i%255),
				"error_passthrough": errorPassthrough(i),
			},
		})
	}
	for i := 1; i <= 75; i++ {
		store.CreateResource("alert-rules", AdminResource{
			ID:          fmt.Sprintf("alr_mock_%02d", i),
			Name:        fmt.Sprintf("Mock Alert Rule %02d", i),
			Description: "Used to test quota, cost, error-rate, and Provider health alert configuration.",
			Status:      activeEvery(i, 19),
			Fields: map[string]any{
				"metric":    alertMetric(i),
				"threshold": fmt.Sprintf("%d%%", 65+(i%30)),
				"channel":   alertChannel(i),
			},
		})
	}
}

func seedMockUsage(store Store) {
	if len(store.ListRequestLogs()) >= 220 {
		return
	}
	for i := 1; i <= 260; i++ {
		secret := fmt.Sprintf("thk_mock_%03d_%d", ((i-1)%80)+1, (i%2)+1)
		project, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
		if err != nil {
			continue
		}
		modelName := "gpt-4.1-mini"
		call, err := store.StartCall(context.Background(), project, key, modelName, 0)
		if err != nil {
			store.RecordRejectedRequest(project, key, modelName, false, http.StatusTooManyRequests, "quota_exceeded", mockIP(i), "mock-seed/1.0")
			continue
		}
		route, err := store.SelectRoute(modelName)
		if err != nil {
			store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusServiceUnavailable, "provider_unavailable", mockIP(i), "mock-seed/1.0")
			continue
		}
		status := http.StatusOK
		errorCode := ""
		if i%17 == 0 {
			status = http.StatusBadGateway
			errorCode = "upstream_error"
		}
		usage := Usage{
			PromptTokens:     int64(120 + i*3),
			CompletionTokens: int64(80 + i*2),
		}
		store.FinishCall(call, route, usage, status, errorCode, mockIP(i), "mock-seed/1.0")
	}
	for i := 1; i <= 40; i++ {
		project, key, err := store.ValidateAPIKey(fmt.Sprintf("thk_mock_%03d_%d", ((i-1)%80)+1, (i%2)+1), "127.0.0.1")
		if err != nil {
			continue
		}
		store.RecordRejectedRequest(project, key, fmt.Sprintf("blocked-model-%02d", i), false, http.StatusForbidden, "model_not_allowed", mockIP(300+i), "mock-seed/1.0")
	}
}

func activeEvery(index int, disabledEvery int) string {
	if disabledEvery > 0 && index%disabledEvery == 0 {
		return StatusDisabled
	}
	return StatusActive
}

func mockActiveTeamID(index int) string {
	teamNumber := ((index - 1) % 24) + 1
	if teamNumber%14 == 0 {
		teamNumber++
	}
	return fmt.Sprintf("team_mock_%02d", teamNumber)
}

func mockAllowedModels(seed int) []string {
	models := []string{"gpt-4.1-mini", "text-embedding-3-small"}
	for i := 0; i < 5; i++ {
		models = append(models, fmt.Sprintf("mock-model-%03d", ((seed+i*7-1)%96)+1))
	}
	return models
}

func mockRegion(index int) string {
	regions := []string{"cn-north", "cn-east", "sg", "tokyo", "us-west", "eu-central"}
	return regions[(index-1)%len(regions)]
}

func mockTier(index int) string {
	tiers := []string{"standard", "priority", "backup", "experimental"}
	return tiers[(index-1)%len(tiers)]
}

func mockResourceType(providerType string) string {
	switch providerType {
	case ProviderAzureOpenAI:
		return "azure_resource"
	case ProviderGemini:
		return "service_account"
	case "local":
		return "local_cluster"
	case ProviderMock:
		return "mock"
	default:
		return "api_key"
	}
}

func mockEnvironment(index int) string {
	environments := []string{"prod", "backup", "staging"}
	return environments[(index-1)%len(environments)]
}

func mockModelFamily(index int) string {
	families := []string{"gpt", "claude", "gemini", "qwen", "deepseek", "llama", "embedding"}
	return families[(index-1)%len(families)]
}

func mockRole(index int) string {
	roles := []string{"user", "team_leader", "security", "admin"}
	return roles[(index-1)%len(roles)]
}

func quotaScope(index int) string {
	scopes := []string{"project", "api_key", "user", "team"}
	return scopes[(index-1)%len(scopes)]
}

func monitorResult(index int) string {
	if index%13 == 0 {
		return "degraded"
	}
	if index%17 == 0 {
		return "failed"
	}
	return "ok"
}

func proxyProtocol(index int) string {
	protocols := []string{"direct", "http", "https", "socks5"}
	return protocols[(index-1)%len(protocols)]
}

func mockNotifyMode(index int) string {
	if index%4 == 0 {
		return "popup"
	}
	return "silent"
}

func errorPassthrough(index int) string {
	modes := []string{"sanitized", "masked", "internal_only"}
	return modes[(index-1)%len(modes)]
}

func alertMetric(index int) string {
	metrics := []string{"daily_cost_usd", "daily_tokens", "error_rate", "provider_health", "latency_p95"}
	return metrics[(index-1)%len(metrics)]
}

func alertChannel(index int) string {
	channels := []string{"console", "email", "webhook", "feishu"}
	return channels[(index-1)%len(channels)]
}

func mockIP(index int) string {
	return fmt.Sprintf("10.%d.%d.%d", (index/128)%255, (index/16)%255, index%255)
}
