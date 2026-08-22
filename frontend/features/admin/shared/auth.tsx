import { Box, Check, Eye, EyeOff, Fingerprint, KeyRound, LockKeyhole, Moon, Pause, Play, ShieldCheck, Sun, UserRound, UserRoundCheck, Users } from "lucide-react";
import { type FormEvent, useRef, useState } from "react";
import { savePendingOAuthLogin } from "../core/session";
import { type LoginIdentityProvider, viewRoutes } from "../core/types";
import { stringifyValue } from "../domain/entities";
import { identityProviderIconLabel } from "../domain/labels";
import { buildOAuthLoginStartURL, createOAuthLoginPKCE } from "../domain/oauth-login";
import { LanguageSelect } from "../i18n/language-switcher";
import { activeLanguage, type AppLanguage, tx } from "../i18n/runtime";

export const identityProviderIconOptions = [
  "auto",
  "dingtalk",
  "feishu",
  "wecom",
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
  userdetail_url?: string;
};

export type IdentityProviderTemplate = {
  key: string;
  label: string;
  providerType: "oidc" | "oauth2";
  iconKey: string;
  loginLabel: string;
  configurationGuideURL: string;
  configurationGuideLabel?: string;
  configurationHelp?: string;
  issuerPlaceholder: string;
  defaultIssuer?: string;
  scopes: string;
  usernameClaim: string;
  emailClaim: string;
  teamClaim: string;
  subjectClaim: string;
  endpoints?: (issuerURL: string) => IdentityProviderEndpointDefaults;
};

export const identityProviderTemplates: IdentityProviderTemplate[] = [
  {
    key: "generic_oidc",
    label: "通用 OIDC",
    providerType: "oidc",
    iconKey: "oidc",
    loginLabel: "SSO",
    configurationGuideURL: "https://openid.net/specs/openid-connect-core-1_0.html",
    configurationGuideLabel: "查看 OIDC 协议参考",
    configurationHelp: "通用模板没有统一的应用管理后台，请查阅实际身份源的应用注册文档以获取 Client ID 和 Client Secret。",
    issuerPlaceholder: "https://sso.example.com",
    scopes: "openid, profile, email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
    subjectClaim: "sub",
  },
  {
    key: "dingtalk",
    label: "钉钉",
    providerType: "oauth2",
    iconKey: "dingtalk",
    loginLabel: "DingTalk",
    configurationGuideURL: "https://open.dingtalk.com/document/orgapp/tutorial-obtaining-user-personal-information",
    issuerPlaceholder: "https://login.dingtalk.com",
    defaultIssuer: "https://login.dingtalk.com",
    scopes: "openid",
    usernameClaim: "unionId",
    emailClaim: "email",
    teamClaim: "",
    subjectClaim: "unionId",
    endpoints: () => ({
      authorize_url: "https://login.dingtalk.com/oauth2/auth",
      token_url: "https://api.dingtalk.com/v1.0/oauth2/userAccessToken",
      userinfo_url: "https://api.dingtalk.com/v1.0/contact/users/me",
    }),
  },
  {
    key: "feishu",
    label: "飞书",
    providerType: "oauth2",
    iconKey: "feishu",
    loginLabel: "Feishu",
    configurationGuideURL: "https://open.feishu.cn/document/common-capabilities/sso/web-application-sso/web-app-overview",
    issuerPlaceholder: "https://open.feishu.cn",
    defaultIssuer: "https://open.feishu.cn",
    scopes: "",
    usernameClaim: "union_id",
    emailClaim: "enterprise_email",
    teamClaim: "",
    subjectClaim: "union_id",
    endpoints: () => ({
      authorize_url: "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
      token_url: "https://open.feishu.cn/open-apis/authen/v2/oauth/token",
      userinfo_url: "https://open.feishu.cn/open-apis/authen/v1/user_info",
    }),
  },
  {
    key: "wecom",
    label: "企业微信",
    providerType: "oauth2",
    iconKey: "wecom",
    loginLabel: "WeCom",
    configurationGuideURL: "https://developer.work.weixin.qq.com/document/path/91022",
    issuerPlaceholder: "https://qyapi.weixin.qq.com",
    defaultIssuer: "https://qyapi.weixin.qq.com",
    scopes: "",
    usernameClaim: "userid",
    emailClaim: "biz_mail",
    teamClaim: "main_department",
    subjectClaim: "userid",
    endpoints: () => ({
      authorize_url: "https://login.work.weixin.qq.com/wwlogin/sso/login",
      token_url: "https://qyapi.weixin.qq.com/cgi-bin/gettoken",
      userinfo_url: "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo",
      userdetail_url: "https://qyapi.weixin.qq.com/cgi-bin/user/get",
    }),
  },
  {
    key: "gitlab",
    label: "GitLab",
    providerType: "oauth2",
    iconKey: "gitlab",
    loginLabel: "GitLab",
    configurationGuideURL: "https://docs.gitlab.com/integration/oauth_provider/",
    issuerPlaceholder: "https://gitlab.example.com",
    scopes: "openid profile email read_user",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
    subjectClaim: "sub",
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
    configurationGuideURL: "https://developers.google.com/identity/protocols/oauth2/web-server",
    issuerPlaceholder: "https://accounts.google.com",
    defaultIssuer: "https://accounts.google.com",
    scopes: "openid profile email",
    usernameClaim: "email",
    emailClaim: "email",
    teamClaim: "hd",
    subjectClaim: "sub",
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
    configurationGuideURL: "https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app",
    issuerPlaceholder: "https://login.microsoftonline.com/{tenant}/v2.0",
    scopes: "openid profile email User.Read",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
    subjectClaim: "sub",
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
    configurationGuideURL: "https://developer.okta.com/docs/guides/sign-into-web-app-redirect/",
    issuerPlaceholder: "https://company.okta.com/oauth2/default",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    subjectClaim: "sub",
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
    configurationGuideURL: "https://www.keycloak.org/docs/latest/server_admin/#assembly-managing-clients_server_administration_guide",
    issuerPlaceholder: "https://keycloak.example.com/realms/company",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    subjectClaim: "sub",
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
    configurationGuideURL: "https://www.rfc-editor.org/info/rfc6749/",
    configurationGuideLabel: "查看 OAuth2 协议参考",
    configurationHelp: "通用模板没有统一的应用管理后台，请查阅实际身份源的应用注册文档以获取 Client ID 和 Client Secret。",
    issuerPlaceholder: "https://oauth.example.com",
    scopes: "profile, email",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
    subjectClaim: "sub",
  },
];

