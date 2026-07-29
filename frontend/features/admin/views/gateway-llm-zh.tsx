import { type AppRole } from "../core/types";
import { formatNumber } from "../domain/formatting";
import {
  gatewayAnthropicCountTokensCurl,
  gatewayAnthropicMessagesCurl,
  gatewayClaudeCodeExample,
  gatewayEmbeddingsCurl,
  gatewayListModelsCurl,
  gatewayOpenAISDKExample,
  gatewayPythonSDKExample,
  gatewayResponsesCurl,
  gatewayRetrieveModelCurl,
  gatewayStreamingCurl,
} from "./gateway-llm-en";
import { type GatewayDocBundle, type GatewayDocStats } from "./gateway-view";

export function gatewayChineseLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "接口文档",
      subtitle: "OpenAI 兼容大模型调用",
      searchPlaceholder: "搜索接口、参数或错误码",
      noResults: "没有匹配的接口文档",
    },
    eyebrow: "LLM API Docs",
    title: "调用大语言模型",
    description: teamLeader
      ? "使用项目 Key 调用已批准模型，并在项目维度管理成员权限、额度和成本归因。"
      : "使用项目 API Key 调用 OpenAI 兼容模型接口。先查询可用模型，再发起对话、Responses 或向量请求。",
    languageLabel: "文档语言",
    quickInfoLabel: "接口基础信息",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "示例模型",
      currentConfig: "当前接口范围",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} 个可调用模型`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} 个项目 Key`,
    },
    groups: [
      {
        title: "快速开始",
        items: [
          {
            id: "quickstart",
            group: "快速开始",
            badge: "GUIDE",
            title: "快速接入",
            description: "通过 TokenHub 发起第一笔 OpenAI 兼容的大模型请求。",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "鉴权 Header", value: "Authorization: Bearer <API Key>" },
              { label: "示例模型", value: stats.sampleModel },
              { label: "当前范围", value: teamLeader ? "团队项目" : "已分配项目" },
            ],
            notesTitle: "调用顺序",
            notes: [
              "先在 Key 管理中创建或复制项目 API Key。控制台登录 Token 不能用于 /v1 模型接口。",
              "先调用 GET /v1/models，这个返回值就是当前 Key 可用的模型列表。",
              "从模型列表里选择 model，调用 POST /v1/chat/completions、/v1/responses 或 /v1/embeddings。",
              "调用失败时复制响应里的 request_id，到请求日志里排查。",
            ],
            examplesTitle: "首个请求",
            examples: [
              { title: "查询可用模型", code: gatewayListModelsCurl(stats) },
              { title: "创建聊天对话", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "快速开始",
            badge: "AUTH",
            title: "鉴权方式",
            description: "所有模型接口都使用项目 API Key，通过 Authorization header 传入。",
            table: {
              title: "必填请求头",
              columns: ["Header", "必填", "值"],
              rows: [
                ["Authorization", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST 请求", "application/json"],
              ],
            },
            notesTitle: "权限检查",
            notes: [
              "Key 必须处于启用状态，并且绑定到启用的项目。",
              "请求模型必须对该项目可见，并且至少有一条启用路由。",
              teamLeader
                ? "团队 Leader 发放 Key 时，应选择真实归属项目，确保用量能回到正确团队和成本中心。"
                : "如果你在多个项目中，创建 Key 前先选择应该承担用量和成本的项目。",
            ],
          },
        ],
      },
      {
        title: "大模型 API",
        items: [
          {
            id: "list-models",
            group: "大模型 API",
            method: "GET",
            path: "/v1/models",
            title: "获取模型列表",
            description: "返回当前 API Key 可用的大语言模型列表。该 Endpoint 与 OpenAI API 兼容。",
            params: {
              title: "请求头",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "是", "application/json"],
              ],
            },
            table: {
              title: "模型字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "模型标识符，后续调用时填写到 model 字段。"],
                ["object", "对象类型，通常为 model。"],
                ["created", "模型创建 Unix 时间戳。"],
                ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格。"],
                ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格。"],
                ["title", "模型标题。"],
                ["description", "模型描述。"],
                ["context_size", "模型最大上下文长度。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "大模型 API",
            method: "GET",
            path: "/v1/models/{model}",
            title: "获取指定模型信息",
            description: "返回当前 API Key 可见的单个模型信息。响应字段与 jiekou 兼容模型对象一致。",
            params: {
              title: "路径参数和请求头",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "path", "是", `来自 /v1/models 的模型 ID，例如 ${stats.sampleModel}。`],
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "是", "application/json"],
              ],
            },
            table: {
              title: "响应字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "模型 ID，在 API Endpoints 中引用。"],
                ["created", "模型创建 Unix 时间戳。"],
                ["object", "对象类型，始终为 model。"],
                ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格。"],
                ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格。"],
                ["title", "模型标题。"],
                ["description", "模型描述。"],
                ["context_size", "模型最大上下文长度。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "大模型 API",
            method: "POST",
            path: "/v1/chat/completions",
            title: "创建聊天对话请求",
            description: "根据消息列表生成模型回复，适用于普通对话、工具调用、结构化输出和流式输出。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", `来自 /v1/models 的模型 ID，例如 ${stats.sampleModel}。`],
                ["messages", "array", "是", "由 system、user、assistant 组成的消息数组。"],
                ["max_tokens", "integer", "否", "最大生成 token 数。"],
                ["temperature", "number", "否", "采样温度。"],
                ["reasoning_effort", "string", "否", "推理强度；当前路由不支持时省略。"],
                ["stream", "boolean", "否", "true 时返回 SSE 流，结束标记为 data: [DONE]。"],
                ["tools", "array", "否", "兼容上游模型的函数工具。"],
                ["response_format", "object", "否", "上游支持时可指定 JSON object 或 JSON schema。"],
              ],
            },
            table: {
              title: "响应字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "本次对话请求 ID。"],
                ["choices[].message", "模型返回的 assistant 消息。"],
                ["choices[].finish_reason", "生成停止原因，例如 stop 或 length。"],
                ["usage", "prompt、completion 和 total token 统计。"],
              ],
            },
            examplesTitle: "示例",
            examples: [
              { title: "非流式", code: stats.chatCurl },
              { title: "流式", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "大模型 API",
            method: "POST",
            path: "/v1/responses",
            title: "创建 Responses 请求",
            description: "使用 Responses 风格接口处理简单文本输入，并为后续多模态能力保留统一入口。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", "可调用模型 ID。"],
                ["input", "string | array", "是", "输入文本或结构化输入内容。"],
                ["reasoning.effort", "string", "否", "嵌套推理强度；支持 OpenAI 兼容、Anthropic 和 Gemini 路由。"],
                ["stream", "boolean", "否", "暂不支持；true 时返回 501。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "anthropic-messages",
            group: "大模型 API",
            method: "POST",
            path: "/v1/messages",
            title: "创建 Anthropic Messages 请求",
            description: "供 Claude Code 或 Anthropic 兼容客户端调用，支持文本、图片、客户端工具、工具结果和流式响应。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", "可调用的 TokenHub 模型 ID。"],
                ["max_tokens", "integer", "是", "最大生成 Token 数。"],
                ["messages", "array", "是", "由 user、assistant 和结构化内容块组成的 Anthropic 消息。"],
                ["system", "string | array", "否", "顶层 Anthropic system prompt。"],
                ["tools", "array", "否", "使用 input_schema 定义的客户端工具。"],
                ["tool_choice", "object", "否", "控制自动、强制、指定或停用工具调用。"],
                ["stream", "boolean", "否", "true 时返回 Anthropic 命名 SSE 事件。"],
              ],
            },
            notesTitle: "路由行为",
            notes: [
              "原生 Anthropic 路由保留内容块和 beta Header。",
              "OpenAI 兼容路由转换客户端工具、工具结果、图片和流式事件。",
              "OpenAI 兼容路由遇到不支持的 Anthropic 服务端工具时返回明确的 400 响应。",
            ],
            examplesTitle: "示例",
            examples: [
              { title: "Messages API", code: gatewayAnthropicMessagesCurl(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "anthropic-count-tokens",
            group: "大模型 API",
            method: "POST",
            path: "/v1/messages/count_tokens",
            title: "估算 Anthropic Messages Token",
            description: "完成 Key 认证和模型权限检查后返回确定性的输入 Token 估算值，不计为模型推理请求。",
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayAnthropicCountTokensCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "大模型 API",
            method: "POST",
            path: "/v1/embeddings",
            title: "创建文本向量",
            description: "为搜索、RAG、分类和聚类等场景生成文本向量。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", "当前 Key 可见的向量模型 ID。"],
                ["input", "string | array", "是", "需要向量化的文本。"],
                ["encoding_format", "string", "否", "上游支持时可选 float 或 base64。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "团队接入" : "项目 Key",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "KEY",
            title: teamLeader ? "为项目发放 Key" : "使用项目 Key",
            description: teamLeader
              ? "一个成员可以加入多个项目；每个 Key 应发放到真实归属项目，用于用量、额度和成本归因。"
              : "你可能属于多个项目；创建 Key 时应选择真实归属项目，用于用量、额度和成本归因。",
            table: {
              title: teamLeader ? "团队 Key 发放检查表" : "项目 Key 规则",
              columns: ["项目", "规则"],
              rows: teamLeader ? [
                ["Project", "先创建或选择项目，再发放 Key。"],
                ["Members", "在项目详情侧边栏添加应用负责人。"],
                ["Models", "交付给应用前，用新 Key 调用 GET /v1/models 验证模型范围。"],
                ["Reports", "按成员、项目、模型和成本中心查看团队用量。"],
              ] : [
                ["Project", "每个 API Key 只属于一个项目。"],
                ["Models", "模型列表会按项目和 Key 权限过滤。"],
                ["Secret", "新 Key 只展示一次，请保存到应用密钥管理系统。"],
                ["Usage", "请求会归因到 Key 所属项目和当前账号。"],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "SDK",
            title: "SDK 与 Claude Code",
            description: "OpenAI 兼容 SDK 使用 TokenHub Base URL；Claude Code 使用 TokenHub Host URL。",
            examplesTitle: "客户端示例",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "REF",
            title: "错误码与排查",
            description: "按状态码定位 API Key、项目成员、模型路由和额度问题。",
            table: {
              title: "常见错误",
              columns: ["状态", "常见原因", "处理方式"],
              rows: [
                ["401", "API Key 缺失、格式错误、已停用或已过期。", "检查 Authorization header 和 Key 状态。"],
                ["403", "项目、Key 或模型权限不允许当前请求。", teamLeader ? "检查项目成员、Key 模型范围和团队项目归属。" : "联系团队负责人检查项目成员和模型权限。"],
                ["404/503", "该模型没有可用健康路由。", "请管理员启用路由或检查 Provider 健康状态。"],
                ["429", "项目额度、并发或 Provider 资源限制触发。", teamLeader ? "查看项目额度和并发限制。" : "等待额度恢复或申请提升额度。"],
                ["500", "上游 Provider 或路由错误。", `在请求日志中搜索 request_id。当前可见日志样本：${formatNumber(stats.requestLogCount)} 条。`],
              ],
            },
          },
        ],
      },
    ],
  };
}
