import { Check, Eye, EyeOff, Fingerprint, KeyRound, LockKeyhole, Moon, Server, ShieldCheck, Sun, UserRoundCheck, Users } from "lucide-react";
import { type FormEvent, useState } from "react";
import { savePendingOAuthBaseURL } from "../core/session";
import { type LoginIdentityProvider, viewRoutes } from "../core/types";
import { stringifyValue } from "../domain/entities";
import { identityProviderIconLabel } from "../domain/labels";
import { activeLanguage, tx } from "../i18n/runtime";

export const identityProviderIconOptions = [
  "auto",
  "gitlab",
  "github",
  "google",
  "microsoft",
  "okta",
  "keycloak",
  "oidc",
  "oauth2",
  "saml",
  "ldap",
  "sso",
];

export type IdentityProviderEndpointDefaults = {
  authorize_url?: string;
  token_url?: string;
  userinfo_url?: string;
};

export type IdentityProviderTemplate = {
  key: string;
  label: string;
  providerType: "oidc" | "oauth2";
  iconKey: string;
  loginLabel: string;
  issuerPlaceholder: string;
  defaultIssuer?: string;
  scopes: string;
  usernameClaim: string;
  emailClaim: string;
  teamClaim: string;
  endpoints?: (issuerURL: string) => IdentityProviderEndpointDefaults;
};

export const identityProviderTemplates: IdentityProviderTemplate[] = [
  {
    key: "generic_oidc",
    label: "通用 OIDC",
    providerType: "oidc",
    iconKey: "oidc",
    loginLabel: "SSO",
    issuerPlaceholder: "https://sso.example.com",
    scopes: "openid, profile, email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
  },
  {
    key: "gitlab",
    label: "GitLab",
    providerType: "oauth2",
    iconKey: "gitlab",
    loginLabel: "GitLab",
    issuerPlaceholder: "https://gitlab.example.com",
    scopes: "openid profile email read_user",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/oauth/authorize`,
      token_url: `${issuerURL}/oauth/token`,
      userinfo_url: `${issuerURL}/api/v4/user`,
    }) : {},
  },
  {
    key: "google",
    label: "Google",
    providerType: "oidc",
    iconKey: "google",
    loginLabel: "Google",
    issuerPlaceholder: "https://accounts.google.com",
    defaultIssuer: "https://accounts.google.com",
    scopes: "openid profile email",
    usernameClaim: "email",
    emailClaim: "email",
    teamClaim: "hd",
    endpoints: () => ({
      authorize_url: "https://accounts.google.com/o/oauth2/v2/auth",
      token_url: "https://oauth2.googleapis.com/token",
      userinfo_url: "https://openidconnect.googleapis.com/v1/userinfo",
    }),
  },
  {
    key: "microsoft",
    label: "Microsoft Entra ID",
    providerType: "oidc",
    iconKey: "microsoft",
    loginLabel: "Microsoft",
    issuerPlaceholder: "https://login.microsoftonline.com/{tenant}/v2.0",
    scopes: "openid profile email User.Read",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/oauth2/v2.0/authorize`,
      token_url: `${issuerURL}/oauth2/v2.0/token`,
      userinfo_url: "https://graph.microsoft.com/oidc/userinfo",
    }) : {},
  },
  {
    key: "okta",
    label: "Okta",
    providerType: "oidc",
    iconKey: "okta",
    loginLabel: "Okta",
    issuerPlaceholder: "https://company.okta.com/oauth2/default",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/v1/authorize`,
      token_url: `${issuerURL}/v1/token`,
      userinfo_url: `${issuerURL}/v1/userinfo`,
    }) : {},
  },
  {
    key: "keycloak",
    label: "Keycloak",
    providerType: "oidc",
    iconKey: "keycloak",
    loginLabel: "Keycloak",
    issuerPlaceholder: "https://keycloak.example.com/realms/company",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/protocol/openid-connect/auth`,
      token_url: `${issuerURL}/protocol/openid-connect/token`,
      userinfo_url: `${issuerURL}/protocol/openid-connect/userinfo`,
    }) : {},
  },
  {
    key: "custom_oauth2",
    label: "通用 OAuth2",
    providerType: "oauth2",
    iconKey: "oauth2",
    loginLabel: "OAuth2",
    issuerPlaceholder: "https://oauth.example.com",
    scopes: "profile, email",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
  },
];