export const identityProviderTemplateOptions = identityProviderTemplates.map((template) => template.key);

export type LoginIdentityProviderIconComponent = React.ComponentType<{ size?: number }>;

export function identityProviderLoginURL(baseURL: string, provider: LoginIdentityProvider, returnURL: string, codeChallenge?: string) {
  return buildOAuthLoginStartURL(baseURL, provider.id, returnURL, codeChallenge);
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
  for (const key of ["dingtalk", "feishu", "wecom", "gitlab", "github", "google", "microsoft", "azure", "entra", "okta", "keycloak"]) {
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
  if (overwrite) next.issuer_url = template.defaultIssuer ?? "";
  else if (template.defaultIssuer && !next.issuer_url) next.issuer_url = template.defaultIssuer;
  const issuer = normalizeIdentityProviderIssuer(next.issuer_url || template.defaultIssuer || "");
  for (const [key, value] of Object.entries({
    login_label: template.loginLabel,
    scopes: template.scopes,
    username_claim: template.usernameClaim,
    email_claim: template.emailClaim,
    team_claim: template.teamClaim,
    subject_claim: template.subjectClaim,
  })) {
    if (overwrite || !next[key]) next[key] = value;
  }
  const endpoints = identityProviderEndpointDefaults(template, issuer);
  for (const key of ["authorize_url", "token_url", "userinfo_url", "userdetail_url"] as const) {
    const value = endpoints[key] ?? "";
    if (overwrite || (value && !next[key])) next[key] = value;
  }
  if (overwrite && template.key !== "wecom") next.agent_id = "";
  return next;
}

export function identityProviderInitialFormValues(values: Record<string, string>, createMode: boolean) {
  const templateKey = inferIdentityProviderTemplateKey(values);
  const next: Record<string, string> = createMode ? applyIdentityProviderTemplate(values, templateKey, false) : { ...values, provider_template: templateKey };
  if (createMode) {
    if (next.client_id === "tokenhub-admin") next.client_id = "";
    if (next.issuer_url === "https://sso.example.com") next.issuer_url = "";
    if (next.redirect_uri === "http://localhost:8080/api/admin/auth/oauth/callback") next.redirect_uri = "";
  }
  if (!next.default_role) next.default_role = "user";
  if (!next.default_project_role) next.default_project_role = "developer";
  return next;
}

export function updateIdentityProviderFormValue(values: Record<string, string>, key: string, value: string) {
  if (key === "provider_template") {
    const currentTemplateKey = inferIdentityProviderTemplateKey(values);
    const next = applyIdentityProviderTemplate(values, value, true);
    if (currentTemplateKey !== next.provider_template) {
      next.client_id = "";
      next.client_secret = "";
      next.agent_id = "";
    }
    return next;
  }
  const next = { ...values, [key]: value };
  if (key === "issuer_url") {
    const template = identityProviderTemplateByKey(next.provider_template || inferIdentityProviderTemplateKey(next));
    const previousEndpoints = identityProviderEndpointDefaults(template, values.issuer_url ?? "");
    const nextEndpoints = identityProviderEndpointDefaults(template, value);
    for (const endpointKey of ["authorize_url", "token_url", "userinfo_url", "userdetail_url"] as const) {
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

export function DingTalkBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 1024 1024" width={size} height={size} aria-hidden="true">
      <path fill="#59adf8" d="M717.6192 682.1376h116.5824l-212.0192 296.3968-5.12-1.792 46.08-194.816h-91.136c10.24-47.2576 20.0704-91.648 30.72-140.1856-25.9072 7.7824-48.5376 12.288-69.1712 21.2992-44.6976 19.456-84.8896 11.1616-121.1392-17.6128a455.68 455.68 0 0 1-66.9696-63.232c-20.48-24.4736-13.9776-38.4512 17.0496-43.6736 65.6896-11.0592 131.584-20.7872 197.5808-33.0752h-41.4208c-56.7808-.5632-113.6128-1.4848-170.3936-1.6896-34.8672 0-59.4432-18.176-80.4864-43.4688a272.5888 272.5888 0 0 1-54.9888-110.848c-6.4512-26.0096.6656-33.1776 27.4944-26.9312 67.84 15.7696 135.5776 32.1024 203.4176 47.9744a1030.1952 1030.1952 0 0 0 105.1648 20.1728c-40.96-13.6192-82.7392-26.112-123.3408-40.96-61.952-22.9376-122.88-47.9232-184.832-71.68A66.56 66.56 0 0 1 201.728 240.64c-24.1152-54.9888-42.1376-111.5648-42.5472-172.288 0-22.1184 7.2192-27.5456 26.2144-18.3296C378.88 143.36 578.9184 222.3104 779.5712 299.3152A288.6656 288.6656 0 0 1 834.56 329.0624c33.3312 22.1696 44.1856 50.1248 26.9312 85.248-35.4304 72.0896-74.3936 142.4384-112.2304 213.3504-9.472 17.4592-20.0192 34.4064-31.6416 54.4768Z" />
    </svg>
  );
}

export function FeishuBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 1024 1024" width={size} height={size} aria-hidden="true">
      <path fill="#133c9a" d="M832.032 367.2048c4.1232.024 8.2432.2656 12.3392.7232a345.9104 345.9104 0 0 1 91.8656 25.368c8.5232 3.8176 10.6288 6.9104 3.2912 14.608a296.456 296.456 0 0 0-51.4608 75.9424c-14.1488 29.776-29.6128 58.928-44.0896 88.576a190.048 190.048 0 0 1-43.992 58.5664c-45.2752 40.9632-98.0512 58.2384-158.264 49.88-69.096-9.5744-134.5408-32.9024-196.3648-63.6672-3.8496-1.9072-6.5808-3.2896-8.8512-4.672a4.4832 4.4832 0 0 1-2.096-3.9424 4.4832 4.4832 0 0 1 2.3584-3.7904l4.2784-2.304c50.0784-26.7488 91.8656-64.0272 132.1376-103.216 17.0112-16.4512 33.3312-33.7248 50.5072-50.0448a291.1264 291.1264 0 0 1 135.2-72.3872c11.12-2.6656 22.3392-4.8368 33.5264-7.2384h.528l23.8208-2.2048" />
      <path fill="#3370ff" d="M348.0288 850.6816c-7.6-.4272-26.3216-2.9936-28.56-3.2896a452.6144 452.6144 0 0 1-139.312-40.7344c-25.4656-11.9104-49.9792-25.96-74.5584-39.648-16.1552-9.016-23.4928-23.032-23.328-42.0496.528-70.3472.528-140.704 0-211.0736-.2624-45.2752-1.5792-90.5488-2.2704-135.792a36.6016 36.6016 0 0 1 1.8752-11.744c2.7312-8.16 8.3584-8.656 13.92-3.2912 6.416 6.1856 11.5152 13.6864 17.8656 19.7408 56.856 56 117.1008 107.296 184.6512 149.5456a1017.56 1017.56 0 0 0 118.4512 65.2464c65.6416 29.8096 132.928 56.0992 203.44 72.7808 62.2848 14.7408 122.8608 5.4624 173.3328-34.0864 15.4-13.1616 23.0336-22.8032 41.2944-47.1184a303.6624 303.6624 0 0 1-31.5552 61.464c-11.7456 18.4912-38.2 43.168-58.368 62.5152-30.6336 29.6128-70.6768 53.632-108.2528 73.9008-40.9632 22.0784-83.5408 39.7136-129.0448 49.3536-23.328 5.824-57.0224 12.504-68.6368 13.1616-2.04-.1648-8.9824 1.4144-12.536 1.12-29.9744 2.2688-48.4656 3.1248-78.408 0Z" />
      <path fill="#00d6b9" d="M219.28 172.912a44.256 44.256 0 0 1 6.2832 0c128.848 0 256.6448 2.072 385.328 2.072.224 0 .4432.0688.6256.1984a303.4976 303.4976 0 0 1 33.1328 33.856c29.0544 28.8896 50.704 78.968 65.5104 109.5024 7.3712 21.0912 18.4912 41.2608 23.7568 64.752v.4288a281.552 281.552 0 0 0-38.2992 15.5968c-37.2144 18.8864-54.1264 32.672-85.0224 63.1072-16.8128 16.4512-31.192 31.2912-53.5328 52.3488-7.008 6.5808-24.8416 23.264-25.1376 22.736-5.9232-10.464-106.08-206.4-307.2832-360.552" />
    </svg>
  );
}

