import { FileText, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { appRole } from "../core/navigation";
import { type AdminUser, type ApiContext, type AppData, type AppRole, type Model } from "../core/types";
import { activeRouteCount, apiGatewayBaseURL, playgroundModels } from "../domain/formatting";
import { type AppLanguage, languageOptions } from "../i18n/runtime";
import { SimpleTable } from "../shared/ui";
import { gatewayEnglishDocs } from "./gateway-docs-en";
import { gatewayJapaneseDocs } from "./gateway-docs-ja";
import { apiMethodClass, GatewayCodeBlock, GatewayCopyCard, gatewayLanguageLabel } from "./gateway-docs-ui";
import { gatewayChineseDocs } from "./gateway-docs-zh";
import { gatewayLLMUsageDocs } from "./gateway-llm-en";
import { QuickAccessView } from "./quick-access";

export function GatewayView({
  api,
  data,
  user,
  language,
  onLanguageChange,
  loading,
  onQuickCreateKey,
  onManageKeys,
}: {
  api: ApiContext;
  data: AppData;
  user: AdminUser;
  language: AppLanguage;
  onLanguageChange: (language: AppLanguage) => void;
  loading: boolean;
  onQuickCreateKey: (values: Record<string, string>, onCreated: () => void) => void;
  onManageKeys: () => void;
}) {
  const role = appRole(user.role);
  if (role === "user" || role === "team_leader") {
    return (
      <QuickAccessView
        api={api}
        data={data}
        user={user}
        loading={loading}
        onCreateKey={onQuickCreateKey}
        onManageKeys={onManageKeys}
      />
    );
  }

  return <GatewayDocsView api={api} data={data} language={language} onLanguageChange={onLanguageChange} role={role} />;
}

function GatewayDocsView({
  api,
  data,
  language,
  onLanguageChange,
  role,
}: {
  api: ApiContext;
  data: AppData;
  language: AppLanguage;
  onLanguageChange: (language: AppLanguage) => void;
  role: AppRole;
}) {
  const baseURL = apiGatewayBaseURL(api.baseURL);
  const activeRoutes = data.routes.filter((route) => route.status === "active").length;
  const callableModels = playgroundModels(data);
  const sampleModel = callableModels.find((model) => activeRouteCount(model.name, data) > 0)?.name ?? callableModels[0]?.name ?? "gpt-4.1-mini";
  const keyHint = data.keys[0] ? `${data.keys[0].key_prefix}...${data.keys[0].key_suffix}` : "YOUR_TOKENHUB_API_KEY";
  const docBundle = gatewayDocBundle({ language, baseURL, keyHint, sampleModel, activeRoutes, data, callableModels, role });
  const [activeDocID, setActiveDocID] = useState(docBundle.defaultDocID ?? "quickstart");
  const allDocs = docBundle.groups.flatMap((group) => group.items);
  const activeDoc = allDocs.find((item) => item.id === activeDocID) ?? allDocs[0]!;
  const defaultDocID = docBundle.defaultDocID ?? allDocs[0]?.id ?? "quickstart";

  useEffect(() => {
    if (!allDocs.some((item) => item.id === activeDocID)) {
      setActiveDocID(defaultDocID);
    }
  }, [activeDocID, allDocs, defaultDocID]);

  return (
    <div className="gateway-docs">
      <div className="api-doc-shell">
        <GatewayDocNav groups={docBundle.groups} activeID={activeDoc.id} onSelect={setActiveDocID} ui={docBundle.nav} />
        <section className="api-doc-main">
          <header className="api-doc-main-head">
            <div>
              <p className="eyebrow">{docBundle.eyebrow}</p>
              <h2>{docBundle.title}</h2>
              <p>{docBundle.description}</p>
            </div>
            <div className="api-doc-language-switcher" aria-label={docBundle.languageLabel}>
              {languageOptions.map((option) => (
                <button
                  aria-pressed={language === option.value}
                  className={language === option.value ? "active" : ""}
                  key={option.value}
                  onClick={() => onLanguageChange(option.value)}
                  type="button"
                >
                  {gatewayLanguageLabel(option.value)}
                </button>
              ))}
            </div>
          </header>

          <section className="api-doc-quick-grid" aria-label={docBundle.quickInfoLabel}>
            <GatewayCopyCard label={docBundle.quickCards.baseURL} value={baseURL} />
            <GatewayCopyCard label={docBundle.quickCards.authorization} value={`Bearer ${keyHint}`} />
            <GatewayCopyCard label={docBundle.quickCards.sampleModel} value={sampleModel} />
            <article className="gateway-copy-card api-doc-config-card">
              <span>{docBundle.quickCards.currentConfig}</span>
              <strong>{docBundle.quickCards.activeRoutes}</strong>
              <small>{docBundle.quickCards.apiKeys}</small>
            </article>
          </section>

          <GatewayDocContent doc={activeDoc} />
        </section>
      </div>
    </div>
  );
}

export type GatewayDocTable = {
  title: string;
  columns: string[];
  rows: React.ReactNode[][];
};

export type GatewayDocItem = {
  id: string;
  group: string;
  title: string;
  description: string;
  badge?: string;
  method?: string;
  path?: string;
  details?: Array<{ label: string; value: string }>;
  notesTitle?: string;
  notes?: string[];
  params?: GatewayDocTable;
  table?: GatewayDocTable;
  examplesTitle?: string;
  examples?: Array<{ title: string; code: string }>;
};

export type GatewayDocGroup = {
  title: string;
  items: GatewayDocItem[];
};

export type GatewayDocNavCopy = {
  title: string;
  subtitle: string;
  searchPlaceholder: string;
  noResults: string;
};

export type GatewayDocBundle = {
  defaultDocID?: string;
  nav: GatewayDocNavCopy;
  eyebrow: string;
  title: string;
  description: string;
  languageLabel: string;
  quickInfoLabel: string;
  quickCards: {
    baseURL: string;
    authorization: string;
    sampleModel: string;
    currentConfig: string;
    activeRoutes: string;
    apiKeys: string;
  };
  groups: GatewayDocGroup[];
};

export function GatewayDocNav({
  groups,
  activeID,
  onSelect,
  ui,
}: {
  groups: GatewayDocGroup[];
  activeID: string;
  onSelect: (id: string) => void;
  ui: GatewayDocNavCopy;
}) {
  const [searchText, setSearchText] = useState("");
  const normalizedSearch = searchText.trim().toLowerCase();
  const visibleGroups = normalizedSearch
    ? groups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) =>
          [group.title, item.title, item.description, item.path, item.badge, item.group]
            .filter(Boolean)
            .join(" ")
            .toLowerCase()
            .includes(normalizedSearch),
        ),
      }))
      .filter((group) => group.items.length > 0)
    : groups;

  return (
    <aside className="api-doc-nav" aria-label={ui.title}>
      <div className="api-doc-nav-head">
        <FileText size={16} />
        <div>
          <strong>{ui.title}</strong>
          <span>{ui.subtitle}</span>
        </div>
      </div>
      <label className="api-doc-search">
        <Search size={15} />
        <input
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
          placeholder={ui.searchPlaceholder}
        />
      </label>
      <div className="api-doc-nav-list">
        {visibleGroups.length ? visibleGroups.map((group) => (
          <section className="api-doc-nav-group" key={group.title}>
            <h3>{group.title}</h3>
            {group.items.map((item) => (
              <button
                aria-selected={activeID === item.id}
                className={activeID === item.id ? "api-doc-nav-item active" : "api-doc-nav-item"}
                key={item.id}
                onClick={() => onSelect(item.id)}
                type="button"
              >
                {item.method ? <span className={`api-method ${apiMethodClass(item.method)}`}>{item.method}</span> : <span className="api-method muted">{item.badge ?? "DOC"}</span>}
                <span>
                  <strong>{item.title}</strong>
                  {item.path ? <em>{item.path}</em> : <em>{item.description}</em>}
                </span>
              </button>
            ))}
          </section>
        )) : <div className="api-doc-empty">{ui.noResults}</div>}
      </div>
    </aside>
  );
}