export const identityProviderTemplateOptions = identityProviderTemplates.map((template) => template.key);

export type LoginIdentityProviderIconComponent = React.ComponentType<{ size?: number }>;

export function identityProviderLoginURL(baseURL: string, provider: LoginIdentityProvider, returnURL: string) {
  const target = new URL(`${baseURL.replace(/\/$/, "")}/api/admin/auth/oauth/start`);
  target.searchParams.set("id", provider.id);
  target.searchParams.set("return_url", returnURL);
  return target.toString();
}

export function currentOAuthReturnURL() {
  if (typeof window === "undefined") return viewRoutes.overview;
  return `${window.location.origin}${viewRoutes.overview}`;
}

export function loginIdentityProviderDisplayName(provider: LoginIdentityProvider) {
  if (provider.display_name) return provider.display_name;
  const iconKey = loginIdentityProviderIconKey(provider);
  const label = identityProviderIconLabel(iconKey);
  if (label !== "自动" && label !== "SSO" && label !== "OIDC" && label !== "OAuth2" && label !== "SAML" && label !== "LDAP") {
    return label;
  }
  return provider.name;
}

export function LoginIdentityProviderIcon({ provider }: { provider: LoginIdentityProvider }) {
  const iconKey = loginIdentityProviderIconKey(provider);
  const iconConfig = loginIdentityProviderIconConfig(iconKey);
  const Icon = iconConfig.icon;
  return (
    <span className={`login-sso-icon ${iconConfig.key}`} aria-hidden="true">
      <Icon size={15} />
    </span>
  );
}

export function loginIdentityProviderIconKey(provider: LoginIdentityProvider) {
  const configured = normalizedIdentityProviderIconKey(provider.icon_key);
  if (configured && configured !== "auto") return configured;
  const providerType = stringifyValue(provider.provider_type).trim().toLowerCase();
  const fingerprint = `${provider.name} ${provider.issuer_url ?? ""} ${providerType}`.toLowerCase();
  for (const key of ["gitlab", "github", "google", "microsoft", "azure", "entra", "okta", "keycloak"]) {
    if (fingerprint.includes(key)) {
      return key === "azure" || key === "entra" ? "microsoft" : key;
    }
  }
  return normalizedIdentityProviderIconKey(providerType) || "sso";
}

export function normalizedIdentityProviderIconKey(value: string | undefined) {
  const normalized = stringifyValue(value).trim().toLowerCase().replace(/[^a-z0-9_-]/g, "");
  return identityProviderIconOptions.includes(normalized) ? normalized : "";
}

export function normalizedIdentityProviderTemplateKey(value: string | undefined) {
  const normalized = stringifyValue(value).trim().toLowerCase().replace(/[^a-z0-9_-]/g, "");
  return identityProviderTemplates.some((template) => template.key === normalized) ? normalized : "";
}

export function identityProviderTemplateByKey(value: string | undefined) {
  const normalized = normalizedIdentityProviderTemplateKey(value);
  return identityProviderTemplates.find((template) => template.key === normalized) ?? identityProviderTemplates[0];
}

export function inferIdentityProviderTemplateKey(values: Record<string, string>) {
  const configured = normalizedIdentityProviderTemplateKey(values.provider_template);
  if (configured) return configured;
  const iconKey = normalizedIdentityProviderIconKey(values.icon_key);
  if (iconKey && identityProviderTemplates.some((template) => template.key === iconKey)) {
    return iconKey;
  }
  const fingerprint = `${values.name ?? ""} ${values.login_label ?? ""} ${values.issuer_url ?? ""}`.toLowerCase();
  for (const template of identityProviderTemplates) {
    if (template.key !== "generic_oidc" && template.key !== "custom_oauth2" && fingerprint.includes(template.key)) {
      return template.key;
    }
  }
  return stringsEqual(values.provider_type, "oauth2") ? "custom_oauth2" : "generic_oidc";
}

