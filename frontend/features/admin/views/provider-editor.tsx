import { AlertCircle, Ban, Check, Copy, KeyRound, Plus, Search, Send, Trash2, UserRoundCheck } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { clearPendingProviderAccountOAuthSession, consumePendingProviderAccountOAuthResult, hasPendingProviderAccountOAuthResult, parseProviderAccountOAuthResult, providerAccountOAuthCallbackURL, type ProviderAccountOAuthGenerateResponse, type ProviderAccountOAuthResult, readPendingProviderAccountOAuthSession, savePendingProviderAccountOAuthSession } from "../core/session";
import { type ApiContext, type Model, type ModelRoute, type Provider, type ProviderCatalogEntry, type ProviderCredentialMode, type ProviderResource } from "../core/types";
import { buildCustomProviderCatalogEntry, canonicalModelNameForUI, catalogModelCategoryOptions, modelCategory, modelCategoryForCatalog, modelCategoryLabel, providerEntryCategoryCount, providerEntrySupportsCategory } from "../domain/catalog";
import { compactNumber, formatModelPrice, modelCapabilities } from "../domain/formatting";
import { providerTypeLabel } from "../domain/labels";
import { countWithLabel, countWithUnit, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, providerPayload, providerResourcePayload, providerUpdatePayload, readAdminError } from "../resources/payloads";
import { assertProviderAccountResourceReady, defaultProviderResourceName, providerAccountTokenSummary, providerCreateAccountManualTokenFields, providerCreateAccountRuntimeFields, providerResourceDraftDefaults } from "../resources/provider-model-config";
import { ReviewItem } from "../shared/modals";
import { providerTypeOptions } from "../shared/ui";
import { ProviderInlineField, providerAccountResourceReady, providerCreateWizardSteps, providerCreateWizardStepTitle, providerCredentialModeLabel, providerCredentialOptions } from "./provider-editor-fields";

const openAIAccountOAuthRedirectURI = "http://localhost:1455/auth/callback";

type OpenAIQuotaWindow = {
  used_percent: number;
  limit_window_seconds: number;
  reset_after_seconds: number;
  reset_at: number;
};

type OpenAIAccountQuota = {
  account_id?: string;
  email?: string;
  plan_type?: string;
  rate_limit?: {
    allowed: boolean;
    limit_reached: boolean;
    primary_window?: OpenAIQuotaWindow;
    secondary_window?: OpenAIQuotaWindow;
  };
  additional_rate_limits?: Array<{
    limit_name: string;
    metered_feature: string;
    rate_limit?: {
      allowed: boolean;
      limit_reached: boolean;
      primary_window?: OpenAIQuotaWindow;
      secondary_window?: OpenAIQuotaWindow;
    };
  }>;
  rate_limit_reset_credits?: { available_count: number };
  fetched_at: number;
};

type CodexSubscriptionTestResult = {
  resource_id: string;
  model: string;
  reasoning_effort: string;
  speed: "standard" | "fast";
  upstream_service_tier?: string;
  output_text: string;
  latency_ms: number;
  usage: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
};

type ProviderEditTab = "quota" | "test" | "settings" | "models";

type ProviderAccountConfirmation = {
  action: "enable" | "disable" | "delete";
  resource: ProviderResource;
};

const deleteAccountConfirmationPhrase = "DELETE THIS ACCOUNT";

const codexProviderCatalogSummary: ProviderCatalogEntry = {
  id: "openai-codex",
  name: "OpenAI Codex",
  display_name: "OpenAI Codex",
  type: "openai_codex",
  base_url: "https://chatgpt.com/backend-api/codex",
  categories: ["codex"],
  category_counts: { codex: 0 },
  models_count: 0,
  source: "openai-codex-live",
};

const accountProviderCatalogOptions = [codexProviderCatalogSummary];

const fallbackCodexReasoningEfforts = ["low", "medium", "high", "xhigh", "max"];