export function GatewayDocContent({ doc }: { doc: GatewayDocItem }) {
  return (
    <article className="api-doc-content-card">
      <GatewayDocTitle doc={doc} />
      {doc.details ? (
        <div className="api-doc-detail-grid">
          {doc.details.map((item) => (
            <div key={item.label}>
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
          ))}
        </div>
      ) : null}
      {doc.notes ? (
        <section className="api-doc-panel">
          <h3>{doc.notesTitle ?? "Notes"}</h3>
          <ul className="api-doc-notes">
            {doc.notes.map((note) => <li key={note}>{note}</li>)}
          </ul>
        </section>
      ) : null}
      {doc.params ? (
        <section className="api-doc-panel">
          <h3>{doc.params.title}</h3>
          <SimpleTable columns={doc.params.columns} rows={doc.params.rows} />
        </section>
      ) : null}
      {doc.table ? (
        <section className="api-doc-panel">
          <h3>{doc.table.title}</h3>
          <SimpleTable columns={doc.table.columns} rows={doc.table.rows} />
        </section>
      ) : null}
      {doc.examples ? (
        <section className="api-doc-panel">
          <h3>{doc.examplesTitle ?? "Examples"}</h3>
          <div className="api-doc-code-grid">
            {doc.examples.map((example) => (
              <div className="api-doc-code-card" key={example.title}>
                <strong>{example.title}</strong>
                <GatewayCodeBlock code={example.code} />
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </article>
  );
}

export function GatewayDocTitle({ doc }: { doc: GatewayDocItem }) {
  return (
    <div className="api-doc-title">
      <div>
        <span>{doc.group}</span>
        <h2>{doc.title}</h2>
        <p>{doc.description}</p>
      </div>
      {doc.path ? (
        <div className="api-doc-endpoint">
          <span className={`api-method ${apiMethodClass(doc.method)}`}>{doc.method}</span>
          <code>{doc.path}</code>
        </div>
      ) : null}
    </div>
  );
}

export function gatewayDocBundle({
  language,
  baseURL,
  keyHint,
  sampleModel,
  activeRoutes,
  data,
  callableModels,
  role,
}: {
  language: AppLanguage;
  baseURL: string;
  keyHint: string;
  sampleModel: string;
  activeRoutes: number;
  data: AppData;
  callableModels: Model[];
  role: AppRole;
}): GatewayDocBundle {
  const authHeader = `Authorization: Bearer ${keyHint}`;
  const activeRouteCountValue = activeRoutes || data.summary.active_route_count || 0;
  const apiKeyCount = data.keys.length || data.summary.api_key_count || 0;
  const projectCount = data.projects.length;
  const userCount = data.users.length || data.summary.user_count || 0;
  const providerCount = data.providers.length;
  const routeCount = data.routes.length;
  const requestLogCount = data.logs.length;
  const visibleModelCount = callableModels.length;
  const chatCurl = `curl -X POST "${baseURL}/chat/completions" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${sampleModel}",
    "messages": [
      {"role": "system", "content": "You are an internal enterprise AI assistant."},
      {"role": "user", "content": "Introduce TokenHub in two concise sentences."}
    ],
    "temperature": 0.7,
    "stream": false
  }'`;

  const commonEN = gatewayEnglishDocs({
    baseURL,
    keyHint,
    authHeader,
    sampleModel,
    chatCurl,
    activeRouteCountValue,
    apiKeyCount,
    projectCount,
    userCount,
    providerCount,
    routeCount,
    requestLogCount,
    visibleModelCount,
  });
  if (role === "user" || role === "team_leader") {
    return gatewayLLMUsageDocs({
      language,
      role,
      baseURL,
      keyHint,
      authHeader,
      sampleModel,
      chatCurl,
      activeRouteCountValue,
      apiKeyCount,
      projectCount,
      userCount,
      providerCount,
      routeCount,
      requestLogCount,
      visibleModelCount,
    });
  }
  if (language === "zh-CN") return gatewayChineseDocs(commonEN);
  if (language === "ja") return gatewayJapaneseDocs(commonEN);
  return commonEN;
}

export type GatewayDocStats = {
  baseURL: string;
  keyHint: string;
  authHeader: string;
  sampleModel: string;
  chatCurl: string;
  activeRouteCountValue: number;
  apiKeyCount: number;
  projectCount: number;
  userCount: number;
  providerCount: number;
  routeCount: number;
  requestLogCount: number;
  visibleModelCount: number;
};