export function WeComBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 1229 1024" width={size} height={size} aria-hidden="true">
      <path fill="#0082ef" d="M690.8 828.8c-72 28.8-148.8 33.6-225.6 28.8-33.6-4.8-67.2-9.6-100.8-19.2-4.8 0-9.6 0-14.4 4.8-43.2 19.2-86.4 43.2-124.8 62.4-14.4 9.6-28.8 9.6-43.2 0s-14.4-24-14.4-43.2c9.6-33.6 9.6-67.2 14.4-100.8 0-4.8-4.8-9.6-4.8-14.4-48-48-86.4-96-115.2-158.4-48-115.2-38.4-230.4 28.8-336C158 137.6 263.6 75.2 388.4 46.4S633.2 32 748.4 89.6c105.6 52.8 182.4 134.4 216 249.6 14.4 43.2 19.2 86.4 14.4 129.6-24-24-52.8-28.8-81.6-14.4 0-28.8 0-57.6-9.6-86.4-19.2-67.2-57.6-120-105.6-163.2-81.6-67.2-182.4-96-288-96-110.4 9.6-206.4 48-283.2 124.8-62.4 62.4-96 139.2-91.2 230.4 4.8 76.8 38.4 139.2 86.4 192l38.4 38.4c19.2 14.4 24 28.8 14.4 48-4.8 19.2-9.6 43.2-14.4 62.4 0 4.8-4.8 9.6 0 9.6 4.8 4.8 9.6 0 9.6 0 24-14.4 52.8-28.8 76.8-48 14.4-9.6 28.8-9.6 48-4.8 81.6 24 168 24 249.6 0 4.8 0 9.6-4.8 9.6 4.8 9.6 28.8 24 48 52.8 62.4Z" />
      <path fill="#0081ee" d="M1170.8 732.8c0 33.6-24 57.6-52.8 62.4-48 9.6-86.4 28.8-120 62.4-9.6 9.6-14.4 9.6-24 4.8-4.8-4.8-4.8-14.4 0-24 33.6-33.6 52.8-76.8 62.4-120 4.8-33.6 38.4-52.8 72-52.8 38.4 4.8 62.4 33.6 62.4 67.2Z" />
      <path fill="#fa6202" d="M926 992c-33.6 0-62.4-24-67.2-52.8-4.8-48-28.8-86.4-62.4-115.2-4.8-4.8-9.6-9.6-4.8-19.2 4.8-14.4 14.4-14.4 24-9.6 9.6 4.8 14.4 14.4 19.2 19.2 28.8 24 62.4 38.4 96 43.2 33.6 4.8 57.6 38.4 52.8 72 4.8 33.6-24 62.4-57.6 62.4Z" />
      <path fill="#fecd00" d="M671.6 742.4c0-33.6 19.2-57.6 52.8-67.2 48-9.6 86.4-28.8 120-62.4 9.6-9.6 19.2-9.6 24 0 4.8 4.8 4.8 14.4-4.8 24-28.8 28.8-48 62.4-57.6 105.6 0 4.8 0 14.4-4.8 19.2-9.6 33.6-38.4 52.8-72 48-33.6-4.8-57.6-33.6-57.6-67.2Z" />
      <path fill="#2cbd00" d="M1002.8 574.4c14.4 28.8 28.8 52.8 48 72 9.6 9.6 9.6 19.2 4.8 24-4.8 9.6-14.4 9.6-24 0-24-28.8-57.6-48-91.2-57.6-9.6-4.8-19.2-4.8-28.8-4.8-19.2-4.8-38.4-14.4-43.2-38.4-9.6-24-9.6-48 9.6-67.2 19.2-24 43.2-28.8 67.2-24 24 9.6 43.2 24 48 52.8 0 14.4 4.8 28.8 9.6 43.2Z" />
    </svg>
  );
}