export function stringsEqual(left: string | undefined, right: string) {
  return String(left ?? "").trim().toLowerCase() === right;
}

export function normalizeIdentityProviderIssuer(value: string) {
  return value.trim().replace(/\/+$/, "");
}

export function identityProviderEndpointDefaults(template: IdentityProviderTemplate, issuerURL: string) {
  return template.endpoints?.(normalizeIdentityProviderIssuer(issuerURL)) ?? {};
}

export function applyIdentityProviderTemplate(values: Record<string, string>, templateKey: string, overwrite = true) {
  const template = identityProviderTemplateByKey(templateKey);
  const next: Record<string, string> = { ...values, provider_template: template.key };
  next.provider_type = template.providerType;
  next.icon_key = template.iconKey;
  if (template.defaultIssuer && (overwrite || !next.issuer_url)) next.issuer_url = template.defaultIssuer;
  const issuer = normalizeIdentityProviderIssuer(next.issuer_url || template.defaultIssuer || "");
  for (const [key, value] of Object.entries({
    login_label: template.loginLabel,
    scopes: template.scopes,
    username_claim: template.usernameClaim,
    email_claim: template.emailClaim,
    team_claim: template.teamClaim,
  })) {
    if (overwrite || !next[key]) next[key] = value;
  }
  const endpoints = identityProviderEndpointDefaults(template, issuer);
  for (const [key, value] of Object.entries(endpoints)) {
    if (value && (overwrite || !next[key])) next[key] = value;
  }
  return next;
}

export function identityProviderInitialFormValues(values: Record<string, string>, createMode: boolean) {
  const templateKey = inferIdentityProviderTemplateKey(values);
  const next: Record<string, string> = createMode ? applyIdentityProviderTemplate(values, templateKey, false) : { ...values, provider_template: templateKey };
  if (!next.default_role) next.default_role = "user";
  if (!next.default_project_role) next.default_project_role = "developer";
  return next;
}

export function updateIdentityProviderFormValue(values: Record<string, string>, key: string, value: string) {
  if (key === "provider_template") {
    return applyIdentityProviderTemplate(values, value, true);
  }
  const next = { ...values, [key]: value };
  if (key === "issuer_url") {
    const template = identityProviderTemplateByKey(next.provider_template || inferIdentityProviderTemplateKey(next));
    const previousEndpoints = identityProviderEndpointDefaults(template, values.issuer_url ?? "");
    const nextEndpoints = identityProviderEndpointDefaults(template, value);
    for (const endpointKey of ["authorize_url", "token_url", "userinfo_url"] as const) {
      if (!values[endpointKey] || values[endpointKey] === previousEndpoints[endpointKey]) {
        next[endpointKey] = nextEndpoints[endpointKey] ?? "";
      }
    }
  }
  return next;
}

export function identityProviderTemplateLabel(templateKey: string) {
  return identityProviderTemplateByKey(templateKey).label;
}

export function identityProviderTemplateHelp(template: IdentityProviderTemplate) {
  if (template.key === "generic_oidc") return "适合标准 OIDC 服务，填写 Issuer 后一般可自动发现端点。";
  if (template.key === "custom_oauth2") return "适合非标准 OAuth2 服务，需要确认授权、Token 和用户信息端点。";
  if (activeLanguage === "en") return `Best for ${tx(template.label)} enterprise apps; common endpoints and claims are prefilled.`;
  if (activeLanguage === "ja") return `${tx(template.label)} の企業アプリ向けです。一般的なエンドポイントと Claim を事前入力します。`;
  return `适合 ${template.label} 企业应用，常用端点和 Claim 已预置。`;
}

