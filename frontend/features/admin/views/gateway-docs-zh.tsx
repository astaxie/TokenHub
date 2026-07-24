import { type GatewayDocBundle } from "./gateway-view";

export function gatewayChineseDocs(stats: GatewayDocBundle): GatewayDocBundle {
  return {
    nav: {
      title: "文档导航",
      subtitle: "按角色和任务查看指南",
      searchPlaceholder: "搜索指南、API 或错误码",
      noResults: "没有匹配的文档",
    },
    eyebrow: "TokenHub Docs",
    title: "面向三种角色的网关指南",
    description: "TokenHub 文档按企业角色组织：普通用户调用已批准模型，团队负责人管理项目和成员，管理员治理 Provider、路由、身份源、审计和成本。",
    languageLabel: "文档语言",
    quickInfoLabel: "接口基础信息",
    quickCards: {
      ...stats.quickCards,
      sampleModel: "示例模型",
      currentConfig: "当前配置",
      activeRoutes: stats.quickCards.activeRoutes.replace("active routes", "条启用路由").replace("active route", "条启用路由"),
      apiKeys: stats.quickCards.apiKeys.replace("API Keys", "个 API Key").replace("API Key", "个 API Key"),
    },
    groups: [
      {
        title: "开始",
        items: [
          {
            id: "overview",
            group: "开始",
            badge: "DOC",
            title: "平台概览",
            description: "理解 TokenHub 如何把模型、项目、Key、路由、审计和成本归因放在同一条治理链路里。",
            details: [
              { label: "主要入口", value: "Model Playground / Key Management / Usage Analytics" },
              { label: "业务接口", value: "/v1/*" },
              { label: "管理接口", value: "/api/admin/*" },
              { label: "数据范围", value: "Personal / Team / Platform" },
            ],
            notesTitle: "上手路径",
            notes: [
              "普通用户从可用模型、Key 管理、个人用量和请求日志开始，不需要理解 Provider 凭证。",
              "团队负责人围绕项目空间工作，在项目详情侧边栏维护成员、Key、额度和费用归属。",
              "管理员接入 Provider，发布模型目录，启用路由策略，配置身份源，并监控审计和成本治理。",
            ],
          },
          {
            ...stats.groups[0].items[1],
            group: "开始",
            title: "核心概念",
            description: "用统一术语解释企业 AI 网关中的资源边界和权限边界。",
            table: {
              title: "概念表",
              columns: ["概念", "含义"],
              rows: [
                ["Project", "企业内部应用或业务空间，是 Key、额度、成员和成本归因的基本单元。"],
                ["API Key", "绑定到 Project 的调用凭证，用于业务应用访问 /v1/* 模型接口。"],
                ["Model Catalog", "对用户展示的标准模型目录，只有配置了启用路由的模型才可调用。"],
                ["Routing Rule", "把标准模型映射到上游 Provider 模型，并定义优先级、权重和策略。"],
                ["Provider", "上游模型服务商或内部模型服务资源，包含 Base URL、凭证和健康状态。"],
                ["Usage Attribution", "请求、Token 和成本会归因到个人、Project、Team 和成本中心。"],
              ],
            },
          },
        ],
      },
      {
        title: "角色指南",
        items: [
          {
            ...stats.groups[1].items[0],
            group: "角色指南",
            title: "普通用户指南",
            description: "普通用户关注可用模型、项目 Key、调用示例、个人用量和请求日志。",
            notesTitle: "日常流程",
            notes: [
              "先在 Available Models 或 Model Playground 确认可调用模型。",
              "在 Key Management 中选择被分配的 Project，再创建或复制业务 API Key。",
              "业务应用只调用 /v1/chat/completions、/v1/responses、/v1/embeddings 等模型接口。",
              "遇到 401/403/429 时，复制 request_id 到 Request Logs 查看原因，或联系团队负责人调整项目权限。",
            ],
            table: {
              title: "普通用户能做什么",
              columns: ["任务", "位置", "说明"],
              rows: [
                ["查看模型", "Available Models", "显示当前账号可调用的模型。"],
                ["测试模型", "Model Playground", "验证提示词、模型返回、路由和成本估算。"],
                ["管理 Key", "Key Management", "Key 必须选择已分配的 Project。"],
                ["查看用量", "Usage Analytics", "只展示当前账号可见的请求、Token 和成本。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[1],
            group: "角色指南",
            title: "团队负责人指南",
            description: "团队负责人负责项目空间、项目成员、Key 发放、团队报表和项目级成本归因。",
            notesTitle: "项目治理流程",
            notes: [
              "在 Project Spaces 中创建或选择项目，项目是成员、Key、额度和成本归属的边界。",
              "点击项目后，在右侧详情栏查看、添加、编辑或移除项目成员。",
              "为项目发放 Key 时，根据成员角色决定是否允许创建业务 Key。",
              "用 Team Reports 查看成员、项目、模型和成本中心维度的消费归因。",
            ],
            table: {
              title: "项目成员角色",
              columns: ["角色", "默认能力"],
              rows: [
                ["Owner", "管理项目设置、成员、Key 和额度。"],
                ["Maintainer", "维护成员和 Key，适合项目技术负责人。"],
                ["Developer", "可以创建和使用项目 Key，适合应用开发者。"],
                ["Viewer", "只能查看项目和用量，不能发放 Key。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[2],
            group: "角色指南",
            title: "管理员指南",
            description: "管理员负责全局 Provider、模型目录、路由策略、身份源、角色权限、安全审计和成本治理。",
            notesTitle: "上线顺序",
            notes: [
              "先在 Provider Channels 配置上游 Base URL、凭证、资源组和健康检查。",
              "在 Model Catalog 中维护对业务开放的标准模型名、能力标签和价格口径。",
              "在 Routing Policies 中为每个可用模型配置至少一条启用路由。",
              "在 System Settings 中配置身份源、角色权限、默认策略、审计保留和企业集成。",
            ],
            table: {
              title: "管理员检查清单",
              columns: ["领域", "检查项"],
              rows: [
                ["Identity", "至少配置一个企业身份源，并保留可控的管理员账号。"],
                ["Routing", "未配置路由的模型需要在后台以不同背景色提示。"],
                ["Security", "API Key 只展示一次，轮换和删除需要审计记录。"],
                ["Cost", "Provider、Project、Team 和 Cost Center 都需要可追踪。"],
              ],
            },
          },
        ],
      },
      {
        title: "API 参考",
        items: [
          {
            ...stats.groups[2].items[0],
            group: "API 参考",
            title: "模型 API",
            description: "使用项目 API Key 调用 OpenAI 兼容的模型接口。",
            params: {
              title: "请求参数",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "是", "统一模型名，例如示例模型卡片中的模型名称"],
                ["messages", "array", "是", "system/user/assistant 消息数组"],
                ["stream", "boolean", "否", "true 时返回 SSE 流式响应"],
              ],
            },
            examplesTitle: "英文样例",
          },
          {
            ...stats.groups[2].items[1],
            group: "API 参考",
            title: "Key 与项目",
            description: "Key 始终属于 Project；一个人可以加入多个 Project，并在创建 Key 时选择归属项目。",
            table: {
              title: "分配模型",
              columns: ["对象", "谁来管理", "说明"],
              rows: [
                ["Project", "管理员或团队负责人", "承载成员、Key、额度和成本归因。"],
                ["Membership", "项目 Owner 或 Maintainer", "决定用户是否可查看项目或发放 Key。"],
                ["API Key", "有权限的项目成员", "只允许调用项目可见且已配置路由的模型。"],
              ],
            },
          },
          {
            ...stats.groups[2].items[2],
            group: "API 参考",
            title: "错误排查",
            description: "按状态码定位 API Key、项目成员、模型路由和额度问题。",
            table: {
              title: "常见错误",
              columns: ["状态", "错误码", "处理方式"],
              rows: [
                ["401", "invalid_api_key", "检查 Authorization 是否使用 TokenHub API Key。"],
                ["403", "project_forbidden / model_not_allowed", "检查用户是否在项目中，以及模型是否对项目开放。"],
                ["404/503", "provider_unavailable", "为模型配置启用路由，或检查上游 Provider 健康状态。"],
                ["429", "quota_exceeded", "检查项目额度、并发限制和 Provider 资源限制。"],
                ["500", "upstream_error", "在 Request Logs 中查看 request_id。"],
              ],
            },
          },
        ],
      },
    ],
  };
}
