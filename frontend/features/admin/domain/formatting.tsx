import { type ApiExampleLanguage, type AppData, type Model, type ModelRoute, type PlaygroundChatPayload, type ProviderCatalogModel, routeViews, type ViewKey } from "../core/types";
import { modelCategory } from "./catalog";
import { modelDisplayName } from "./model-display-name";
import { codexImageCapableResources, findProvider, findProviderResource, isCodexSubscriptionImageModel, modelRoutesFor, stringifyForm, stringifyValue } from "./entities";
import { guardrailBlockedDiagnostic, languageLocale, tx } from "../i18n/runtime";
import { preferredModelCategories } from "./model-categories";

export function initialView(): ViewKey {
  if (typeof window === "undefined") return "overview";
  return viewFromPath(window.location.pathname);
}

export function viewFromPath(pathname: string): ViewKey {
  const normalized = pathname.replace(/^\/+|\/+$/g, "");
  if (!normalized) return "overview";
  return routeViews[normalized] ?? "overview";
}

export function playgroundModels(data: AppData, sortByRoutes = data.routes.length > 0) {
  return data.models
    .filter((model) => model.status === "active" && (model.modality === "" || model.modality === "chat"))
    .sort((a, b) => {
      const routeDiff = sortByRoutes ? activeRouteCount(b.name, data) - activeRouteCount(a.name, data) : 0;
      return routeDiff || modelCategoryRank(a) - modelCategoryRank(b) || a.name.localeCompare(b.name);
    });
}

export const apiExampleLanguages: Array<{ key: ApiExampleLanguage; label: string }> = [
  { key: "python", label: "Python" },
  { key: "typescript", label: "TypeScript" },
  { key: "java", label: "Java" },
  { key: "go", label: "Go" },
];

export function apiExampleScripts(baseURL: string, modelName: string, supportsImages = false): Record<ApiExampleLanguage, string> {
  const normalizedBaseURL = apiGatewayBaseURL(baseURL);
  const model = modelName || "gpt-4.1-mini";
  const systemPrompt = tx("你是企业内部 AI 助手。");
  const prompt = tx("请用三句话介绍 TokenHub。");
  const imagePrompt = tx("请描述这张图片。");
  const pythonUserMessage = supportsImages
    ? `{"role": "user", "content": [
            {"type": "text", "text": "${imagePrompt}"},
            {"type": "image_url", "image_url": {"url": "https://example.com/campus.jpg"}},
        ]}`
    : `{"role": "user", "content": "${prompt}"}`;
  const typescriptUserMessage = supportsImages
    ? `{ role: "user", content: [
      { type: "text", text: "${imagePrompt}" },
      { type: "image_url", image_url: { url: "https://example.com/campus.jpg" } },
    ] }`
    : `{ role: "user", content: "${prompt}" }`;
  return {
    python: `from openai import OpenAI

client = OpenAI(
    api_key="YOUR_TOKENHUB_API_KEY",
    base_url="${normalizedBaseURL}"
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "system", "content": "${systemPrompt}"},
        ${pythonUserMessage},
    ],
    temperature=0.7,
)

print(response.choices[0].message.content)`,
    typescript: `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY ?? "YOUR_TOKENHUB_API_KEY",
  baseURL: "${normalizedBaseURL}",
});

const response = await client.chat.completions.create({
  model: "${model}",
  messages: [
    { role: "system", content: "${systemPrompt}" },
    ${typescriptUserMessage},
  ],
  temperature: 0.7,
});

console.log(response.choices[0]?.message?.content);`,
    java: `import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OpenAIOkHttpClient;
import com.openai.models.ChatModel;
import com.openai.models.chat.completions.ChatCompletionCreateParams;

public class TokenHubExample {
  public static void main(String[] args) {
    OpenAIClient client = OpenAIOkHttpClient.builder()
        .apiKey(System.getenv().getOrDefault("TOKENHUB_API_KEY", "YOUR_TOKENHUB_API_KEY"))
        .baseUrl("${normalizedBaseURL}")
        .build();

    ChatCompletionCreateParams params = ChatCompletionCreateParams.builder()
        .model(ChatModel.of("${model}"))
        .addSystemMessage("${systemPrompt}")
        .addUserMessage("${prompt}")
        .temperature(0.7)
        .build();

    System.out.println(client.chat().completions().create(params).choices().get(0).message().content().orElse(""));
  }
}`,
    go: `package main

import (
	"context"
	"fmt"
	"os"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("TOKENHUB_API_KEY")),
		option.WithBaseURL("${normalizedBaseURL}"),
	)

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "${model}",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("${systemPrompt}"),
			openai.UserMessage("${prompt}"),
		},
		Temperature: openai.Float(0.7),
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}`,
  };
}

