import { type AdminUIContribution } from "../core/types";
import { activeLanguage, type AppLanguage } from "./runtime";
import { adminUICopyTranslations as translations } from "./admin-ui-copy";

const builtinText: Record<string, (tx: (key: string) => string) => string> = {
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
  if (!builtinPlugins.has(contribution.plugin_id)) return contribution;
  const tx = (key: string) => language === "zh-CN" ? key : translations[language][key] ?? key;
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
