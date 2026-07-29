import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { type AppData } from "../core/types";
import { formatNumber } from "../domain/formatting";
import { type AppLanguage, countWithUnit, tx } from "../i18n/runtime";

export function gatewayLanguageLabel(language: AppLanguage) {
  if (language === "zh-CN") return "中文";
  if (language === "ja") return "日本語";
  return "English";
}

export function gatewayDocGroups({
  baseURL,
  keyHint,
  sampleModel,
  activeRoutes,
  data,
}: {
  baseURL: string;
  keyHint: string;
  sampleModel: string;
  activeRoutes: number;
  data: AppData;
}): any[] {
  const authHeader = `Authorization: Bearer ${keyHint}`;
  const sampleSystemPrompt = tx("你是企业内部 AI 助手。");
  const sampleIntroPrompt = tx("用两句话介绍 TokenHub");
  const sampleWorkOrderPrompt = tx("总结今天的工单重点");
  const sampleKnowledgeInput = tx("TokenHub 企业知识库");
  const chatCurl = `curl -X POST "${baseURL}/chat/completions" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${sampleModel}",
    "messages": [
      {"role": "system", "content": "${sampleSystemPrompt}"},
      {"role": "user", "content": "${sampleIntroPrompt}"}
    ],
    "temperature": 0.7,
    "stream": false
  }'`;

  return [
    {
      title: "开始",
      items: [
        {
          id: "quickstart",
          group: "开始",
          title: "快速接入",
          description: "用一个项目 API Key 调用 TokenHub 的 OpenAI 兼容模型接口。",
          details: [
            { label: "Base URL", value: baseURL },
            { label: "鉴权方式", value: "Bearer API Key" },
            { label: "示例模型", value: sampleModel },
            { label: "启用路由", value: countWithUnit(activeRoutes || data.summary.active_route_count || 0, "条", "route", "件") },
          ],
          notes: [
            "业务应用只需要调用 /v1/*，不需要也不应该访问 /api/admin/*。",
            "每个 API Key 都会受到项目状态、模型白名单、额度、并发和 Provider 路由策略约束。",
            "排查失败请求时，优先复制响应里的 request_id 到请求日志查看完整链路。",
          ],
          examples: [
            {
              title: "查询可用模型",
              code: `curl "${baseURL}/models" \\
  -H "${authHeader}"`,
            },
            { title: "发起一次对话", code: chatCurl },
          ],
        },
        {
          id: "auth",
          group: "开始",
          title: "认证与权限",
          description: "业务 API 使用项目下发的 API Key；控制台登录令牌不能替代业务 Key。",
          details: [
            { label: "Header", value: "Authorization" },
            { label: "格式", value: "Bearer YOUR_TOKENHUB_API_KEY" },
            { label: "当前 Key 数", value: countWithUnit(data.keys.length || data.summary.api_key_count || 0, "个", "Key", "件") },
          ],
          params: [
            ["Authorization", "header", "是", "项目 API Key，格式为 Bearer sk_xxx"],
            ["Content-Type", "header", "POST 必填", "JSON 请求使用 application/json"],
            ["model", "body", "模型接口必填", "统一模型名，需在 Key 白名单和路由策略中可用"],
          ],
          notes: [
            "401 通常表示 Key 缺失、格式错误、已停用或已过期。",
            "403 通常表示项目状态、模型白名单或权限范围不允许当前调用。",
          ],
        },
      ],
    },
    {
      title: "模型 API",
      items: [
        {
          id: "models",
          group: "模型 API",
          title: "模型列表",
          method: "GET",
          path: "/v1/models",
          description: "按当前 API Key 的权限返回可用统一模型。",
          status: "active",
          params: [
            ["Authorization", "header", "是", "Bearer API Key"],
            ["Content-Type", "header", "是", "application/json"],
          ],
          examples: [{ title: "请求示例", code: `curl "${baseURL}/models" -H "${authHeader}" -H "Content-Type: application/json"` }],
          table: {
            columns: ["字段", "说明"],
            rows: [
              ["id", "统一模型名称，用于后续调用的 model 字段"],
              ["object", "OpenAI 兼容对象类型，通常为 model"],
              ["created", "模型创建 Unix 时间戳"],
              ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格"],
              ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格"],
              ["title", "模型标题"],
              ["description", "模型描述"],
              ["context_size", "模型最大上下文长度"],
            ],
          },
        },
        {
          id: "model-detail",
          group: "模型 API",
          title: "指定模型信息",
          method: "GET",
          path: "/v1/models/{model}",
          description: "按当前 API Key 的权限返回单个可用统一模型。",
          status: "active",
          params: [
            ["model", "path", "是", "来自 /v1/models 的统一模型名"],
            ["Authorization", "header", "是", "Bearer API Key"],
            ["Content-Type", "header", "是", "application/json"],
          ],
          examples: [{ title: "请求示例", code: `curl "${baseURL}/models/${encodeURIComponent(sampleModel)}" -H "${authHeader}" -H "Content-Type: application/json"` }],
          table: {
            columns: ["字段", "说明"],
            rows: [
              ["id", "统一模型名称，用于后续调用的 model 字段"],
              ["created", "模型创建 Unix 时间戳"],
              ["object", "OpenAI 兼容对象类型，始终为 model"],
              ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格"],
              ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格"],
              ["title", "模型标题"],
              ["description", "模型描述"],
              ["context_size", "模型最大上下文长度"],
            ],
          },
        },
        {
          id: "chat",
          group: "模型 API",
          title: "对话补全",
          method: "POST",
          path: "/v1/chat/completions",
          description: "兼容 OpenAI Chat Completions，用于普通对话、工具调用和流式输出。",
          status: "active",
          params: [
            ["model", "string", "是", "统一模型名，例如 " + sampleModel],
            ["messages", "array", "是", "system/user/assistant 消息数组"],
            ["temperature", "number", "否", "采样温度，默认由上游模型决定"],
            ["reasoning_effort", "string", "否", "推理强度；当前路由不支持时省略"],
            ["stream", "boolean", "否", "true 时返回 SSE 流式响应"],
          ],
          examples: [{ title: "cURL", code: chatCurl }],
        },
        {
          id: "responses",
          group: "模型 API",
          title: "Responses API",
          method: "POST",
          path: "/v1/responses",
          description: "兼容新版 Responses 风格调用，适合统一文本输入和多模态能力扩展。",
          status: "active",
          params: [
            ["model", "string", "是", "统一模型名"],
            ["input", "string | array", "是", "用户输入内容"],
            ["reasoning.effort", "string", "否", "嵌套推理强度；支持 OpenAI 兼容、Anthropic 和 Gemini 路由"],
            ["stream", "boolean", "否", "暂不支持；true 时返回 501"],
          ],
          examples: [
            {
              title: "cURL",
              code: `curl -X POST "${baseURL}/responses" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${sampleModel}","input":"${sampleWorkOrderPrompt}"}'`,
            },
          ],
        },
        {
          id: "embeddings",
          group: "模型 API",
          title: "文本向量",
          method: "POST",
          path: "/v1/embeddings",
          description: "转发文本向量生成请求，并记录 Token 与成本归因。",
          status: "active",
          params: [
            ["model", "string", "是", "向量统一模型名"],
            ["input", "string | array", "是", "需要向量化的文本"],
            ["encoding_format", "string", "否", "可选 float/base64，取决于上游模型支持"],
          ],
          examples: [
            {
              title: "cURL",
              code: `curl -X POST "${baseURL}/embeddings" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"text-embedding-3-small","input":"${sampleKnowledgeInput}"}'`,
            },
          ],
        },
        {
          id: "models-current",
          group: "模型 API",
          title: "当前可调用模型",
          description: "根据模型目录和路由策略汇总当前控制台可见模型。",
        },
      ],
    },
    {
      title: "管理 API",
      items: [
        {
          id: "admin-login",
          group: "管理 API",
          title: "控制台登录",
          method: "POST",
          path: "/api/admin/auth/login",
          description: "控制台用户登录接口，不等同于模型 API Key。",
          params: [
            ["identity", "string", "是", "用户名或邮箱"],
            ["password", "string", "是", "控制台密码"],
          ],
          notes: ["管理 API 仅用于控制台和受信任后台程序，不应该暴露给业务应用前端。"],
        },
        {
          id: "admin-providers",
          group: "管理 API",
          title: "Provider 配置",
          method: "GET/POST",
          path: "/api/admin/providers",
          description: "管理上游 Provider、Base URL、凭证和健康状态。",
          table: {
            columns: ["能力", "说明"],
            rows: [
              ["GET", "读取 Provider 列表"],
              ["POST", "新增 Provider 配置"],
              ["PATCH", "更新 Provider 凭证、状态和元信息"],
            ],
          },
        },
        {
          id: "admin-routes",
          group: "管理 API",
          title: "路由策略",
          method: "GET/POST",
          path: "/api/admin/routing-rules",
          description: "维护统一模型到 Provider 资源的优先级、权重和状态。",
          details: [
            { label: "当前路由", value: countWithUnit(data.routes.length, "条", "route", "件") },
            { label: "启用路由", value: countWithUnit(activeRoutes, "条", "active route", "件") },
          ],
        },
        {
          id: "admin-audit",
          group: "管理 API",
          title: "请求日志",
          method: "GET",
          path: "/api/admin/audit/requests",
          description: "按权限查看模型调用日志、命中路由、耗时、Token 和错误信息。",
          details: [
            { label: "日志样本", value: countWithUnit(data.logs.length, "条", "log", "件") },
            { label: "用途", value: "排查 request_id 和上游响应" },
          ],
        },
      ],
    },
    {
      title: "参考",
      items: [
        {
          id: "errors",
          group: "参考",
          title: "常见错误",
          description: "按状态码定位 API Key、模型白名单、路由和额度问题。",
          table: {
            columns: ["状态", "错误码", "处理方式"],
            rows: [
              ["401", "invalid_api_key", "检查 Authorization 是否使用 TokenHub API Key"],
              ["403", "model_not_allowed", "检查 Key 的模型白名单和项目状态"],
              ["404/503", "provider_unavailable", "为统一模型配置启用路由"],
              ["429", "quota_exceeded", "检查项目额度、并发和 Provider 资源限制"],
              ["500", "upstream_error", "在请求日志里查看上游响应和 request_id"],
            ],
          },
        },
        {
          id: "sdk",
          group: "参考",
          title: "SDK 示例",
          description: "使用 OpenAI 兼容 SDK 接入 TokenHub。",
        },
        {
          id: "pipeline",
          group: "参考",
          title: "调用链路",
          description: "从鉴权到 Provider 路由的完整治理链路。",
          table: {
            columns: ["阶段", "能力", "当前数据"],
            rows: [
              ["认证", "Bearer API Key", `${formatNumber(data.keys.length)} keys`],
              ["权限", "模型白名单 + 项目状态", `${formatNumber(data.projects.length)} projects`],
              ["Provider", "上游渠道实例、凭证和健康状态", `${formatNumber(data.providers.length)} providers`],
              ["路由", "对外模型到 Provider 的优先级/权重映射", `${formatNumber(data.routes.length)} rules`],
              ["治理", "额度、审计、成本统计", `${formatNumber(data.logs.length)} logs`],
            ],
          },
        },
      ],
    },
  ];
}

export function apiMethodClass(method?: string) {
  const normalized = (method || "").toLowerCase();
  if (normalized.includes("/") || normalized.includes(",")) return "mixed";
  return normalized || "muted";
}

export function GatewayCopyCard({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  async function copyValue() {
    try {
      await navigator.clipboard?.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }
  return (
    <article className="gateway-copy-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <button className="icon-button subtle" onClick={() => void copyValue()} type="button" title={tx("复制")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
    </article>
  );
}

export function GatewayCodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  async function copyCode() {
    try {
      await navigator.clipboard?.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="gateway-code-block">
      <button className="icon-button subtle" onClick={() => void copyCode()} type="button" title={tx("复制代码")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
      <pre><code>{code}</code></pre>
    </div>
  );
}