export function ProviderUpsertModal({
  mode,
  provider,
  api,
  catalog,
  standardModels,
  routes = [],
  resources = [],
  loading,
  onClose,
  onSaved,
  onAccountsChanged,
  setLoading,
  setError,
  setNotice,
}: {
  mode: "create" | "edit";
  provider?: Provider;
  api: ApiContext;
  catalog: ProviderCatalogEntry[];
  standardModels: Model[];
  routes?: ModelRoute[];
  resources?: ProviderResource[];
  loading: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
  onAccountsChanged?: () => Promise<void>;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setNotice: (value: string) => void;
}) {
  const editingCodexSubscription = mode === "edit" && resources.some((resource) =>
    resource.provider_id === provider?.id && resource.resource_type === "openai_subscription",
  );
  const directCredentialCatalog = useMemo(
    () => catalog.filter((entry) => entry.id !== codexProviderCatalogSummary.id && entry.type !== "openai_codex"),
    [catalog],
  );
  const selectableProviderCatalog = mode === "create" ? directCredentialCatalog : catalog;
  const availableCategories = useMemo(
    () => catalogModelCategoryOptions(selectableProviderCatalog).filter((item) => mode !== "create" || item.key !== "codex"),
    [mode, selectableProviderCatalog],
  );
  const providerCatalogID = provider?.options?.catalog_id;
  const providerModelCategory = provider?.options?.model_category;
  const initialCategory = editingCodexSubscription ? "codex" : (providerModelCategory || availableCategories.find((item) => item.key !== "all")?.key || "custom");
  const initialEntry = editingCodexSubscription
    ? codexProviderCatalogSummary
    : selectableProviderCatalog.find((entry) => entry.id === providerCatalogID)
      ?? selectableProviderCatalog.find((entry) => providerEntrySupportsCategory(entry, initialCategory))
      ?? selectableProviderCatalog.find((entry) => entry.id === "custom")
      ?? selectableProviderCatalog[0];
  const [modelCategory, setModelCategory] = useState(initialCategory);
  const [catalogID, setCatalogID] = useState(initialEntry?.id ?? "custom");
  const [detail, setDetail] = useState<ProviderCatalogEntry | null>(null);
  const [codexCatalog, setCodexCatalog] = useState<ProviderCatalogEntry | null>(null);
  const [codexCatalogLoading, setCodexCatalogLoading] = useState(false);
  const [codexCatalogError, setCodexCatalogError] = useState("");
  const [catalogQuery, setCatalogQuery] = useState("");
  const [modelQuery, setModelQuery] = useState("");
  const [modelLoading, setModelLoading] = useState(false);
  const [modelError, setModelError] = useState("");
  const [catalogReloadKey, setCatalogReloadKey] = useState(0);
  const [selectedModels, setSelectedModels] = useState<Record<string, boolean>>({});
  const [values, setValues] = useState<Record<string, string>>(() => ({
    id: mode === "edit" ? provider?.id ?? "" : "",
    name: mode === "edit" ? editingCodexSubscription ? "OpenAI Codex" : provider?.name ?? "" : initialEntry?.display_name ?? "",
    type: mode === "edit" ? editingCodexSubscription ? "openai_codex" : provider?.type ?? "openai_compatible" : initialEntry?.type ?? "openai_compatible",
    base_url: mode === "edit" ? editingCodexSubscription ? codexProviderCatalogSummary.base_url ?? "" : provider?.base_url ?? "" : initialEntry?.base_url ?? "",
    api_key: "",
    priority: String(provider?.priority ?? 10),
    status: provider?.status ?? "active",
    healthy: String(provider?.healthy ?? true),
    create_routes: mode === "create" ? "true" : "false",
  }));
  const [credentialMode, setCredentialMode] = useState<ProviderCredentialMode>(editingCodexSubscription ? "account_integration" : "provider_api_key");
  const [accountValues, setAccountValues] = useState<Record<string, string>>(() =>
    providerResourceDraftDefaults({
      provider_id: "",
      name: mode === "edit" ? editingCodexSubscription ? "OpenAI Codex Codex Account" : provider?.name ?? "" : initialEntry?.display_name || initialEntry?.name || "",
      base_url: mode === "edit" ? editingCodexSubscription ? codexProviderCatalogSummary.base_url ?? "" : provider?.base_url ?? "" : initialEntry?.base_url ?? "",
    }),
  );
  const [accountOAuthCallback, setAccountOAuthCallback] = useState("");
  const [accountOAuthStatus, setAccountOAuthStatus] = useState("");
  const [accountOAuthBusy, setAccountOAuthBusy] = useState(false);
  const [accountOAuthCallbackModalOpen, setAccountOAuthCallbackModalOpen] = useState(false);
  const [accountOAuthCallbackModalError, setAccountOAuthCallbackModalError] = useState("");
  const [accountQuotaBusyIDs, setAccountQuotaBusyIDs] = useState<Record<string, boolean>>({});
  const [accountQuotaErrors, setAccountQuotaErrors] = useState<Record<string, string>>({});
  const [accountQuotas, setAccountQuotas] = useState<Record<string, OpenAIAccountQuota>>({});
  const [accountConfirmation, setAccountConfirmation] = useState<ProviderAccountConfirmation | null>(null);
  const [accountConfirmationBusy, setAccountConfirmationBusy] = useState(false);
  const [accountConfirmationError, setAccountConfirmationError] = useState("");
  const [deleteAccountConfirmation, setDeleteAccountConfirmation] = useState("");
  const [selectedAccountID, setSelectedAccountID] = useState(editingCodexSubscription ? "all" : "");
  const [accountCatalogs, setAccountCatalogs] = useState<Record<string, ProviderCatalogEntry>>({});
  const [accountCatalogErrors, setAccountCatalogErrors] = useState<Record<string, string>>({});
  const [accountCatalogLoading, setAccountCatalogLoading] = useState(false);
  const [codexTestValues, setCodexTestValues] = useState({ model: "gpt-5.6-luna", reasoning_effort: "medium", speed: "standard", prompt: "" });
  const [codexTestBusyID, setCodexTestBusyID] = useState("");
  const [codexTestErrors, setCodexTestErrors] = useState<Record<string, string>>({});
  const [codexTestResults, setCodexTestResults] = useState<Record<string, CodexSubscriptionTestResult>>({});
  const [editTab, setEditTab] = useState<ProviderEditTab>(editingCodexSubscription ? "quota" : "settings");
  const [createStep, setCreateStep] = useState(0);
  const createSteps = useMemo(() => providerCreateWizardSteps(), []);
  const lastCreateStep = createSteps.length - 1;
  const accountCallbackURL = useMemo(() => providerAccountOAuthCallbackURL(), []);
  const modalRef = useRef<HTMLFormElement | null>(null);
  const existingRouteModels = useMemo(
    () => new Set(routes.filter((route) => provider && route.provider_id === provider.id).map((route) => route.model_name)),
    [provider, routes],
  );
  const subscriptionResources = useMemo(
    () => resources.filter((resource) =>
      resource.resource_type === "openai_subscription" && (mode === "create" || resource.provider_id === provider?.id),
    ),
    [mode, provider?.id, resources],
  );
  const usesCodexCatalog = credentialMode === "account_integration" || editingCodexSubscription;
  const selectedAccountResources = useMemo(
    () => selectedAccountID === "all"
      ? subscriptionResources
      : subscriptionResources.filter((resource) => resource.id === selectedAccountID),
    [selectedAccountID, subscriptionResources],
  );
  const categoryCatalog = useMemo(
    () => selectableProviderCatalog.filter((entry) => providerEntrySupportsCategory(entry, modelCategory)),
    [modelCategory, selectableProviderCatalog],
  );
  const customCatalogEntry = useMemo(() => buildCustomProviderCatalogEntry(modelCategory, standardModels), [modelCategory, standardModels]);

  useEffect(() => {
    if (credentialMode === "account_integration" || catalogID === codexProviderCatalogSummary.id) return;
    if (catalogID === "custom") return;
    if (categoryCatalog.length === 0) return;
    if (!categoryCatalog.some((entry) => entry.id === catalogID)) {
      selectCatalog(categoryCatalog[0]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelCategory, categoryCatalog.length, credentialMode, catalogID]);

  useEffect(() => {
    const entry = catalogID === "custom" ? customCatalogEntry : catalog.find((item) => item.id === catalogID);
    setModelQuery("");
    setModelError("");
    if (entry && mode === "create") {
      setValues((current) => ({
        ...current,
        name: catalogID === "custom" ? (current.name === initialEntry?.display_name ? "" : current.name) : entry.display_name || entry.name || current.name,
        type: entry.type || current.type || "openai_compatible",
        base_url: catalogID === "custom" ? current.base_url : entry.base_url ?? "",
      }));
    }
    let cancelled = false;
    setDetail(null);
    setSelectedModels({});
    if (catalogID === codexProviderCatalogSummary.id) {
      setModelLoading(false);
      return () => {
        cancelled = true;
      };
    }
    if (catalogID === "custom") {
      if (!values.base_url?.trim()) {
        setDetail(customCatalogEntry);
        setModelLoading(false);
        return () => {
          cancelled = true;
        };
      }
      setModelLoading(true);
      adminFetch(api, "/api/admin/provider-catalog/custom", {
        method: "POST",
        body: JSON.stringify({
          provider_id: mode === "edit" ? provider?.id : "",
          name: values.name,
          type: values.type || "openai_compatible",
          base_url: values.base_url,
          api_key: values.api_key,
          model_category: modelCategory,
        }),
      })
        .then(async (resp) => {
          if (!resp.ok) throw new Error(await readAdminError(resp, tx("加载自定义上游模型")));
          return (await resp.json()) as { data: ProviderCatalogEntry };
        })
        .then((payload) => {
          if (cancelled) return;
          setDetail(payload.data);
          setModelError("");
        })
        .catch((err) => {
          if (cancelled || isAuthExpiredError(err)) return;
          const message = err instanceof Error ? err.message : tx("自定义上游模型加载失败");
          setDetail(customCatalogEntry);
          setModelError(message);
        })
        .finally(() => {
          if (!cancelled) setModelLoading(false);
        });
      return () => {
        cancelled = true;
      };
    }
    setModelLoading(true);
    adminFetch(api, `/api/admin/provider-catalog/${encodeURIComponent(catalogID)}`)
      .then(async (resp) => {
        if (!resp.ok) throw new Error(`provider catalog ${resp.status}`);
        return (await resp.json()) as { data: ProviderCatalogEntry };
      })
      .then((payload) => {
        if (cancelled) return;
        setDetail(payload.data);
        setModelError("");
        if (mode === "create") {
          setValues((current) => ({
            ...current,
            name: payload.data.display_name || payload.data.name || current.name,
            type: payload.data.type || current.type || "openai_compatible",
            base_url: payload.data.base_url ?? "",
          }));
        }
      })
      .catch((err) => {
        if (!cancelled) {
          if (isAuthExpiredError(err)) return;
          const message = err instanceof Error ? err.message : tx("Provider 模板加载失败");
          setModelError(message);
          setError(message);
        }
      })
      .finally(() => {
        if (!cancelled) setModelLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, catalogID, catalogReloadKey, customCatalogEntry, initialEntry?.display_name, mode, modelCategory, provider?.id]);

  useEffect(() => {
    if (!usesCodexCatalog || mode === "edit") return;
    const resource = subscriptionResources.find((item) => item.status === "active") ?? subscriptionResources[0];
    const hasPendingCredentials = Boolean(accountValues.access_token?.trim() && accountValues.account_id?.trim());
    if (!resource && !hasPendingCredentials) {
      setCodexCatalog(null);
      setCodexCatalogError(tx("完成 Codex 账号授权后将从真实账号加载可用模型。"));
      return;
    }
    let cancelled = false;
    setCodexCatalogLoading(true);
    setCodexCatalogError("");
    const path = resource
      ? `/api/admin/provider-catalog/${codexProviderCatalogSummary.id}?resource_id=${encodeURIComponent(resource.id)}`
      : `/api/admin/provider-catalog/${codexProviderCatalogSummary.id}`;
    const request = hasPendingCredentials
      ? {
          method: "POST",
          body: JSON.stringify({
            auth_type: accountValues.auth_type || "oauth",
            access_token: accountValues.access_token,
            refresh_token: accountValues.refresh_token,
            id_token: accountValues.id_token,
            account_id: accountValues.account_id,
            email: accountValues.account_email,
            organization_id: accountValues.organization_id,
            plan_type: accountValues.plan_type,
            token_type: accountValues.token_type,
            expires_at: accountValues.expires_at,
            scopes: accountValues.scopes,
          }),
        }
      : undefined;
    adminFetch(api, path, request)
      .then(async (resp) => {
        if (!resp.ok) throw new Error(await readAdminError(resp, tx("加载 Codex 模型目录")));
        return (await resp.json()) as { data: ProviderCatalogEntry };
      })
      .then((payload) => {
        if (cancelled) return;
        setCodexCatalog(payload.data);
        setCodexCatalogError("");
      })
      .catch((err) => {
        if (cancelled || isAuthExpiredError(err)) return;
        setCodexCatalog(null);
        setCodexCatalogError(err instanceof Error ? err.message : tx("Codex 模型目录加载失败"));
      })
      .finally(() => {
        if (!cancelled) setCodexCatalogLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [
    api,
    credentialMode,
    subscriptionResources,
    usesCodexCatalog,
    accountValues.access_token,
    accountValues.account_id,
    catalogReloadKey,
    mode,
  ]);

  useEffect(() => {
    if (mode !== "edit" || !editingCodexSubscription || selectedAccountResources.length === 0) {
      setAccountCatalogs({});
      setAccountCatalogErrors({});
      return;
    }
    let cancelled = false;
    setAccountCatalogLoading(true);
    setAccountCatalogErrors({});
    void Promise.all(selectedAccountResources.map(async (resource) => {
      try {
        const resp = await adminFetch(
          api,
          `/api/admin/provider-catalog/${codexProviderCatalogSummary.id}?resource_id=${encodeURIComponent(resource.id)}`,
        );
        if (!resp.ok) throw new Error(await readAdminError(resp, tx("加载账号模型目录")));
        const payload = (await resp.json()) as { data: ProviderCatalogEntry };
        if (!cancelled) {
          setAccountCatalogs((current) => ({ ...current, [resource.id]: payload.data }));
        }
      } catch (err) {
        if (cancelled || isAuthExpiredError(err)) return;
        const message = err instanceof Error ? err.message : tx("账号模型目录加载失败");
        setAccountCatalogErrors((current) => ({ ...current, [resource.id]: message }));
      }
    })).finally(() => {
      if (!cancelled) setAccountCatalogLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [api, editingCodexSubscription, mode, selectedAccountResources]);

  useEffect(() => {
    if (mode === "create") {
      modalRef.current?.scrollTo({ top: 0 });
    }
  }, [createStep, mode]);

  useEffect(() => {
    if (mode === "create" && createStep === 3 && catalogID === "custom" && values.base_url?.trim()) {
      setCatalogReloadKey((current) => current + 1);
    }
    // Load custom upstream models when the wizard reaches route confirmation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalogID, createStep, mode]);

  useEffect(() => {
    if (mode !== "edit" || editTab !== "quota") return;
    for (const resource of selectedAccountResources) {
      if (!accountQuotas[resource.id]) void queryAccountQuota(resource);
    }
    // Query when the visible account scope changes; successful results remain cached for manual refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editTab, mode, selectedAccountResources]);

  const selectedAccountCatalog = useMemo(
    () => {
      const catalogs = selectedAccountResources
        .map((resource) => accountCatalogs[resource.id])
        .filter((entry): entry is ProviderCatalogEntry => Boolean(entry));
      return catalogs.length === selectedAccountResources.length ? intersectProviderCatalogs(catalogs) : null;
    },
    [accountCatalogs, selectedAccountResources],
  );
  const effectiveDetail = editingCodexSubscription ? selectedAccountCatalog : usesCodexCatalog ? codexCatalog : detail;
  const effectiveCatalogLoading = editingCodexSubscription ? accountCatalogLoading : usesCodexCatalog ? codexCatalogLoading : modelLoading;
  const effectiveCatalogError = editingCodexSubscription
    ? Object.values(accountCatalogErrors)[0] || ""
    : usesCodexCatalog ? codexCatalogError : modelError;
  const models = useMemo(
    () => (effectiveDetail?.models ?? []).filter((model) => {
      if (modelCategory !== "all" && modelCategoryForCatalog(model) !== modelCategory) return false;
      if (usesCodexCatalog) return true;
      if (catalogID === "custom") return true;
      const canonical = model.canonical_name || canonicalModelNameForUI(model.id, model.display_name);
      return standardModels.some((standard) => canonicalModelNameForUI(standard.name, standard.name) === canonicalModelNameForUI(canonical, canonical));
    }),
    [catalogID, effectiveDetail, modelCategory, standardModels, usesCodexCatalog],
  );
  useEffect(() => {
    if (mode !== "create" || !effectiveDetail || values.create_routes !== "true") return;
    const nextSelected: Record<string, boolean> = {};
    for (const model of models) {
      nextSelected[model.id] = true;
    }
    setSelectedModels(nextSelected);
  }, [effectiveDetail, mode, models, values.create_routes]);
  const filteredCatalog = useMemo(() => {
    const normalized = catalogQuery.trim().toLowerCase();
    const entries = categoryCatalog;
    if (!normalized) return entries;
    return entries.filter((entry) =>
      [
        entry.id,
        entry.name,
        entry.display_name,
      ].filter(Boolean).join(" ").toLowerCase().includes(normalized),
    );
  }, [categoryCatalog, catalogQuery]);
  const filteredModels = useMemo(() => {
    const normalized = modelQuery.trim().toLowerCase();
    if (!normalized) return models.slice(0, 80);
    return models
      .filter((model) => JSON.stringify(model).toLowerCase().includes(normalized))
      .slice(0, 80);
  }, [models, modelQuery]);
  const selectedModelIDs = Object.entries(selectedModels)
    .filter(([, selected]) => selected)
    .map(([id]) => id);
  const autoRouteEnabled = values.create_routes === "true";
  const selectedRouteCount = autoRouteEnabled ? selectedModelIDs.length : 0;
  const selectedEntry = usesCodexCatalog
    ? codexCatalog ?? codexProviderCatalogSummary
    : detail ?? (catalogID === "custom" ? customCatalogEntry : catalog.find((entry) => entry.id === catalogID));
  const showProviderCatalog = (mode === "edit" && !usesCodexCatalog) || (mode === "create" && createStep === 1 && credentialMode !== "account_integration");
  const providerBodyClassName = !showProviderCatalog ? "provider-modal-body provider-wizard-single" : "provider-modal-body";
  const accountRuntimeFields = useMemo(() => providerCreateAccountRuntimeFields(), []);
  const accountManualTokenFields = useMemo(() => providerCreateAccountManualTokenFields(), []);
  const accountTokenSummary = useMemo(() => providerAccountTokenSummary(accountValues), [accountValues]);
  const accountResourceNameConflict = useMemo(() => {
    const normalized = accountValues.name?.trim().toLocaleLowerCase();
    if (!normalized) return false;
    return resources.some((resource) => resource.name.trim().toLocaleLowerCase() === normalized);
  }, [accountValues.name, resources]);
  const codexTestModels = selectedAccountCatalog?.models ?? [];
  const selectedCodexTestModel = codexTestModels.find((model) => model.id === codexTestValues.model);
  const codexReasoningEfforts = selectedCodexTestModel?.metadata?.supported_reasoning_levels
    ?.split(",")
    .map((value) => value.trim())
    .filter(Boolean) ?? fallbackCodexReasoningEfforts;

  useEffect(() => {
    if (codexTestModels.length === 0) return;
    setCodexTestValues((current) => {
      const nextModel = codexTestModels.some((model) => model.id === current.model)
        ? current.model
        : codexTestModels.find((model) => model.id === "gpt-5.6-luna")?.id ?? codexTestModels[0].id;
      const model = codexTestModels.find((item) => item.id === nextModel);
      const efforts = model?.metadata?.supported_reasoning_levels?.split(",").filter(Boolean) ?? fallbackCodexReasoningEfforts;
      const nextEffort = efforts.includes(current.reasoning_effort)
        ? current.reasoning_effort
        : model?.metadata?.default_reasoning_level || efforts[0] || "medium";
      if (nextModel === current.model && nextEffort === current.reasoning_effort) return current;
      return { ...current, model: nextModel, reasoning_effort: nextEffort };
    });
  }, [codexTestModels, codexTestValues.model]);

  useEffect(() => {
    if (mode !== "create" || !hasPendingProviderAccountOAuthResult()) return;
    selectCredentialMode("account_integration");
    setCreateStep(2);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  useEffect(() => {
    if (mode !== "create" || credentialMode !== "account_integration") return;
    const pending = consumePendingProviderAccountOAuthResult();
    if (!pending) return;
    void applyProviderAccountOAuthResult(pending, tx("已从回调 URL 自动回填账号 Token。"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, credentialMode]);

  useEffect(() => {
    if (mode !== "create" || credentialMode !== "account_integration" || catalogID === codexProviderCatalogSummary.id) return;
    setModelCategory("codex");
    selectCatalog(codexProviderCatalogSummary);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [credentialMode, mode, catalogID]);

  function update(key: string, value: string) {
    const previousProviderName = values.name;
    const previousBaseURL = values.base_url;
    setValues((current) => ({ ...current, [key]: value }));
    if (mode !== "create") return;
    if (key === "name") {
      setAccountValues((current) => {
        if (current.name && current.name !== defaultProviderResourceName(previousProviderName)) return current;
        return { ...current, name: defaultProviderResourceName(value) };
      });
    }
    if (key === "base_url") {
      setAccountValues((current) => {
        if (current.base_url && current.base_url !== previousBaseURL) return current;
        return {
          ...current,
          base_url: value || (credentialMode === "account_integration" ? codexProviderCatalogSummary.base_url || "" : "https://api.openai.com/v1"),
        };
      });
    }
  }

  function updateAccountValue(key: string, value: string) {
    setAccountValues((current) => ({ ...current, [key]: value }));
  }

  async function applyProviderAccountOAuthResult(result: ProviderAccountOAuthResult, message: string) {
    if (result.error) {
      const errorMessage = `${tx("账号授权失败")}：${result.error}`;
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
      clearPendingProviderAccountOAuthSession();
      return;
    }
    if (result.authorization_code && !result.access_token && !result.refresh_token && !result.id_token) {
      await exchangeProviderAccountAuthorizationCode(result, message);
      return;
    }
    if (!result.access_token && !result.refresh_token && !result.id_token) {
      setAccountOAuthStatus(tx("未在回调结果中识别到 Token。"));
      setError(tx("未在回调结果中识别到 Token。"));
      return;
    }
    setAccountValues((current) => ({
      ...current,
      resource_type: "openai_subscription",
      auth_type: "oauth",
      access_token: result.access_token || current.access_token || "",
      refresh_token: result.refresh_token || current.refresh_token || "",
      id_token: result.id_token || current.id_token || "",
      account_email: result.account_email || current.account_email || "",
      account_id: result.account_id || current.account_id || "",
      organization_id: result.organization_id || current.organization_id || "",
      plan_type: result.plan_type || current.plan_type || "",
      token_type: result.token_type || current.token_type || "",
      expires_at: result.expires_at || current.expires_at || "",
      scopes: result.scopes || current.scopes || "",
    }));
    setAccountOAuthStatus(message);
    setError("");
  }

  function parseAccountOAuthCallback(raw: string) {
    setAccountOAuthCallback(raw);
    const result = parseProviderAccountOAuthResult(raw, true);
    if (!result) return;
    void applyProviderAccountOAuthResult(result, tx("已从粘贴的回调结果回填账号 Token。"));
  }

  function parseAccountOAuthCallbackNow() {
    const result = parseProviderAccountOAuthResult(accountOAuthCallback, true);
    if (!result) {
      setAccountOAuthStatus(tx("未在回调结果中识别到 Token。"));
      setError(tx("未在回调结果中识别到 Token。"));
      return;
    }
    void applyProviderAccountOAuthResult(result, tx("已从粘贴的回调结果回填账号 Token。"));
  }

  function confirmProviderAccountOAuthCallback() {
    const result = parseProviderAccountOAuthResult(accountOAuthCallback, true);
    if (!result) {
      const message = tx("请粘贴登录后浏览器地址栏中的完整 localhost 回调地址。");
      setAccountOAuthCallbackModalError(message);
      setAccountOAuthStatus(message);
      return;
    }
    setAccountOAuthCallbackModalError("");
    setAccountOAuthCallbackModalOpen(false);
    void applyProviderAccountOAuthResult(result, tx("已从粘贴的回调结果回填账号 Token。"));
  }

  async function exchangeProviderAccountAuthorizationCode(result: ProviderAccountOAuthResult, message: string) {
    const pendingSession = readPendingProviderAccountOAuthSession();
    const sessionID = result.session_id || pendingSession?.session_id || "";
    const state = result.state || pendingSession?.state || "";
    if (!sessionID || !state || !result.authorization_code) {
      setAccountOAuthStatus(tx("授权回调缺少会话信息，请重新打开授权。"));
      setError(tx("授权回调缺少会话信息，请重新打开授权。"));
      return;
    }
    setAccountOAuthBusy(true);
    setAccountOAuthStatus(tx("正在换取账号 Token..."));
    try {
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/openai/exchange-code", {
        method: "POST",
        body: JSON.stringify({
          session_id: sessionID,
          state,
          code: result.authorization_code,
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("账号授权换取 Token")));
      const tokenInfo = (await resp.json()) as ProviderAccountOAuthResult;
      clearPendingProviderAccountOAuthSession();
      await applyProviderAccountOAuthResult(tokenInfo, message);
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("账号授权换取 Token 失败");
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setAccountOAuthBusy(false);
    }
  }

  async function openProviderAccountAuthorization() {
    try {
      setAccountOAuthBusy(true);
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/openai/generate-auth-url", {
        method: "POST",
        body: JSON.stringify({ return_url: accountCallbackURL }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("生成账号授权地址")));
      const generated = (await resp.json()) as ProviderAccountOAuthGenerateResponse;
      savePendingProviderAccountOAuthSession({ session_id: generated.session_id, state: generated.state });
      window.open(generated.auth_url, "_blank", "noopener,noreferrer");
      setAccountOAuthCallback("");
      setAccountOAuthCallbackModalError("");
      setAccountOAuthCallbackModalOpen(true);
      setAccountOAuthStatus(tx("已打开 OpenAI/Codex 授权页。授权后请复制浏览器地址栏中的完整 localhost callback URL，并粘贴到回调结果。"));
      setError("");
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("生成账号授权地址失败");
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setAccountOAuthBusy(false);
    }
  }

  async function copyProviderAccountCallbackURL() {
    try {
      await navigator.clipboard.writeText(openAIAccountOAuthRedirectURI);
      setAccountOAuthStatus(tx("已复制 OpenAI 固定回调地址。"));
    } catch {
      setAccountOAuthCallback(openAIAccountOAuthRedirectURI);
      setAccountOAuthStatus(openAIAccountOAuthRedirectURI);
    }
  }

  async function queryAccountQuota(resource: ProviderResource) {
    setAccountQuotaBusyIDs((current) => ({ ...current, [resource.id]: true }));
    setAccountQuotaErrors((current) => {
      const next = { ...current };
      delete next[resource.id];
      return next;
    });
    try {
      const resp = await adminFetch(api, `/api/admin/provider-resources/${resource.id}/quota`);
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("查询订阅额度")));
      const quota = (await resp.json()) as OpenAIAccountQuota;
      setAccountQuotas((current) => ({ ...current, [resource.id]: quota }));
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setAccountQuotaErrors((current) => ({
        ...current,
        [resource.id]: err instanceof Error ? err.message : tx("查询订阅额度失败"),
      }));
    } finally {
      setAccountQuotaBusyIDs((current) => ({ ...current, [resource.id]: false }));
    }
  }

  async function testCodexSubscription() {
    if (selectedAccountResources.length === 0) {
      setCodexTestErrors({ selection: tx("请选择要测试的 Codex 账号") });
      return;
    }
    setCodexTestBusyID(selectedAccountID);
    setCodexTestErrors({});
    await Promise.all(selectedAccountResources.map(async (resource) => {
      try {
        const resp = await adminFetch(api, `/api/admin/provider-resources/${resource.id}/test`, {
          method: "POST",
          body: JSON.stringify(codexTestValues),
        });
        if (!resp.ok) throw new Error(await readAdminError(resp, tx("测试 Codex Subscription")));
        const result = (await resp.json()) as CodexSubscriptionTestResult;
        setCodexTestResults((current) => ({ ...current, [resource.id]: result }));
      } catch (err) {
        if (isAuthExpiredError(err)) return;
        const message = err instanceof Error ? err.message : tx("Codex Subscription 测试失败");
        setCodexTestErrors((current) => ({ ...current, [resource.id]: message }));
      }
    }));
    setCodexTestBusyID("");
  }

  function selectCredentialMode(nextMode: ProviderCredentialMode) {
    setCredentialMode(nextMode);
    if (nextMode === "account_integration") {
      setAccountValues((current) => ({
        ...current,
        resource_type: "openai_subscription",
        auth_type: "oauth",
      }));
      setModelCategory("codex");
      setCatalogQuery("");
      setModelQuery("");
      setSelectedModels({});
      selectCatalog(codexProviderCatalogSummary);
      return;
    }
    if (modelCategory === "codex" || catalogID === codexProviderCatalogSummary.id) {
      selectCategory(availableCategories[0]?.key ?? "custom");
    }
  }

  function syncAccountDefaults(providerName: string, baseURL?: string) {
    if (mode !== "create") return;
    setAccountValues((current) => ({
      ...current,
      name: defaultProviderResourceName(providerName),
      base_url: baseURL || codexProviderCatalogSummary.base_url || "",
    }));
  }

  function requestAccountConfirmation(action: ProviderAccountConfirmation["action"], resource: ProviderResource) {
    setAccountConfirmation({ action, resource });
    setAccountConfirmationError("");
    setDeleteAccountConfirmation("");
  }

  function closeAccountConfirmation() {
    if (accountConfirmationBusy) return;
    setAccountConfirmation(null);
    setAccountConfirmationError("");
    setDeleteAccountConfirmation("");
  }

  async function updateAccountStatus(resource: ProviderResource, action: "enable" | "disable") {
    setLoading(true);
    setAccountConfirmationBusy(true);
    setAccountConfirmationError("");
    setError("");
    setNotice("");
    try {
      const resp = await adminFetch(api, "/api/admin/provider-resources/bulk", {
        method: "POST",
        body: JSON.stringify({ action, ids: [resource.id] }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx(action === "disable" ? "禁用账号" : "启用账号")));
      const result = (await resp.json()) as { success?: number; errors?: string[] };
      if (result.success !== 1) {
        throw new Error(result.errors?.[0] || tx(action === "disable" ? "禁用账号失败" : "启用账号失败"));
      }
      setNotice(`${providerResourceAccountLabel(resource)} ${tx(action === "disable" ? "已禁用" : "已启用")}`);
      setAccountConfirmation(null);
      await (onAccountsChanged ?? onSaved)();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setAccountConfirmationError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setAccountConfirmationBusy(false);
      setLoading(false);
    }
  }

  async function deleteAccount(resource: ProviderResource) {
    if (deleteAccountConfirmation !== deleteAccountConfirmationPhrase) return;
    setLoading(true);
    setAccountConfirmationBusy(true);
    setAccountConfirmationError("");
    setError("");
    setNotice("");
    try {
      const resp = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(resource.id)}`, {
        method: "DELETE",
      });
      if (!resp.ok && resp.status !== 204) throw new Error(await readAdminError(resp, tx("删除账号")));
      setNotice(`${providerResourceAccountLabel(resource)} ${tx("已彻底删除")}`);
      setAccountConfirmation(null);
      await (onAccountsChanged ?? onSaved)();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setAccountConfirmationError(err instanceof Error ? err.message : tx("删除账号失败"));
    } finally {
      setAccountConfirmationBusy(false);
      setLoading(false);
    }
  }

  function selectCategory(category: string) {
    setModelCategory(category);
    setCatalogQuery("");
    setModelQuery("");
    setSelectedModels({});
    const nextEntry = selectableProviderCatalog.find((entry) => providerEntrySupportsCategory(entry, category));
    if (nextEntry) {
      selectCatalog(nextEntry);
    } else {
      selectCustomCatalog();
    }
  }

  function selectCatalog(entry: ProviderCatalogEntry) {
    const nextName = entry.display_name || entry.name || values.name;
    setCatalogID(entry.id);
    setCatalogReloadKey((current) => current + 1);
    setDetail(null);
    setSelectedModels({});
    setModelQuery("");
    setModelError("");
    setValues((current) => ({
      ...current,
      name: mode === "create" ? entry.display_name || entry.name || current.name : current.name,
      type: entry.type || current.type || "openai_compatible",
      base_url: mode === "create" ? entry.base_url ?? "" : current.base_url,
    }));
    syncAccountDefaults(nextName, entry.base_url);
  }

  function selectCustomCatalog() {
    setCatalogID("custom");
    setCatalogReloadKey((current) => current + 1);
    setDetail(customCatalogEntry);
    setSelectedModels({});
    setModelQuery("");
    setModelError("");
    setValues((current) => ({
      ...current,
      id: mode === "create" ? "" : current.id,
      name: mode === "create" ? "" : current.name,
      type: current.type || "openai_compatible",
      base_url: mode === "create" ? "" : current.base_url,
    }));
    syncAccountDefaults(values.name || "Provider", "");
  }

  function reloadSelectedCatalog() {
    if (catalogID === "custom") {
      setCatalogReloadKey((current) => current + 1);
      setModelError("");
      return;
    }
    if (selectedEntry) selectCatalog(selectedEntry);
  }

  function canContinueCreateStep(targetStep = createStep) {
    if (mode !== "create") return true;
    if (targetStep === 0) return Boolean(credentialMode);
    if (targetStep === 1) {
      return Boolean(selectedEntry && values.name?.trim());
    }
    if (targetStep === 2 && credentialMode === "account_integration") return providerAccountResourceReady(accountValues);
    return true;
  }

  function validateCreateStep(targetStep = createStep) {
    if (mode !== "create") return true;
    if (targetStep === 0 && !credentialMode) {
      setError(tx("请先选择一种接入方式。"));
      return false;
    }
    if (targetStep === 1 && !selectedEntry) {
      setError(tx("请先选择一个渠道商。"));
      return false;
    }
    if (targetStep === 1 && !values.name?.trim()) {
      setError(tx(credentialMode === "account_integration" ? "请填写通道名称。" : "请填写渠道名称。"));
      return false;
    }
    if (targetStep === 2 && credentialMode === "account_integration") {
      if (!accountValues.name?.trim()) {
        setError(tx("请填写账号资源名称。"));
        return false;
      }
      if (accountResourceNameConflict) {
        setError(tx("账号资源名称已存在，请使用唯一名称。"));
        return false;
      }
      try {
        assertProviderAccountResourceReady(accountValues);
      } catch (err) {
        setError(err instanceof Error ? err.message : tx("账号资源配置不完整"));
        return false;
      }
    }
    setError("");
    return true;
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mode === "create" && createStep < lastCreateStep) {
      if (!validateCreateStep(createStep)) return;
      setCreateStep((current) => Math.min(current + 1, lastCreateStep));
      return;
    }
    setLoading(true);
    setError("");
    setNotice("");
    try {
      if (mode === "create" && credentialMode === "account_integration") {
        if (accountResourceNameConflict) throw new Error(tx("账号资源名称已存在，请使用唯一名称。"));
        assertProviderAccountResourceReady(accountValues);
      }
      const payload = (mode === "edit" ? providerUpdatePayload : providerPayload)({
        ...values,
        api_key: mode === "create" && credentialMode !== "provider_api_key" ? "" : values.api_key,
        create_routes: autoRouteEnabled && selectedModelIDs.length > 0 ? "true" : "false",
        catalog_id: catalogID,
        model_category: modelCategory,
        selected_models: selectedModelIDs.length > 0 ? selectedModelIDs.join(",") : "",
        custom_models: catalogID === "custom" && effectiveDetail?.models ? JSON.stringify(effectiveDetail.models) : "",
      });
      const resp = await adminFetch(api, mode === "edit" && provider ? `/api/admin/providers/${provider.id}` : "/api/admin/providers", {
        method: mode === "edit" ? "PATCH" : "POST",
        body: JSON.stringify(payload),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, `${mode === "edit" ? tx("更新") : tx("创建")} ${tx("Provider 渠道")}`));
      const result = (await resp.json()) as { created_routes?: number; provider?: Provider };
      let accountResourceCreated = false;
      if (mode === "create" && credentialMode === "account_integration") {
        const payloadProviderID = typeof payload.id === "string" ? payload.id : "";
        const providerID = result.provider?.id || payloadProviderID || values.id;
        if (!providerID) throw new Error(tx("Provider 已创建，但无法确认账号资源所属 Provider。"));
        const resourceValues = {
          ...accountValues,
          provider_id: providerID,
          name: accountValues.name?.trim() || defaultProviderResourceName(result.provider?.name || values.name || providerID),
          base_url: accountValues.base_url?.trim() || values.base_url || codexProviderCatalogSummary.base_url || "",
        };
        const resourceResp = await adminFetch(api, "/api/admin/provider-resources", {
          method: "POST",
          body: JSON.stringify(providerResourcePayload(resourceValues)),
        });
        if (!resourceResp.ok) throw new Error(await readAdminError(resourceResp, tx("创建账号资源")));
        accountResourceCreated = true;
      }
      const routed = result.created_routes ?? 0;
      setNotice(`${tx("Provider 已")}${tx(mode === "edit" ? "更新" : "新增")}${accountResourceCreated ? `，${tx("已创建账号资源")}` : ""}${routed ? `，${tx("创建")} ${countWithUnit(routed, `条${modelCategoryLabel(modelCategory)}路由`, `${modelCategoryLabel(modelCategory)} route`, `${modelCategoryLabel(modelCategory)} ルート`)}` : ""}`);
      await onSaved();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className={mode === "create" ? "modal provider-modal provider-wizard-modal" : "modal provider-modal"} ref={modalRef} onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx(mode === "edit" ? "编辑" : "新增")}</p>
            <h2>{tx("Provider 渠道")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        {mode === "create" ? (
          <div className="wizard-stepper provider-wizard-stepper" aria-label={tx("创建 Provider 步骤")}>
            {createSteps.map((item, index) => {
              const Icon = item.icon;
              const title = providerCreateWizardStepTitle(item.title, credentialMode);
              return (
                <button
                  aria-current={createStep === index ? "step" : undefined}
                  className={createStep === index ? "wizard-step active" : index < createStep ? "wizard-step done" : "wizard-step"}
                  disabled={index > createStep || loading}
                  key={item.title}
                  onClick={() => setCreateStep(index)}
                  type="button"
                >
                  <span><Icon size={14} /></span>
                  <strong>{tx(title)}</strong>
                </button>
              );
            })}
          </div>
        ) : null}
        <div className={providerBodyClassName}>
          {showProviderCatalog ? (
            <section className="provider-catalog-pane">
            <div className="provider-catalog-head">
              <strong>{tx("模型类型")}</strong>
              <span>{countWithLabel(availableCategories.length, "类")}</span>
            </div>
            <div className="provider-category-list">
              {availableCategories.map((category) => (
                <button
                  className={category.key === modelCategory ? "provider-category-item active" : "provider-category-item"}
                  key={category.key}
                  onClick={() => selectCategory(category.key)}
                  type="button"
                >
                  <strong>{tx(category.label)}</strong>
                  <span>{countWithLabel(category.count, "个模型")}</span>
                </button>
              ))}
            </div>

            <div className="provider-catalog-head provider-catalog-subhead">
              <strong>{tx("渠道商")}</strong>
              <span>{filteredCatalog.length}/{categoryCatalog.length}</span>
            </div>
            <button
              className={catalogID === "custom" ? "custom-provider-button active" : "custom-provider-button"}
              onClick={selectCustomCatalog}
              type="button"
            >
              <Plus size={14} />
              <span>{tx("自定义渠道商")}</span>
              <em>{modelCategoryLabel(modelCategory)} · {tx("按 Base URL 加载上游模型")}</em>
            </button>
            <div className="provider-template-search">
              <Search size={14} />
              <input
                value={catalogQuery}
                onChange={(event) => setCatalogQuery(event.target.value)}
                placeholder={tx("搜索渠道商名称或 ID")}
              />
            </div>
            <div className="provider-catalog-list compact">
              {filteredCatalog.length === 0 ? (
                <div className="empty compact-empty">
                  <span>{tx("没有匹配的渠道商")}</span>
                  <button className="secondary-button" onClick={selectCustomCatalog} type="button">{tx("使用自定义渠道商")}</button>
                </div>
              ) : filteredCatalog.map((entry) => (
                <button
                  className={entry.id === catalogID ? "catalog-item active" : "catalog-item"}
                  key={entry.id}
                  onClick={() => selectCatalog(entry)}
                  type="button"
                >
                  <strong>{entry.display_name || entry.name}</strong>
                  <span>{providerTypeLabel(entry.type)} · {countWithLabel(providerEntryCategoryCount(entry, modelCategory), "个模型")}</span>
                </button>
              ))}
            </div>
          </section>
          ) : null}

          <section className="provider-config-pane">
            {mode === "edit" && editingCodexSubscription ? (
              <div className="provider-account-selector">
                <div>
                  <strong>{tx("账号列表")}</strong>
                  <span>{tx("额度、真实测试和模型映射均使用这里选择的账号。")}</span>
                </div>
                <select
                  aria-label={tx("账号列表")}
                  value={selectedAccountID}
                  onChange={(event) => {
                    setSelectedAccountID(event.target.value);
                    setModelQuery("");
                    setSelectedModels({});
                  }}
                >
                  <option value="all">{tx("全部账号")}（{subscriptionResources.length}）</option>
                  {subscriptionResources.map((resource) => (
                    <option key={resource.id} value={resource.id}>
                      {resource.name} · {providerResourceAccountLabel(resource)}
                      {resource.status !== "active" ? ` · ${tx("已停用")}` : ""}
                    </option>
                  ))}
                </select>
              </div>
            ) : mode === "edit" || createStep > 0 ? (
              <div className="provider-selected-summary">
                <strong>{modelCategoryLabel(modelCategory)}</strong>
                {credentialMode === "account_integration" ? (
                  <select
                    aria-label={tx("账号池通道")}
                    className="provider-account-channel-select"
                    value={catalogID}
                    onChange={(event) => {
                      const entry = accountProviderCatalogOptions.find((item) => item.id === event.target.value);
                      if (entry) selectCatalog(entry);
                    }}
                  >
                    {accountProviderCatalogOptions.map((entry) => (
                      <option key={entry.id} value={entry.id}>{entry.display_name || entry.name}</option>
                    ))}
                  </select>
                ) : (
                  <span>{selectedEntry?.display_name || selectedEntry?.name || tx("请选择渠道商")}</span>
                )}
                <em>{providerTypeLabel(selectedEntry?.type || values.type || "openai_compatible")}</em>
              </div>
            ) : null}
            {mode === "edit" ? (
              <div className="provider-editor-tabs" role="tablist" aria-label={tx("Provider 编辑区")}>
                {subscriptionResources.length > 0 ? (
                  <>
                    <button
                      aria-selected={editTab === "quota"}
                      className={editTab === "quota" ? "active" : ""}
                      onClick={() => setEditTab("quota")}
                      role="tab"
                      type="button"
                    >
                      {tx("订阅额度")}
                    </button>
                    <button
                      aria-selected={editTab === "test"}
                      className={editTab === "test" ? "active" : ""}
                      onClick={() => setEditTab("test")}
                      role="tab"
                      type="button"
                    >
                      {tx("真实请求测试")}
                    </button>
                  </>
                ) : null}
                <button
                  aria-selected={editTab === "settings"}
                  className={editTab === "settings" ? "active" : ""}
                  onClick={() => setEditTab("settings")}
                  role="tab"
                  type="button"
                >
                  {tx("基础配置")}
                </button>
                <button
                  aria-selected={editTab === "models"}
                  className={editTab === "models" ? "active" : ""}
                  onClick={() => setEditTab("models")}
                  role="tab"
                  type="button"
                >
                  {tx("模型映射")}
                </button>
              </div>
            ) : null}
            {mode === "create" && createStep === 0 ? (
              <section className="provider-wizard-panel provider-access-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("选择接入方式")}</h3>
                  <p>{tx("先告诉 TokenHub 你手里有什么：上游 API Key、OpenAI 账号资源，或者只是先占位建路由。")}</p>
                </div>
                <div className="provider-access-options" role="radiogroup" aria-label={tx("选择接入方式")}>
                  {providerCredentialOptions().map((option) => {
                    const Icon = option.icon;
                    const active = credentialMode === option.key;
                    return (
                      <button
                        aria-checked={active}
                        className={active ? "provider-access-card active" : "provider-access-card"}
                        key={option.key}
                        onClick={() => selectCredentialMode(option.key)}
                        role="radio"
                        type="button"
                      >
                        <span><Icon size={18} /></span>
                        <strong>{tx(option.label)}</strong>
                        <em>{tx(option.description)}</em>
                        {option.key === "account_integration" ? <small>{tx("账号资源池会选择 Codex Subscription 适配器，下一步确认 Codex Backend 地址和账号凭据。")}</small> : null}
                      </button>
                    );
                  })}
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 1 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx(credentialMode === "account_integration" ? "确认账号通道和基础信息" : "选择渠道和基础信息")}</h3>
                  <p>{tx(credentialMode === "account_integration" ? "账号资源池已为你选好默认通道。这里通常只确认 Base URL；账号走企业代理时再修改。" : "选择上游渠道商模板，TokenHub 会带出类型、Base URL 和可映射模型。")}</p>
                </div>
                {credentialMode === "account_integration" ? (
                  <div className="provider-account-channel-note">
                    <strong>{tx("推荐通道")}</strong>
                    <span>{tx("默认通道只负责协议与 Base URL，真实账号 Token 会在下一步保存为账号资源。")}</span>
                  </div>
                ) : null}
                {!showProviderCatalog ? (
                  <div className="wizard-review-grid provider-create-review">
                    <ReviewItem label={credentialMode === "account_integration" ? "模型协议" : "模型类型"} value={modelCategoryLabel(modelCategory)} />
                    <ReviewItem label={credentialMode === "account_integration" ? "默认通道" : "渠道商"} value={selectedEntry?.display_name || selectedEntry?.name || "-"} />
                    <ReviewItem label={credentialMode === "account_integration" ? "兼容协议" : "渠道商类型"} value={providerTypeLabel(selectedEntry?.type || values.type || "openai_compatible")} />
                    <ReviewItem label="可映射模型" value={effectiveDetail ? `${models.length}/${effectiveDetail.models_count}` : codexCatalogError || tx("加载中")} />
                  </div>
                ) : null}
              </section>
            ) : null}
            {mode === "create" && createStep === 2 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx(credentialMode === "account_integration" ? "账号授权" : credentialMode === "provider_api_key" ? "直接 API Key" : "稍后配置")}</h3>
                  <p>{tx(
                    credentialMode === "account_integration"
                      ? "先完成账号授权回填；TokenHub 会把回填的 Token 保存为账号资源。"
                      : credentialMode === "provider_api_key"
                        ? "把上游 Key 保存到 Provider，适合单账号或兼容 API。"
                        : "保存后不会写入上游凭据，可稍后通过编辑 Provider 或账号集成补齐。",
                  )}</p>
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 3 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("确认路由策略")}</h3>
                  <p>{tx("选择是否自动创建默认路由，并确认要映射到标准模型目录的上游模型。")}</p>
                </div>
                <div className="wizard-review-grid provider-create-review">
                  <ReviewItem label="渠道商" value={values.name || selectedEntry?.display_name || selectedEntry?.name || "-"} />
                  <ReviewItem label="凭据方式" value={providerCredentialModeLabel(credentialMode)} />
                  <ReviewItem label="自动路由" value={autoRouteEnabled ? tx("开启") : tx("关闭开关")} />
                  <ReviewItem label="已选模型" value={selectedRouteCount ? String(selectedRouteCount) : tx("无")} />
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 2 ? (
              <section className="provider-credential-panel">
                {credentialMode === "provider_api_key" ? (
                  <div className="provider-direct-key-fields">
                    <label className="field">
                      <span>API Key</span>
                      <input value={values.api_key ?? ""} type="password" onChange={(event) => update("api_key", event.target.value)} />
                    </label>
                  </div>
                ) : credentialMode === "account_integration" ? (
                  <div className="provider-account-inline">
                    <div className="provider-account-inline-head">
                      <strong>{tx("账号授权")}</strong>
                      <span>{tx("使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。")}</span>
                    </div>
                    <div className="provider-account-auth-grid">
                      <label className="field provider-account-auth-wide">
                        <span>{tx("账号资源名称")}</span>
                        <input
                          aria-invalid={accountResourceNameConflict}
                          value={accountValues.name ?? ""}
                          onChange={(event) => updateAccountValue("name", event.target.value)}
                          required
                        />
                        <small className={accountResourceNameConflict ? "provider-account-name-error" : undefined}>
                          {tx(accountResourceNameConflict ? "账号资源名称已存在，请使用唯一名称。" : "账号资源名称需全局唯一，用于在资源池中区分账号。")}
                        </small>
                      </label>
                      <label className="field provider-account-auth-wide">
                        <span>{tx("OpenAI/Codex 授权")}</span>
                        <div className="field-action-row">
                          <input readOnly value={openAIAccountOAuthRedirectURI} />
                          <button className="secondary-button" onClick={openProviderAccountAuthorization} type="button" disabled={accountOAuthBusy}>
                            <Send size={14} />
                            {tx(accountOAuthBusy ? "授权中" : "打开授权")}
                          </button>
                        </div>
                        <small>{tx("点击后由后端生成授权地址。OpenAI 固定回调到 localhost:1455；无需该端口实际启动服务。")}</small>
                      </label>
                      <label className="field provider-account-auth-wide">
                        <span>{tx("回调结果")}</span>
                        <textarea
                          value={accountOAuthCallback}
                          onChange={(event) => parseAccountOAuthCallback(event.target.value)}
                          placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                        />
                        <small>{tx("授权完成后，即使 localhost 页面显示无法访问，也请复制地址栏中的完整 callback URL 粘贴到这里。")}</small>
                      </label>
                      <div className="provider-account-auth-actions">
                        <button className="secondary-button" onClick={parseAccountOAuthCallbackNow} type="button">
                          <Check size={14} />
                          {tx("解析回填")}
                        </button>
                        <button className="secondary-button" onClick={copyProviderAccountCallbackURL} type="button">
                          <Copy size={14} />
                          {tx("复制固定回调地址")}
                        </button>
                        <div className={accountTokenSummary.ready ? "provider-account-token-status ready" : "provider-account-token-status"}>
                          {accountTokenSummary.ready ? <Check size={15} /> : <AlertCircle size={15} />}
                          <span>{tx(accountTokenSummary.ready ? "已回填账号 Token" : "等待授权回填")}</span>
                          {accountTokenSummary.items.map((item) => <em key={item}>{tx(item)}</em>)}
                        </div>
                      </div>
                    </div>
                    {accountOAuthStatus ? <p className="provider-credential-note">{accountOAuthStatus}</p> : null}
                    <details className="provider-account-runtime">
                      <summary>
                        <strong>{tx("资源调度")}</strong>
                        <span>{tx("一般无需修改；展开后可配置 Base URL、优先级、权重和限流。")}</span>
                      </summary>
                      <div className="provider-account-fields compact">
                        {accountRuntimeFields.filter((field) => field.visible?.(accountValues) ?? true).map((field) => (
                          <ProviderInlineField
                            key={field.key}
                            field={field}
                            value={accountValues[field.key] ?? ""}
                            values={accountValues}
                            onChange={(value) => updateAccountValue(field.key, value)}
                          />
                        ))}
                      </div>
                    </details>
                    <details className="provider-account-advanced">
                      <summary>{tx("高级：手动粘贴 Token")}</summary>
                      <p>{tx("只有在授权回填不可用时使用；保存后 Token 不会再次显示。")}</p>
                      <div className="provider-account-fields">
                        {accountManualTokenFields.filter((field) => field.visible?.(accountValues) ?? true).map((field) => (
                          <ProviderInlineField
                            key={field.key}
                            field={field}
                            value={accountValues[field.key] ?? ""}
                            values={accountValues}
                            onChange={(value) => updateAccountValue(field.key, value)}
                          />
                        ))}
                      </div>
                    </details>
                  </div>
                ) : (
                  <p className="provider-credential-note">
                    {tx("保存后不会写入上游凭据，可稍后通过编辑 Provider 或账号集成补齐。")}
                  </p>
                )}
              </section>
            ) : null}
            {mode === "edit" && editTab === "quota" && subscriptionResources.length > 0 ? (
              <section className="provider-quota-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("订阅额度")}</h3>
                  <p>{tx("实时查询 ChatGPT/Codex 套餐用量和重置时间；不会显示虚构的美元余额。")}</p>
                </div>
                <div className="provider-quota-list">
                  {selectedAccountResources.map((resource) => {
                    const quota = accountQuotas[resource.id];
                    const primary = quota?.rate_limit?.primary_window;
                    const secondary = quota?.rate_limit?.secondary_window;
                    const accountEmail = providerResourceAccountLabel(resource);
                    const quotaStatus = quota?.rate_limit?.limit_reached ? "已达上限" : quota?.rate_limit?.allowed ? "可用" : "不可用";
                    const usagePercent = quotaUsagePercent(primary);
                    return (
                      <article className="provider-quota-card" key={resource.id}>
                        <div className="provider-quota-card-head">
                          <div className="provider-quota-account">
                            <span>{resource.name}</span>
                            <strong>{accountEmail}</strong>
                          </div>
                          <div className="provider-quota-card-actions">
                            {quota ? <span className={`provider-quota-status ${quotaStatus === "可用" ? "available" : "limited"}`}>{tx(quotaStatus)}</span> : null}
                            <button className="secondary-button" disabled={accountQuotaBusyIDs[resource.id]} onClick={() => void queryAccountQuota(resource)} type="button">
                              {tx(accountQuotaBusyIDs[resource.id] ? "查询中" : quota ? "刷新额度" : "查询额度")}
                            </button>
                            {resource.status === "active" ? (
                              <button
                                className="secondary-button provider-account-disable-button"
                                disabled={loading}
                                onClick={() => requestAccountConfirmation("disable", resource)}
                                type="button"
                              >
                                <Ban size={14} />
                                {tx("禁用")}
                              </button>
                            ) : (
                              <button
                                className="secondary-button"
                                disabled={loading}
                                onClick={() => requestAccountConfirmation("enable", resource)}
                                type="button"
                              >
                                {tx("启用")}
                              </button>
                            )}
                            <button
                              className="danger-button provider-account-delete-button"
                              disabled={loading}
                              onClick={() => requestAccountConfirmation("delete", resource)}
                              type="button"
                            >
                              <Trash2 size={14} />
                              {tx("删除")}
                            </button>
                          </div>
                        </div>
                        {quota ? (
                          <>
                            <div className="provider-quota-summary">
                              <div className="provider-quota-usage">
                                <span>{tx("主窗口使用率")}</span>
                                <strong>{primary ? `${formatQuotaPercent(primary.used_percent)}%` : "-"}</strong>
                                <div
                                  aria-label={tx("主窗口使用率")}
                                  aria-valuemax={100}
                                  aria-valuemin={0}
                                  aria-valuenow={usagePercent}
                                  className="provider-quota-progress"
                                  role="progressbar"
                                >
                                  <span style={{ width: `${usagePercent}%` }} />
                                </div>
                              </div>
                              <div className="provider-quota-highlights">
                                <QuotaMetric label="套餐" value={quota.plan_type || resource.credential_summary?.plan_type || "-"} />
                                <QuotaMetric label="重置额度" value={quota.rate_limit_reset_credits ? String(quota.rate_limit_reset_credits.available_count) : "-"} />
                                <QuotaMetric label="主窗口重置时间" value={quotaWindowResetLabel(primary)} />
                              </div>
                            </div>
                            <div className="provider-quota-fetched-at">
                              {tx("查询时间")} · {new Date(quota.fetched_at * 1000).toLocaleString()}
                            </div>
                            <details className="provider-quota-details">
                              <summary>{tx("账号与更多用量详情")}</summary>
                              {secondary ? (
                                <div className="provider-quota-grid">
                                  <QuotaMetric label="次窗口使用率" value={`${formatQuotaPercent(secondary.used_percent)}%`} />
                                  <QuotaMetric label="次窗口重置" value={quotaWindowResetLabel(secondary)} />
                                </div>
                              ) : null}
                              <ProviderAccountDetails resource={resource} />
                            </details>
                          </>
                        ) : (
                          <p className="provider-credential-note">
                            {tx(accountQuotaBusyIDs[resource.id] ? "正在自动查询该真实账号的套餐与额度信息。" : "暂未获取到该账号的套餐与额度信息，可点击查询重试。")}
                          </p>
                        )}
                        {accountQuotaErrors[resource.id] ? <p className="provider-quota-error">{accountQuotaErrors[resource.id]}</p> : null}
                      </article>
                    );
                  })}
                </div>
              </section>
            ) : null}
            {mode === "edit" && editTab === "test" && subscriptionResources.length > 0 ? (
              <section className="provider-quota-panel provider-codex-test-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("Codex 真实请求测试")}</h3>
                  <p>{tx("使用顶部选中的账号真实调用 Codex Responses；选择全部账号时，每个账号都会分别发起一次请求并展示独立结果。")}</p>
                </div>
                {selectedAccountID === "all" ? (
                  <p className="provider-account-intersection-note">
                    {tx("当前模型列表是所有账号可用模型的交集，确保所选模型能够被每个账号真实调用。")}
                  </p>
                ) : null}
                <div className="provider-codex-test-controls">
                  <label className="field">
                    <span>{tx("模型")}</span>
                    <select
                      disabled={accountCatalogLoading || codexTestModels.length === 0}
                      value={codexTestValues.model}
                      onChange={(event) => setCodexTestValues((current) => ({ ...current, model: event.target.value }))}
                    >
                      {codexTestModels.map((model) => <option key={model.id} value={model.id}>{model.display_name || model.name}</option>)}
                    </select>
                    {accountCatalogLoading ? <small>{tx("正在从所选账号加载模型目录...")}</small> : null}
                  </label>
                  <label className="field">
                    <span>{tx("推理强度")}</span>
                    <select
                      disabled={accountCatalogLoading || codexTestModels.length === 0}
                      value={codexTestValues.reasoning_effort}
                      onChange={(event) => setCodexTestValues((current) => ({ ...current, reasoning_effort: event.target.value }))}
                    >
                      {codexReasoningEfforts.map((value) => <option key={value} value={value}>{value}</option>)}
                    </select>
                  </label>
                  <label className="field">
                    <span>{tx("速度")}</span>
                    <select
                      disabled={accountCatalogLoading || codexTestModels.length === 0}
                      value={codexTestValues.speed}
                      onChange={(event) => setCodexTestValues((current) => ({ ...current, speed: event.target.value }))}
                    >
                      <option value="standard">{tx("标准")}</option>
                      <option value="fast">{tx("快速")}</option>
                    </select>
                  </label>
                  <label className="field provider-codex-test-prompt">
                    <span>{tx("真实提示词")}</span>
                    <textarea
                      rows={3}
                      value={codexTestValues.prompt}
                      onChange={(event) => setCodexTestValues((current) => ({ ...current, prompt: event.target.value }))}
                      placeholder={tx("输入要真实发送给 Codex 的内容")}
                    />
                  </label>
                </div>
                {Object.entries(accountCatalogErrors).map(([resourceID, message]) => (
                  <p className="provider-quota-error" key={resourceID}>
                    {subscriptionResources.find((resource) => resource.id === resourceID)?.name || resourceID}：{message}
                  </p>
                ))}
                <div className="provider-codex-test-actions">
                  <button
                    className="primary-button"
                    disabled={
                      Boolean(codexTestBusyID) ||
                      accountCatalogLoading ||
                      codexTestModels.length === 0 ||
                      !codexTestValues.prompt.trim()
                    }
                    onClick={() => void testCodexSubscription()}
                    type="button"
                  >
                    <Send size={14} />
                    {tx(codexTestBusyID ? "正在真实调用" : selectedAccountID === "all" ? "测试全部账号" : "发送真实测试")}
                  </button>
                </div>
                <div className="provider-quota-list">
                  {selectedAccountResources.map((resource) => {
                    const result = codexTestResults[resource.id];
                    const testError = codexTestErrors[resource.id];
                    return (
                    <article className="provider-quota-card" key={resource.id}>
                        <div className="provider-quota-card-head">
                          <div>
                            <strong>{resource.name}</strong>
                            <span>{providerResourceAccountLabel(resource)}</span>
                          </div>
                          <span className={testError ? "provider-test-status error" : result ? "provider-test-status success" : "provider-test-status"}>
                            {tx(testError ? "失败" : result ? "完成" : "等待测试")}
                          </span>
                        </div>
                        {result ? (
                          <div className="provider-codex-test-result">
                            <div className="provider-quota-grid">
                              <QuotaMetric label="模型" value={result.model} />
                              <QuotaMetric label="推理强度" value={result.reasoning_effort} />
                              <QuotaMetric label="请求速度" value={result.speed === "fast" ? "快速" : "标准"} />
                              <QuotaMetric label="上游 Service Tier" value={result.upstream_service_tier || "未返回"} />
                              <QuotaMetric label="耗时" value={`${result.latency_ms} ms`} />
                              <QuotaMetric label="输入 Token" value={String(result.usage.prompt_tokens)} />
                              <QuotaMetric label="输出 Token" value={String(result.usage.completion_tokens)} />
                              <QuotaMetric label="总 Token" value={String(result.usage.total_tokens)} />
                            </div>
                            <pre>{result.output_text}</pre>
                          </div>
                        ) : testError ? (
                          <p className="provider-quota-error">{testError}</p>
                        ) : (
                          <p className="provider-credential-note">{tx("填写提示词后发送；返回内容来自真实 Codex 上游，不使用 Mock。")}</p>
                        )}
                      </article>
                    );
                  })}
                </div>
                {codexTestErrors.selection ? <p className="provider-quota-error">{codexTestErrors.selection}</p> : null}
              </section>
            ) : null}
            {(mode === "edit" && editTab === "settings") || (mode === "create" && createStep === 1) ? (
              mode === "edit" && editingCodexSubscription && selectedAccountID === "all" ? (
                <div className="provider-account-selection-empty">
                  <UserRoundCheck size={28} />
                  <strong>{tx("请选择具体账号")}</strong>
                  <span>{tx("基础配置不能同时编辑全部账号，请先在顶部账号列表中选择一个具体账号。")}</span>
                </div>
              ) : (
              <div className="provider-form-grid">
              <label className="field">
                <span>Provider ID</span>
                <input value={values.id ?? ""} onChange={(event) => update("id", event.target.value)} placeholder={catalogID === "custom" ? tx("例如 prv_company_proxy") : tx("留空自动生成")} readOnly={mode === "edit"} />
              </label>
              <label className="field">
                <span>{tx(credentialMode === "account_integration" ? "通道名称" : "渠道名称")}</span>
                <input value={values.name ?? ""} onChange={(event) => update("name", event.target.value)} required />
              </label>
              <label className="field">
                <span>{tx(credentialMode === "account_integration" ? "兼容协议" : "渠道商类型")}</span>
                <select value={values.type ?? ""} onChange={(event) => update("type", event.target.value)} required>
                  {providerTypeOptions.map((option) => <option key={option} value={option}>{providerTypeLabel(option)}</option>)}
                </select>
              </label>
              <label className="field">
                <span>Base URL</span>
                <input value={values.base_url ?? ""} onChange={(event) => update("base_url", event.target.value)} />
              </label>
              {mode === "edit" ? (
                <label className="field">
                  <span>API Key</span>
                  <input value={values.api_key ?? ""} type="password" onChange={(event) => update("api_key", event.target.value)} />
                  {mode === "edit" ? <small>{tx("留空表示不修改现有 Key；填写新值才会覆盖。")}</small> : null}
                </label>
              ) : null}
              <label className="field">
                <span>{tx("优先级")}</span>
                <input value={values.priority ?? "10"} type="number" onChange={(event) => update("priority", event.target.value)} />
              </label>
            </div>
              )
            ) : null}

            {(mode === "edit" && editTab === "models") || (mode === "create" && createStep === 3) ? (
              <>
            {mode === "edit" && editingCodexSubscription && selectedAccountID === "all" ? (
              <p className="provider-account-intersection-note">
                {tx("当前上游模型映射仅展示所有账号都支持的模型交集。这样创建的路由才能在账号池切换时保持可用，避免请求被分配到不支持该模型的账号。")}
              </p>
            ) : null}
            <div className="provider-import-options">
              <div>
                <strong>{tx("自动路由")}</strong>
                <span>{mode === "edit" ? tx("开启后会为下方勾选模型补齐缺失线路，不覆盖已有策略。") : tx("保存渠道时会自动创建下方勾选模型的默认路由。")}</span>
              </div>
              <div className="boolean-toggle provider-route-toggle" role="radiogroup" aria-label={tx("自动路由")}>
                <button
                  aria-checked={autoRouteEnabled}
                  className={autoRouteEnabled ? "active" : ""}
                  onClick={() => update("create_routes", "true")}
                  role="radio"
                  type="button"
                >
                  {tx("开启")}
                </button>
                <button
                  aria-checked={!autoRouteEnabled}
                  className={!autoRouteEnabled ? "active" : ""}
                  onClick={() => update("create_routes", "false")}
                  role="radio"
                  type="button"
                >
                  {tx("关闭开关")}
                </button>
              </div>
            </div>

            <div className="provider-model-head">
              <div>
                <strong>{tx("上游模型映射")}</strong>
                <span>{effectiveDetail ? `${models.length}/${effectiveDetail.models_count} ${tx("个可映射模型")}` : effectiveCatalogError || tx("加载中")}</span>
              </div>
              <div className="provider-model-tools">
                <input value={modelQuery} onChange={(event) => setModelQuery(event.target.value)} placeholder={tx("搜索模型、能力、参数")} />
                <button className="secondary-button" onClick={reloadSelectedCatalog} type="button">
                  {tx("重新加载")}
                </button>
              </div>
            </div>
            <div className="provider-model-list">
              {effectiveCatalogLoading ? (
                <div className="empty">{tx("正在加载模型列表...")}</div>
              ) : effectiveCatalogError ? (
                <div className="empty">{effectiveCatalogError}</div>
              ) : filteredModels.length === 0 ? (
                <div className="empty">{models.length === 0 ? tx("该渠道商暂无可匹配当前标准模型目录的上游模型") : tx("没有匹配的模型")}</div>
              ) : filteredModels.map((model) => (
                <label className="model-option" key={model.id}>
                  <input
                    checked={autoRouteEnabled && selectedModels[model.id] === true}
                    disabled={!autoRouteEnabled}
                    onChange={(event) => setSelectedModels((current) => ({ ...current, [model.id]: event.target.checked }))}
                    type="checkbox"
                  />
                  <div>
                    <strong>{model.display_name || model.name}</strong>
                    <span>{model.canonical_name || model.id} ← {model.id}</span>
                    <small>
                      {modelCategoryLabel(modelCategoryForCatalog(model))} · {model.family || "model"} · {model.type || "chat"} · {formatModelPrice(model)} · {model.context_window ? `${compactNumber(model.context_window)} ctx` : "ctx -"}
                      {existingRouteModels.has(model.canonical_name || canonicalModelNameForUI(model.id, model.display_name)) ? ` · ${tx("已有路由")}` : ""}
                    </small>
                    <div className="capability-row">
                      {modelCapabilities(model).map((capability) => <em key={capability}>{capability}</em>)}
                    </div>
                  </div>
                </label>
              ))}
            </div>
            <p className="provider-import-hint">
              {!autoRouteEnabled
                ? tx("已关闭自动路由：保存后只创建 Provider，不生成路由策略。")
                : selectedRouteCount > 0
                  ? `${tx("保存后会为")} ${selectedRouteCount} ${tx("个已选")} ${modelCategoryLabel(modelCategory)} ${tx("模型创建缺失的默认路由。")}`
                  : tx("当前没有勾选模型，保存后不会生成路由策略。")}
            </p>
              </>
            ) : null}
          </section>
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          {mode === "create" && createStep > 0 ? (
            <button className="secondary-button" onClick={() => setCreateStep((current) => Math.max(current - 1, 0))} type="button" disabled={loading}>
              {tx("上一步")}
            </button>
          ) : null}
          <button className="button" disabled={loading} type="submit">
            {mode === "create"
              ? createStep === lastCreateStep
                ? loading ? tx("保存中") : tx("保存 Provider")
                : tx("下一步")
              : tx("保存")}
          </button>
        </div>
      </form>
      {accountConfirmation ? (
        <div className="modal-backdrop provider-account-confirmation-backdrop" role="presentation">
          <div
            aria-labelledby="provider-account-confirmation-title"
            aria-modal="true"
            className="confirm-modal provider-account-confirmation-modal"
            role="dialog"
          >
            <div>
              <p className="eyebrow">{tx("账号安全操作")}</p>
              <h2 id="provider-account-confirmation-title">
                {tx(accountConfirmation.action === "delete"
                  ? "确认彻底删除账号"
                  : accountConfirmation.action === "enable"
                    ? "确认启用账号"
                    : "确认禁用账号")}
              </h2>
            </div>
            <div className="provider-account-confirmation-target">
              <span>{accountConfirmation.resource.name}</span>
              <strong>{providerResourceAccountLabel(accountConfirmation.resource)}</strong>
            </div>
            <p>
              {tx(accountConfirmation.action === "delete"
                ? "删除后，账号凭据、额度缓存、健康观测、会话绑定和运行状态都会被永久清除，且无法恢复。"
                : accountConfirmation.action === "enable"
                  ? "启用后，该账号会重新加入路由调度，并恢复额度查询、真实测试和模型映射能力。"
                  : "禁用后，该账号会立即退出路由调度；账号数据仍会保留，可稍后重新启用。")}
            </p>
            {accountConfirmation.action === "delete" ? (
              <label className="field provider-account-delete-confirmation-field">
                <span>{tx("请输入以下英文短语以确认删除")}</span>
                <code>{deleteAccountConfirmationPhrase}</code>
                <input
                  autoComplete="off"
                  autoFocus
                  onChange={(event) => {
                    setDeleteAccountConfirmation(event.target.value);
                    setAccountConfirmationError("");
                  }}
                  placeholder={deleteAccountConfirmationPhrase}
                  spellCheck={false}
                  value={deleteAccountConfirmation}
                />
              </label>
            ) : null}
            {accountConfirmationError ? <p className="provider-quota-error" role="alert">{accountConfirmationError}</p> : null}
            <div className="modal-actions">
              <button className="secondary-button" disabled={accountConfirmationBusy} onClick={closeAccountConfirmation} type="button">
                {tx("取消")}
              </button>
              <button
                className={accountConfirmation.action === "delete" ? "danger-confirm" : "button"}
                disabled={
                  accountConfirmationBusy ||
                  (accountConfirmation.action === "delete" && deleteAccountConfirmation !== deleteAccountConfirmationPhrase)
                }
                onClick={() => {
                  if (accountConfirmation.action === "delete") {
                    void deleteAccount(accountConfirmation.resource);
                  } else {
                    void updateAccountStatus(accountConfirmation.resource, accountConfirmation.action);
                  }
                }}
                type="button"
              >
                {tx(accountConfirmationBusy
                  ? "处理中"
                  : accountConfirmation.action === "delete"
                    ? "永久删除账号"
                    : accountConfirmation.action === "enable"
                      ? "确认启用"
                      : "确认禁用")}
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {accountOAuthCallbackModalOpen ? (
        <div className="modal-backdrop provider-oauth-callback-backdrop" role="presentation">
          <div
            aria-labelledby="provider-oauth-callback-title"
            aria-modal="true"
            className="confirm-modal provider-oauth-callback-modal"
            role="dialog"
          >
            <div className="provider-oauth-callback-heading">
              <div className="provider-oauth-callback-icon" aria-hidden="true">
                <KeyRound size={18} />
              </div>
              <div>
                <p className="eyebrow">{tx("OpenAI/Codex 授权")}</p>
                <h2 id="provider-oauth-callback-title">{tx("粘贴授权回调地址")}</h2>
              </div>
            </div>
            <p>{tx("完成登录和授权后，请复制浏览器地址栏中的完整 localhost 地址，并粘贴到下方。")}</p>
            <label className="field">
              <span>{tx("登录后的 localhost 地址")}</span>
              <textarea
                autoFocus
                onChange={(event) => {
                  setAccountOAuthCallback(event.target.value);
                  setAccountOAuthCallbackModalError("");
                }}
                placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                value={accountOAuthCallback}
              />
              <small>{tx("localhost 页面显示无法访问是正常现象；请复制地址栏中的完整地址，不要只复制授权 code。")}</small>
            </label>
            {accountOAuthCallbackModalError ? (
              <p className="provider-oauth-callback-error" role="alert">{accountOAuthCallbackModalError}</p>
            ) : null}
            <div className="modal-actions">
              <button className="secondary-button" onClick={() => setAccountOAuthCallbackModalOpen(false)} type="button">
                {tx("稍后填写")}
              </button>
              <button className="button" disabled={accountOAuthBusy} onClick={confirmProviderAccountOAuthCallback} type="button">
                <Check size={15} />
                {tx(accountOAuthBusy ? "处理中" : "确认并回填")}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function QuotaMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="provider-quota-metric">
      <span>{tx(label)}</span>
      <strong>{tx(value)}</strong>
    </div>
  );
}

function ProviderAccountDetails({ resource }: { resource: ProviderResource }) {
  const summary = resource.credential_summary ?? {};
  const options = resource.options ?? {};
  const rawItems: Array<[string, string | undefined]> = [
    ["资源 ID", resource.id],
    ["账号邮箱", summary.account_email],
    ["账号 ID", summary.account_id],
    ["用户 ID", summary.user_id],
    ["组织 ID", summary.organization_id],
    ["套餐", summary.plan_type],
    ["认证方式", summary.auth_type],
    ["Token 类型", summary.token_type || options.token_type],
    ["Token 过期时间", formatProviderAccountDate(summary.token_expires_at || options.token_expires_at)],
    ["授权范围", summary.scopes || options.scopes],
    ["Refresh Token", summary.has_refresh_token === "true" ? "已配置" : "未配置"],
    ["资源状态", resource.status],
    ["健康状态", resource.healthy ? "健康" : "异常"],
    ["资源组", resource.group],
    ["Base URL", resource.base_url],
    ["区域", resource.region],
    ["环境", resource.environment],
    ["优先级", String(resource.priority)],
    ["权重", String(resource.weight)],
    ["每分钟请求限制", resource.rate_limit_rpm ? String(resource.rate_limit_rpm) : "-"],
    ["每分钟 Token 限制", resource.token_limit_tpm ? String(resource.token_limit_tpm) : "-"],
    ["最大并发", resource.max_concurrency ? String(resource.max_concurrency) : "-"],
    ["连续失败次数", String(resource.failure_count ?? 0)],
    ["冷却截止时间", formatProviderAccountDate(resource.cooldown_until)],
    ["最后使用时间", formatProviderAccountDate(resource.last_used_at)],
    ["最后检查时间", formatProviderAccountDate(resource.last_checked_at)],
    ["创建时间", formatProviderAccountDate(resource.created_at)],
    ["更新时间", formatProviderAccountDate(resource.updated_at)],
  ];
  const items = rawItems.filter(([, value]) => value !== undefined && value !== "");
  return (
    <div className="provider-account-detail-grid">
      {items.map(([label, value]) => <QuotaMetric key={label} label={label} value={value || "-"} />)}
    </div>
  );
}

function providerResourceAccountLabel(resource: ProviderResource) {
  return resource.credential_summary?.account_email || resource.credential_summary?.account_id || resource.name;
}

function formatProviderAccountDate(value?: string) {
  if (!value) return "-";
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? value : new Date(timestamp).toLocaleString();
}

function intersectProviderCatalogs(catalogs: ProviderCatalogEntry[]): ProviderCatalogEntry | null {
  if (catalogs.length === 0) return null;
  const first = catalogs[0];
  const models = (first.models ?? []).filter((model) =>
    catalogs.slice(1).every((catalog) =>
      (catalog.models ?? []).some((candidate) => candidate.id.toLowerCase() === model.id.toLowerCase()),
    ),
  ).map((model) => {
    const matches = catalogs.map((catalog) =>
      (catalog.models ?? []).find((candidate) => candidate.id.toLowerCase() === model.id.toLowerCase())!,
    );
    const supportedParameters = (model.supported_parameters ?? []).filter((parameter) =>
      matches.slice(1).every((candidate) => candidate.supported_parameters?.includes(parameter)),
    );
    const reasoningLevels = intersectStringLists(matches.map((candidate) =>
      candidate.metadata?.supported_reasoning_levels?.split(",").map((value) => value.trim()).filter(Boolean) ?? [],
    ));
    const speedTiers = intersectStringLists(matches.map((candidate) =>
      candidate.metadata?.additional_speed_tiers?.split(",").map((value) => value.trim()).filter(Boolean) ?? [],
    ));
    const contextWindows = matches.map((candidate) => candidate.context_window).filter((value): value is number => Boolean(value));
    return {
      ...model,
      context_window: contextWindows.length > 0 ? Math.min(...contextWindows) : model.context_window,
      supported_parameters: supportedParameters,
      metadata: {
        ...model.metadata,
        supported_reasoning_levels: reasoningLevels.join(","),
        additional_speed_tiers: speedTiers.join(","),
      },
    };
  });
  return {
    ...first,
    models,
    models_count: models.length,
    category_counts: { ...(first.category_counts ?? {}), codex: models.length },
  };
}

function intersectStringLists(lists: string[][]) {
  if (lists.length === 0) return [];
  return lists[0].filter((value) => lists.slice(1).every((list) => list.includes(value)));
}

function formatQuotaPercent(value: number) {
  return Number.isFinite(value) ? String(Math.round(value * 10) / 10) : "0";
}

function quotaUsagePercent(window?: OpenAIQuotaWindow) {
  if (!window || !Number.isFinite(window.used_percent)) return 0;
  return Math.min(100, Math.max(0, window.used_percent));
}

function quotaWindowResetLabel(window?: OpenAIQuotaWindow) {
  if (!window) return "-";
  if (window.reset_at > 0) return new Date(window.reset_at * 1000).toLocaleString();
  if (window.reset_after_seconds > 0) {
    const minutes = Math.ceil(window.reset_after_seconds / 60);
    return minutes >= 60 ? `${Math.ceil(minutes / 60)} ${tx("小时后")}` : `${minutes} ${tx("分钟后")}`;
  }
  return "-";
}