export function GoogleBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path fill="#4285f4" d="M22.6 12.2c0-.8-.1-1.6-.2-2.3H12v4.4h5.9c-.3 1.4-1.1 2.6-2.3 3.4v2.8h3.7c2.1-2 3.3-4.8 3.3-8.3z" />
      <path fill="#34a853" d="M12 23c3 0 5.5-1 7.3-2.6l-3.7-2.8c-1 .7-2.2 1.1-3.6 1.1-2.8 0-5.2-1.9-6.1-4.5H2.1V17C3.9 20.6 7.6 23 12 23z" />
      <path fill="#fbbc05" d="M5.9 14.2c-.2-.7-.4-1.4-.4-2.2s.1-1.5.4-2.2V7H2.1C1.4 8.5 1 10.2 1 12s.4 3.5 1.1 5l3.8-2.8z" />
      <path fill="#ea4335" d="M12 5.3c1.6 0 3.1.6 4.2 1.7l3.2-3.2C17.5 2 15 1 12 1 7.6 1 3.9 3.4 2.1 7l3.8 2.8C6.8 7.2 9.2 5.3 12 5.3z" />
    </svg>
  );
}

export function GitLabBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path fill="#fc6d26" d="M12 22 3.2 8.7h5.3L12 22z" />
      <path fill="#e24329" d="M3.2 8.7 4.8 3.9c.2-.6 1-.6 1.2 0l2.5 4.8H3.2z" />
      <path fill="#fca326" d="M12 22 20.8 8.7h-5.3L12 22z" />
      <path fill="#e24329" d="m20.8 8.7-1.6-4.8c-.2-.6-1-.6-1.2 0l-2.5 4.8h5.3z" />
      <path fill="#fc6d26" d="M8.5 8.7h7L12 22 8.5 8.7z" />
    </svg>
  );
}

export function GitHubBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path
        fill="currentColor"
        d="M12 1.8C6.4 1.8 1.8 6.4 1.8 12c0 4.5 2.9 8.3 7 9.7.5.1.7-.2.7-.5v-1.8c-2.8.6-3.4-1.2-3.4-1.2-.5-1.1-1.1-1.4-1.1-1.4-.9-.6.1-.6.1-.6 1 0 1.6 1.1 1.6 1.1.9 1.6 2.4 1.1 2.9.9.1-.7.4-1.1.7-1.4-2.2-.3-4.6-1.1-4.6-5 0-1.1.4-2 1.1-2.8-.1-.3-.5-1.3.1-2.7 0 0 .9-.3 2.9 1.1.8-.2 1.7-.3 2.6-.3s1.8.1 2.6.3c2-1.4 2.9-1.1 2.9-1.1.6 1.4.2 2.4.1 2.7.7.8 1.1 1.7 1.1 2.8 0 3.9-2.4 4.7-4.6 5 .4.3.7.9.7 1.8v2.6c0 .3.2.6.7.5 4.1-1.4 7-5.2 7-9.7C22.2 6.4 17.6 1.8 12 1.8z"
      />
    </svg>
  );
}

export function MicrosoftBrandIcon({ size = 15 }: { size?: number }) {
  const gap = 1.2;
  const cell = (24 - gap) / 2;
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <rect x="0" y="0" width={cell} height={cell} fill="#f25022" />
      <rect x={cell + gap} y="0" width={cell} height={cell} fill="#7fba00" />
      <rect x="0" y={cell + gap} width={cell} height={cell} fill="#00a4ef" />
      <rect x={cell + gap} y={cell + gap} width={cell} height={cell} fill="#ffb900" />
    </svg>
  );
}

export function loginIdentityProviderIconConfig(key: string): { key: string; icon: LoginIdentityProviderIconComponent } {
  switch (key) {
    case "gitlab":
      return { key, icon: GitLabBrandIcon };
    case "github":
      return { key, icon: GitHubBrandIcon };
    case "google":
      return { key, icon: GoogleBrandIcon };
    case "microsoft":
      return { key, icon: MicrosoftBrandIcon };
    case "okta":
      return { key, icon: UserRoundCheck };
    case "keycloak":
      return { key, icon: LockKeyhole };
    case "oidc":
      return { key, icon: Fingerprint };
    case "oauth2":
      return { key, icon: KeyRound };
    case "saml":
      return { key, icon: ShieldCheck };
    case "ldap":
      return { key, icon: Users };
    default:
      return { key: "sso", icon: ShieldCheck };
  }
}

