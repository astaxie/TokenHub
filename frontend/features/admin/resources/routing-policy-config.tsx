import { type AdminResource, type AppData, type FieldConfig, type ResourceConfig } from "../core/types";
import { stringifyValue } from "../domain/entities";
import { tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";
import { genericResourceConfig } from "./generic-config";
import { adminMutate } from "./payloads";

const routingPolicyFields: FieldConfig[] = [
  { key: "scope", label: "作用域", type: "select", options: ["unbound", "global", "project", "api_key"], required: true },
  {
    key: "scope_id",
    label: "绑定对象",
    type: "select",
    optionsFromData: routingPolicyScopeOptions,
    visible: (values) => values.scope !== "unbound",
    help: "同一作用域对象只能绑定一个策略；API Key 优先于项目，项目优先于全局。",
  },
  { key: "strategy", label: "路由算法", type: "select", options: ["inherit", "balanced", "adaptive", "cost", "quality", "priority_weighted", "priority_only"], required: true },
  { key: "allowed_provider_ids", label: "允许的 Provider", type: "multi-select", multiSelectOnEdit: true, optionsFromData: (data) => data.providers.map((item) => ({ value: item.id, label: item.name })) },
  { key: "allowed_provider_resource_ids", label: "允许的 Provider 资源", type: "multi-select", multiSelectOnEdit: true, optionsFromData: providerResourceOptions },
  { key: "allowed_models", label: "允许的模型", type: "multi-select", multiSelectOnEdit: true, optionsFromData: (data) => data.models.filter((item) => item.status === "active").map((item) => ({ value: item.name, label: item.name })) },
  { key: "required_tags", label: "必需路由标签，逗号分隔", placeholder: "internal, compliant" },
  { key: "allowed_regions", label: "允许地域，逗号分隔", placeholder: "cn-east, cn-north" },
  { key: "allowed_environments", label: "允许环境，逗号分隔", placeholder: "prod" },
];

export function routingPolicyConfig(): ResourceConfig<AdminResource> {
  const base = genericResourceConfig(
    "routing-policies",
    "作用域路由策略",
    "在访问允许列表之后，按 API Key → 项目 → 全局优先级收窄候选路由；无合格路由时拒绝请求。",
    routingPolicyFields,
  );
  const config: ResourceConfig<AdminResource> = {
    ...base,
    eyebrow: "作用域策略列表",
    createLabel: "新建作用域策略",
    columns: [
      { key: "name", label: "策略" },
      { key: "scope", label: "作用域", render: (item) => tx(routingPolicyScopeLabel(stringifyValue(item.fields?.scope))) },
      { key: "scope_id", label: "绑定对象", render: (item, data) => routingPolicyBindingLabel(item, data) },
      { key: "priority", label: "优先级", render: (item) => stringifyValue(item.fields?.priority) || "0" },
      { key: "constraints", label: "约束", render: (item) => routingPolicyConstraintSummary(item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    actions: [
      {
        label: "绑定",
        title: "选择作用域并绑定策略",
        visible: (item) => stringifyValue(item.fields?.scope) === "unbound",
        modal: (item) => ({ config: routingPolicyBindingConfig(config), item }),
      },
      {
        label: "解绑",
        title: "解除当前作用域绑定，保留策略定义",
        visible: (item) => !["", "unbound"].includes(stringifyValue(item.fields?.scope)),
        run: (ctx, item) => adminMutate(ctx, `/api/admin/routing-policies/${encodeURIComponent(item.id)}/unbind`, "POST", {}),
        doneMessage: (item) => `${item.name} ${tx("已解绑")}`,
      },
    ],
  };
  return config;
}

function routingPolicyBindingConfig(base: ResourceConfig<AdminResource>): ResourceConfig<AdminResource> {
  return {
    ...base,
    title: "选择作用域并绑定策略",
    fields: routingPolicyFields.filter((field) => field.key === "scope" || field.key === "scope_id"),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/routing-policies/${encodeURIComponent(item.id)}/bind`, "POST", {
      scope: values.scope,
      scope_id: values.scope_id,
    }),
    toForm: (item) => ({
      scope: stringifyValue(item.fields?.scope),
      scope_id: stringifyValue(item.fields?.scope_id),
    }),
  };
}

function routingPolicyScopeOptions(data: AppData, _currentUser?: unknown, values?: Record<string, string>) {
  switch (values?.scope) {
    case "global":
      return [{ value: "global", label: tx("全局") }];
    case "project":
      return data.projects.map((item) => ({ value: item.id, label: item.name }));
    case "api_key":
      return data.keys.map((item) => ({ value: item.id, label: `${item.name} · ${item.key_prefix}...${item.key_suffix}` }));
    default:
      return [];
  }
}

function providerResourceOptions(data: AppData) {
  return data.providerResources.map((resource) => {
    const provider = data.providers.find((item) => item.id === resource.provider_id);
    const boundary = [resource.region, resource.environment].filter(Boolean).join(" / ");
    return { value: resource.id, label: `${provider?.name || resource.provider_id} · ${resource.name}${boundary ? ` · ${boundary}` : ""}` };
  });
}

function routingPolicyScopeLabel(scope: string) {
  return ({ api_key: "API Key", project: "项目", global: "全局", unbound: "未绑定" } as Record<string, string>)[scope] || scope || "未绑定";
}

function routingPolicyBindingLabel(item: AdminResource, data: AppData) {
  const scope = stringifyValue(item.fields?.scope);
  const scopeID = stringifyValue(item.fields?.scope_id);
  if (scope === "global") return tx("全局");
  if (scope === "project") return data.projects.find((project) => project.id === scopeID)?.name || scopeID;
  if (scope === "api_key") return data.keys.find((key) => key.id === scopeID)?.name || scopeID;
  return tx("未绑定");
}

function routingPolicyConstraintSummary(item: AdminResource) {
  const fields = item.fields ?? {};
  const parts = [
    ["Provider", fields.allowed_provider_ids],
    ["资源", fields.allowed_provider_resource_ids],
    ["模型", fields.allowed_models],
    ["标签", fields.required_tags],
    ["地域", fields.allowed_regions],
    ["环境", fields.allowed_environments],
  ].flatMap(([label, value]) => {
    const count = Array.isArray(value) ? value.length : stringifyValue(value) ? stringifyValue(value).split(",").filter(Boolean).length : 0;
    return count > 0 ? [`${tx(String(label))} ${count}`] : [];
  });
  return parts.join(" · ") || tx("仅继承路由算法");
}
