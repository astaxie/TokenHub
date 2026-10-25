import { type AdminUIContribution } from "../core/types";
import { activeLanguage, type AppLanguage } from "./runtime";
import { adminUICopyTranslations as translations } from "./admin-ui-copy";
import { subscriptionTranslations } from "./subscriptions";

const builtinText: Record<string, (tx: (key: string) => string) => string> = {
  "Subscription account quota": (tx) => tx("订阅账号额度"),
  "Account allowance, balance, and reset windows reported by the subscription provider.": (tx) => tx("订阅上游返回的账号额度、余额和重置时间。"),
  "Daily check-in": (tx) => tx("每日签到"),
  "Subscription account options": (tx) => tx("订阅账号选项"),
  "Enable X search": (tx) => tx("启用 X 搜索"),
  "Allow Grok to search X during responses. The provider may charge additional credits.": (tx) => tx("允许 Grok 在回复时搜索 X，可能消耗额外积分。"),
  "Use Standard allowance only": (tx) => tx("仅使用 Standard 额度"),
  "Stop using this account when any Standard allowance window is exhausted. Extra usage will not keep the account in routing.": (tx) => tx("任一 Standard 额度窗口用尽后停止使用此账号，不使用额外付费额度继续请求。"),
  "Zero Data Retention": (tx) => tx("零数据保留"),
  "Use only upstreams that retain no prompts or replies. Requests may cost more and fail when no eligible upstream exists.": (tx) => tx("仅使用不保留提示词和回复的上游，费用可能更高；没有符合条件的上游时请求会失败。"),
  "Plugin Ecosystem": (tx) => tx("插件生态"),
  "Inspect the plugin registry, Admin UI contributions, gateway hooks, and plugin actions.": (tx) => tx("查看插件注册表、管理界面贡献、网关钩子和插件动作。"),
  "Registered plugins": (tx) => tx("已注册插件"),
  "Gateway hooks": (tx) => tx("网关钩子"),
  "UI contributions": (tx) => tx("界面贡献"),
  "Plugin actions": (tx) => tx("插件动作"),
  "Route Plugin Context": (tx) => tx("路由插件上下文"),
  "External model": (tx) => tx("对外模型"),
  "Provider model": (tx) => tx("上游模型"),
  "Route status": (tx) => tx("路由状态"),
  "Plugin Runtime": (tx) => tx("插件运行时"),
  "Admin UI schema": (tx) => tx("管理界面结构版本"),
  "Provider advanced settings": (tx) => tx("上游高级设置"),
  "System prompt transform": (tx) => tx("系统提示词转换"),
  "Provider plugins may declare a default policy; providers without one strip client attribution blocks by default.": (tx) => tx("上游插件可以声明默认策略；未声明的上游默认移除客户端归属信息块。"),
  "Provider resource system prompt transform": (tx) => tx("上游资源系统提示词转换"),
  "OpenAI Codex account setup": (tx) => tx("OpenAI Codex 账号设置"),
  "Start OpenAI Codex OAuth": (tx) => tx("启动 OpenAI Codex OAuth"),
  "OpenAI Codex fingerprint convergence": (tx) => tx("OpenAI Codex 指纹收敛"),
  "Codex fingerprint convergence": (tx) => tx("Codex 指纹收敛"),
  "Rewrite client device and session identifiers to stable account-level values when sharing a subscription account.": (tx) => tx("共享订阅账号时，将客户端设备和会话标识改写为稳定的账号级标识。"),
  "OpenAI Codex image capability": (tx) => tx("OpenAI Codex 图像能力"),
  "OpenAI Codex quota": (tx) => tx("OpenAI Codex 配额"),
  "Resource status": (tx) => tx("资源状态"),
  "Account email": (tx) => tx("账号邮箱"),
  "Image generation": (tx) => tx("图像生成"),
  "Provider resources": (tx) => tx("上游资源"),
};

const builtinPlugins = new Set([
  "tokenhub.admin.plugin-ecosystem",
  "tokenhub.admin.core-provider",
  "tokenhub.provider.openai-codex",
]);

// Translate presentation metadata only; never rewrite identifiers or field values.
export function localizeBuiltinContribution(contribution: AdminUIContribution, language: AppLanguage = activeLanguage): AdminUIContribution {
  const subscription = contribution.plugin_id.startsWith("tokenhub.provider.") && contribution.plugin_id.endsWith("-subscription");
  if (!builtinPlugins.has(contribution.plugin_id) && !subscription) return contribution;
  const tx = (key: string) => language === "zh-CN" ? key : translations[language][key] ?? (subscriptionTranslations[language as "en" | "ja"] as Record<string, string> | undefined)?.[key] ?? key;
  const translate = (value: unknown) => typeof value === "string" ? (Object.hasOwn(builtinText, value) ? builtinText[value](tx) : value) : value;
  const schema = contribution.schema;
  return {
    ...contribution,
    ...(typeof contribution.title === "string" ? { title: translate(contribution.title) as string } : {}),
    ...(schema ? { schema: {
      ...schema,
      ...(typeof schema.description === "string" ? { description: translate(schema.description) } : {}),
      ...(Array.isArray(schema.fields) ? { fields: schema.fields.map((field: unknown) => {
        if (!field || typeof field !== "object" || Array.isArray(field)) return field;
        const value = field as Record<string, unknown>;
        return { ...value, ...Object.fromEntries(["label", "help"].filter((key) => typeof value[key] === "string").map((key) => [key, translate(value[key])])) };
      }) } : {}),
    } } : {}),
  };
}