export function apiGatewayBaseURL(baseURL: string) {
  const trimmed = (baseURL || "http://localhost:8080").replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

export function activeRouteCount(modelName: string, data: AppData) {
  return data.routes.filter((route) => route.model_name === modelName && route.status === "active").length;
}

export type ModelAvailabilityTone = "ready" | "warning" | "blocked" | "restricted";

export type ModelAvailabilitySummary = {
  tone: ModelAvailabilityTone;
  label: string;
  detail: string;
  totalRoutes: number;
  activeRoutes: number;
  healthyRoutes: number;
};

export function modelAvailabilitySummary(model: Model, data: AppData, readOnly = false): ModelAvailabilitySummary {
  const routes = modelRoutesFor(model, data);
  const activeRoutes = routes.filter((route) => route.status === "active");
  const healthyRoutes = activeRoutes.filter((route) => isCodexSubscriptionImageModel(model)
    ? codexImageRouteHasHealthyTarget(route, data)
    : routeHasHealthyTarget(route, data));
  if (model.status !== "active") {
    return {
      tone: "blocked",
      label: "模型未启用",
      detail: "模型目录状态不是启用，前台不会作为可调用模型。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (readOnly && routes.length === 0) {
    return {
      tone: "restricted",
      label: "按权限可见",
      detail: "当前账号可见此模型；实际调用还会受项目 Key 白名单和运行时路由策略限制。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (routes.length === 0) {
    return {
      tone: "blocked",
      label: "未配置路由",
      detail: "管理员需要在路由策略中把该模型映射到一个 Provider 上游模型。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (activeRoutes.length === 0) {
    return {
      tone: "blocked",
      label: "路由未启用",
      detail: "已有 Provider 线路，但线路状态未启用，运行时不会命中。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (healthyRoutes.length === 0) {
    return {
      tone: "warning",
      label: "线路需检查",
      detail: "启用线路存在，但 Provider 或账号资源不是健康启用状态。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  return {
    tone: "ready",
    label: "可调用",
    detail: `${healthyRoutes.length} 条健康启用线路，调用时仍会受项目 Key 白名单和额度限制。`,
    totalRoutes: routes.length,
    activeRoutes: activeRoutes.length,
    healthyRoutes: healthyRoutes.length,
  };
}

export function routeHasHealthyTarget(route: ModelRoute, data: AppData) {
  const provider = findProvider(data, route.provider_id);
  if (!provider || provider.status !== "active" || provider.healthy === false) return false;
  if (route.provider_resource_id) {
    const resource = findProviderResource(data, route.provider_resource_id);
    return Boolean(resource && resource.status === "active" && resource.healthy !== false);
  }
  const group = stringifyValue(route.resource_group);
  if (group) {
    const groupedResources = data.providerResources.filter((resource) =>
      resource.provider_id === route.provider_id && resource.group === group && resource.status === "active",
    );
    if (groupedResources.length > 0) return groupedResources.some((resource) => resource.healthy !== false);
  }
  return true;
}

function codexImageRouteHasHealthyTarget(route: ModelRoute, data: AppData) {
  const capableResources = codexImageCapableResources(data).filter((resource) => resource.provider_id === route.provider_id);
  if (route.provider_resource_id) {
    return capableResources.some((resource) => resource.id === route.provider_resource_id);
  }
  const group = stringifyValue(route.resource_group);
  return capableResources.some((resource) => !group || resource.group === group);
}
export function keyWizardModelOptions(data: AppData) {
  const activeChatModels = playgroundModels(data, data.routes.length > 0);
  const routed = activeChatModels.filter((model) => data.routes.length === 0 || activeRouteCount(model.name, data) > 0);
  const codexImageModels = data.models.filter((model) =>
    model.status === "active" && isCodexSubscriptionImageModel(model) && activeRouteCount(model.name, data) > 0,
  );
  return [...(routed.length > 0 ? routed : activeChatModels), ...codexImageModels].sort((left, right) =>
    modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name),
  );
}

export function modelCategoryRank(model: Model) {
  const index = preferredModelCategories.indexOf(modelCategory(model));
  return index >= 0 ? index : preferredModelCategories.length;
}

export function uniqueUIID(prefix: string) {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}_${crypto.randomUUID()}`;
  }
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`;
}

export async function readAPIError(resp: Response) {
  const payload = await resp.json().catch(() => null);
  const code = payload?.error?.code || payload?.error?.type || `HTTP ${resp.status}`;
  const message = payload?.error?.message || "请求失败";
  if (code === "provider_unavailable") return "该模型暂无可用路由，请先在路由策略中配置启用线路。";
  if (code === "provider_not_configured") return "命中的 Provider 尚未配置 Base URL 或凭证。";
  if (code === "provider_resource_concurrency_exceeded") return "Provider 资源并发已满，请稍后再试。";
  if (code === "provider_resource_cooling_down") return "Provider 资源处于冷却中，请检查资源健康状态。";
  if (code === "guardrail_blocked") {
    const categories: string[] = Array.isArray(payload?.error?.details?.categories) ? payload.error.details.categories.filter((category: unknown): category is string => typeof category === "string") : [];
    const labels: string[] = Array.from(new Set<string>(categories.map(guardrailCategoryLabel)));
    const rawMatches: unknown[] = Array.isArray(payload?.error?.details?.policy_matches) ? payload.error.details.policy_matches : [];
    const policyLabels = Array.from(new Set(rawMatches.map(guardrailPolicyMatchLabel).filter((label): label is string => Boolean(label)))).slice(0, 3);
    const requestID = typeof payload?.request_id === "string" ? payload.request_id.trim() : "";
    return guardrailBlockedDiagnostic(labels.map((label) => tx(label)), policyLabels, requestID);
  }
  return `${message} (${code})`;
}

function guardrailPolicyMatchLabel(value: unknown) {
  if (!value || typeof value !== "object") return "";
  const match = value as Record<string, unknown>;
  const policyName = typeof match.policy_name === "string" ? match.policy_name.trim() : "";
  const itemName = typeof match.detection_item_name === "string" ? match.detection_item_name.trim() : "";
  return [policyName, itemName].filter(Boolean).join(" / ");
}

function guardrailCategoryLabel(category: string) {
  const labels: Record<string, string> = {
    credential: "云凭据与访问密钥", email: "邮箱地址", phone: "手机号码", cn_id_card: "中国身份证号",
    bank_card: "银行卡号", person_name: "姓名", address: "地址", birth_date: "出生日期",
    pattern: "关键词或正则匹配", unsafe: "模型判定为不安全", controversial: "模型判定为争议内容",
  };
  return labels[category] ?? category;
}

export function extractAssistantText(payload: PlaygroundChatPayload) {
  const choice = payload.response?.choices?.[0];
  const content = choice?.message?.content ?? choice?.text ?? payload.response?.output_text ?? payload.response?.content;
  return stringifyChatContent(content);
}

export function stringifyChatContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((item) => {
        if (typeof item === "string") return item;
        if (item && typeof item === "object") {
          const record = item as Record<string, unknown>;
          return stringifyChatContent(record.text ?? record.content ?? record.value);
        }
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  if (content == null) return "";
  if (typeof content === "object") return JSON.stringify(content, null, 2);
  return String(content);
}

export function formatNumber(value: number) {
  return new Intl.NumberFormat(languageLocale()).format(value || 0);
}

export function compactNumber(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`;
  return formatNumber(value || 0);
}

export function formatMoney(value: number) {
  return (value || 0).toFixed(value >= 1 ? 2 : 6);
}

export function formatDashboardMoney(value: number) {
  const amount = Math.max(0, value || 0);
  if (amount === 0) return "0.00";
  if (amount >= 1_000_000) return `${(amount / 1_000_000).toFixed(amount >= 10_000_000 ? 1 : 2)}M`;
  if (amount >= 1_000) return `${(amount / 1_000).toFixed(amount >= 10_000 ? 1 : 2)}K`;
  if (amount >= 1) return amount.toFixed(2);
  if (amount >= 0.01) return amount.toFixed(4);
  return "<0.01";
}

export function modelCapabilities(model: ProviderCatalogModel) {
  return [
    ...(model.capabilities ?? []),
    ...(model.supported_parameters ?? []).map((item) => `param:${item}`),
  ].slice(0, 8);
}

export function formatModelPrice(model: ProviderCatalogModel) {
  const input = model.input_price_usd_per_1m ?? 0;
  const output = model.output_price_usd_per_1m ?? 0;
  if (!input && !output) return "$-";
  return `$${formatMoney(input)}/$${formatMoney(output)}`;
}

export function modelToForm(item: Model) {
  return {
    ...stringifyForm(item),
    display_name: modelDisplayName(item.metadata, ""),
    cache_read_price_usd_per_1m: item.cache_read_price_usd_per_1m
      ? String(item.cache_read_price_usd_per_1m)
      : "",
    capabilities: (item.capabilities ?? []).join(", "),
    supported_parameters: (item.supported_parameters ?? []).join(", "),
    input_modalities: (item.input_modalities ?? []).join(", "),
    output_modalities: (item.output_modalities ?? []).join(", "),
  };
}

const byteUnits = ["B", "KB", "MB", "GB"] as const;

export function formatBytes(value: number, locale = languageLocale()) {
  let size = Number.isFinite(value) && value > 0 ? value : 0;
  let index = 0;
  while (size >= 1024 && index < byteUnits.length - 1) {
    size /= 1024;
    index += 1;
  }
  const fractionDigits = index === 0 ? 0 : 2;
  const formatter = new Intl.NumberFormat(locale, { minimumFractionDigits: fractionDigits, maximumFractionDigits: fractionDigits });
  return `${formatter.format(size)} ${byteUnits[index]}`;
}

export function routeStrategyLabel(value?: string) {
  const labels: Record<string, string> = {
    balanced: "平衡",
    adaptive: "自适应",
    quality: "质量优先",
    cost: "成本优先",
    priority_weighted: "优先级 + 权重",
    priority_only: "仅优先级",
  };
  return tx(labels[value || "balanced"] ?? value ?? "平衡");
}

export function formatTime(value: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "medium" }).format(date);
}