export function LoginView({
  loading,
  error,
  baseURL,
  identityProviders,
  oauthReturnURL,
  theme,
  onThemeToggle,
  onLogin,
}: {
  loading: boolean;
  error: string;
  baseURL: string;
  identityProviders: LoginIdentityProvider[];
  oauthReturnURL: string;
  theme: "light" | "dark";
  onThemeToggle: () => void;
  onLogin: (identity: string, password: string) => void;
}) {
  const [identity, setIdentity] = useState("");
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onLogin(identity, password);
  }

  const ssoListClassName = [
    "login-sso-list",
    identityProviders.length > 1 ? "multi" : "",
    identityProviders.length > 1 ? `count-${Math.min(identityProviders.length, 3)}` : "",
  ].filter(Boolean).join(" ");

  return (
    <main className="login-shell" data-theme={theme}>
      <button className="login-theme-toggle" onClick={onThemeToggle} title={tx("切换主题")} type="button">
        {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
      </button>
      <section className="login-stage">
        <aside className="login-hero-panel" aria-label="TokenHub">
          <div className="login-hero-orbit" aria-hidden="true" />
          <div className="login-flow-scene">
            <svg className="login-flow-svg" viewBox="0 0 460 320" aria-hidden="true">
              <path id="login-key-flow-one" className="login-flow-link" d="M 92 84 C 150 84 165 138 208 151" />
              <path id="login-key-flow-two" className="login-flow-link" d="M 92 160 C 142 160 166 160 208 160" />
              <path id="login-key-flow-three" className="login-flow-link" d="M 92 236 C 150 236 165 182 208 169" />
              <path id="login-provider-flow-one" className="login-flow-link login-provider-flow" d="M 260 150 C 298 116 318 82 362 82" />
              <path id="login-provider-flow-two" className="login-flow-link login-provider-flow" d="M 260 160 C 302 160 320 160 362 160" />
              <path id="login-provider-flow-three" className="login-flow-link login-provider-flow" d="M 260 170 C 298 204 318 238 362 238" />
              {[
                ["#login-key-flow-one", "0s"],
                ["#login-key-flow-one", "-1.25s"],
                ["#login-key-flow-two", "-0.35s"],
                ["#login-key-flow-two", "-1.7s"],
                ["#login-key-flow-three", "-0.7s"],
                ["#login-key-flow-three", "-2.05s"],
                ["#login-provider-flow-one", "-0.1s"],
                ["#login-provider-flow-one", "-1.45s"],
                ["#login-provider-flow-two", "-0.65s"],
                ["#login-provider-flow-two", "-2s"],
                ["#login-provider-flow-three", "-1.1s"],
                ["#login-provider-flow-three", "-2.45s"],
              ].map(([path, begin], index) => (
                <circle className="login-flow-token" key={`${path}-${begin}-${index}`} r={4}>
                  <animateMotion dur="3.1s" begin={begin} repeatCount="indefinite">
                    <mpath href={path} />
                  </animateMotion>
                </circle>
              ))}
            </svg>

            <div className="login-key-stack" aria-hidden="true">
              <span className="login-key-chip key-one">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 01
              </span>
              <span className="login-key-chip key-two">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 02
              </span>
              <span className="login-key-chip key-three">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 03
              </span>
            </div>

            <div className="login-hub-node">
              <span className="login-logo-tile">
                <img src="/brand/tokenhub-logo.png" alt="" />
              </span>
              <span className="login-hub-copy">
                <strong>TokenHub</strong>
                <small>Gateway</small>
              </span>
            </div>

            <div className="login-provider-stack" aria-hidden="true">
              <span className="login-provider-node provider-one">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider A</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
              <span className="login-provider-node provider-two">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider B</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
              <span className="login-provider-node provider-three">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider C</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
            </div>
          </div>
          <div className="login-signal-strip" aria-hidden="true">
            <span />
            <span />
            <span />
          </div>
        </aside>

        <form className="login-card" onSubmit={submit}>
          <div className="login-card-head">
            <h1>{tx("登录控制台")}</h1>
          </div>
          <label className="field">
            <span>{tx("账号 / 邮箱")}</span>
            <input value={identity} onChange={(event) => setIdentity(event.target.value)} required />
          </label>
          <label className="field">
            <span>{tx("密码")}</span>
            <span className="password-field">
              <input
                value={password}
                type={passwordVisible ? "text" : "password"}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <button
                aria-label={passwordVisible ? tx("隐藏密码") : tx("显示密码")}
                className="password-toggle"
                onClick={() => setPasswordVisible((value) => !value)}
                type="button"
              >
                {passwordVisible ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </span>
          </label>
          <div className="login-helper-row">
            <span>
              <span className="login-checkmark">
                <Check size={13} />
              </span>
              {tx("保持登录")}
            </span>
            <button type="button">{tx("忘记密码？")}</button>
          </div>
          {error ? <div className="login-error">{error}</div> : null}
          <button className="button login-submit" disabled={loading} type="submit">
            {loading ? tx("登录中") : tx("登录控制台")}
          </button>
          {identityProviders.length > 0 ? (
            <>
              <div className="login-divider" aria-hidden="true">
                <span />
                <small>{tx("或")}</small>
                <span />
              </div>
              <div className={ssoListClassName}>
                {identityProviders.map((provider) => {
                  const displayName = loginIdentityProviderDisplayName(provider);
                  return (
                    <a
                      aria-disabled={loading}
                      aria-label={`${tx("使用")} ${displayName} ${tx("登录")}`}
                      className="login-sso-button"
                      href={identityProviderLoginURL(baseURL, provider, oauthReturnURL)}
                      key={provider.id}
                      onClick={(event) => {
                        if (loading) {
                          event.preventDefault();
                          return;
                        }
                        savePendingOAuthBaseURL(baseURL);
                      }}
                    >
                      <LoginIdentityProviderIcon provider={provider} />
                      <span className="login-sso-label">
                        {identityProviders.length > 1 ? displayName : `${tx("使用")} ${displayName} ${tx("登录")}`}
                      </span>
                    </a>
                  );
                })}
              </div>
            </>
          ) : null}
        </form>
      </section>
    </main>
  );
}

export function ResetPasswordView({
  loading,
  error,
  theme,
  onThemeToggle,
  token,
  onReset,
}: {
  loading: boolean;
  error: string;
  theme: "light" | "dark";
  onThemeToggle: () => void;
  token: string;
  onReset: (token: string, password: string) => void;
}) {
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const mismatch = confirmPassword !== "" && password !== confirmPassword;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mismatch || password.length < 8) return;
    onReset(token, password);
  }

  return (
    <main className="login-shell" data-theme={theme}>
      <button className="login-theme-toggle" onClick={onThemeToggle} title={tx("切换主题")} type="button">
        {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
      </button>
      <section className="login-stage">
        <form className="login-card" onSubmit={submit}>
          <div className="login-card-head">
            <h1>{tx("重置密码")}</h1>
            <p>{tx("请设置新的控制台登录密码。")}</p>
          </div>
          <label className="field">
            <span>{tx("新密码")}</span>
            <span className="password-field">
              <input
                minLength={8}
                value={password}
                type={passwordVisible ? "text" : "password"}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <button
                aria-label={passwordVisible ? tx("隐藏密码") : tx("显示密码")}
                className="password-toggle"
                onClick={() => setPasswordVisible((value) => !value)}
                type="button"
              >
                {passwordVisible ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </span>
          </label>
          <label className="field">
            <span>{tx("确认新密码")}</span>
            <input
              minLength={8}
              value={confirmPassword}
              type={passwordVisible ? "text" : "password"}
              onChange={(event) => setConfirmPassword(event.target.value)}
              required
            />
          </label>
          {password !== "" && password.length < 8 ? <div className="login-error">{tx("密码至少 8 位")}</div> : null}
          {mismatch ? <div className="login-error">{tx("两次输入的密码不一致")}</div> : null}
          {error ? <div className="login-error">{error}</div> : null}
          <button className="button login-submit" disabled={loading || mismatch || password.length < 8} type="submit">
            {loading ? tx("提交中") : tx("重置密码")}
          </button>
        </form>
      </section>
    </main>
  );
}