export function loginIdentityProviderIconConfig(key: string): { key: string; icon: LoginIdentityProviderIconComponent } {
  switch (key) {
    case "dingtalk":
      return { key, icon: DingTalkBrandIcon };
    case "feishu":
      return { key, icon: FeishuBrandIcon };
    case "wecom":
      return { key, icon: WeComBrandIcon };
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

function LoginUtilityActions({
  language,
  theme,
  onLanguageChange,
  onThemeToggle,
}: {
  language: AppLanguage;
  theme: "light" | "dark";
  onLanguageChange: (language: AppLanguage) => void;
  onThemeToggle: () => void;
}) {
  return (
    <div className="login-utility-actions">
      <LanguageSelect className="login-language-select" language={language} onChange={onLanguageChange} />
      <button className="login-theme-toggle" onClick={onThemeToggle} title={tx("切换主题")} type="button">
        {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
      </button>
    </div>
  );
}

export function LoginView({
  loading,
  error,
  baseURL,
  identityProviders,
  oauthReturnURL,
  language,
  theme,
  onLanguageChange,
  onThemeToggle,
  onLogin,
}: {
  loading: boolean;
  error: string;
  baseURL: string;
  identityProviders: LoginIdentityProvider[];
  oauthReturnURL: string;
  language: AppLanguage;
  theme: "light" | "dark";
  onLanguageChange: (language: AppLanguage) => void;
  onThemeToggle: () => void;
  onLogin: (identity: string, password: string) => void;
}) {
  const [identity, setIdentity] = useState("");
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [heroPaused, setHeroPaused] = useState(false);
  const [ssoLoading, setSSOLoading] = useState(false);
  const [ssoError, setSSOError] = useState("");
  const ssoStarting = useRef(false);

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
      <LoginUtilityActions
        language={language}
        theme={theme}
        onLanguageChange={onLanguageChange}
        onThemeToggle={onThemeToggle}
      />
      <section className="login-stage">
        <aside className={`login-hero-panel${heroPaused ? " is-paused" : ""}`} aria-label="TokenHub">
          <div className="login-brand-lockup">
            <span className="login-brand-mark" aria-hidden="true">
              <img src="/brand/tokenhub-logo.png" alt="" />
            </span>
            <span className="login-brand-copy">
              <strong>Token<span>Hub</span></strong>
              <small>{tx("企业 AI 网关")}</small>
            </span>
          </div>

          <div className="login-route-scene" aria-hidden="true">
            <svg className="login-route-svg" viewBox="0 0 460 240">
              <path className="login-route-line inbound" d="M 96 130 H 168" pathLength="1" />
              <path className="login-route-line outbound top" d="M 296 130 H 334 V 72 H 372" pathLength="1" />
              <path className="login-route-line outbound middle" d="M 296 130 H 372" pathLength="1" />
              <path className="login-route-line outbound bottom" d="M 296 130 H 334 V 188 H 372" pathLength="1" />
            </svg>

            <div className="login-route-user">
              <span className="login-route-icon"><UserRound size={19} strokeWidth={1.8} /></span>
              <strong>{tx("用户")}</strong>
              <small>{tx("发起 API 请求")}</small>
            </div>

            <span className="login-route-label request-label">{tx("携带 API Key")}</span>
            <span className="login-route-packet inbound-packet"><KeyRound size={10} strokeWidth={2.5} /></span>

            <div className="login-route-auth">
              <span className="login-route-icon auth"><KeyRound size={21} strokeWidth={2} /></span>
              <strong>API Key</strong>
              <small>{tx("验证身份与权限")}</small>
              <span className="login-route-auth-status"><Check size={12} strokeWidth={2.7} />{tx("鉴权通过")}</span>
            </div>

            <span className="login-route-label access-label">{tx("允许访问")}</span>
            <span className="login-route-packet route-one" />
            <span className="login-route-packet route-two" />
            <span className="login-route-packet route-three" />

            <div className="login-route-models">
              {["A", "B", "C"].map((model, index) => (
                <div className={`login-route-model model-${index + 1}`} key={model}>
                  <span><Box size={15} strokeWidth={1.8} /></span>
                  <strong>{tx(`模型 ${model}`)}</strong>
                </div>
              ))}
            </div>
          </div>

          <button
            aria-label={tx(heroPaused ? "播放动画" : "暂停动画")}
            aria-pressed={heroPaused}
            className="login-motion-toggle"
            onClick={() => setHeroPaused((current) => !current)}
            title={tx(heroPaused ? "播放动画" : "暂停动画")}
            type="button"
          >
            {heroPaused ? <Play size={14} /> : <Pause size={14} />}
          </button>
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
          {error || ssoError ? <div className="login-error">{error || ssoError}</div> : null}
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
                      aria-disabled={loading || ssoLoading}
                      aria-label={`${tx("使用")} ${displayName} ${tx("登录")}`}
                      className="login-sso-button"
                      href={identityProviderLoginURL(baseURL, provider, oauthReturnURL)}
                      key={provider.id}
                      onClick={async (event) => {
                        event.preventDefault();
                        if (loading || ssoStarting.current) {
                          return;
                        }
                        ssoStarting.current = true;
                        setSSOLoading(true);
                        setSSOError("");
                        try {
                          const pkce = await createOAuthLoginPKCE();
                          savePendingOAuthLogin(baseURL, pkce.codeVerifier);
                          window.location.assign(identityProviderLoginURL(baseURL, provider, oauthReturnURL, pkce.codeChallenge));
                        } catch {
                          ssoStarting.current = false;
                          setSSOError(tx("OAuth 登录失败"));
                          setSSOLoading(false);
                        }
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
  language,
  theme,
  onLanguageChange,
  onThemeToggle,
  token,
  onReset,
}: {
  loading: boolean;
  error: string;
  language: AppLanguage;
  theme: "light" | "dark";
  onLanguageChange: (language: AppLanguage) => void;
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
      <LoginUtilityActions
        language={language}
        theme={theme}
        onLanguageChange={onLanguageChange}
        onThemeToggle={onThemeToggle}
      />
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
