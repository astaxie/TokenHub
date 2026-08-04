import { Activity, AlertTriangle, BarChart3, CircleDollarSign, CircleHelp, Gauge, GripVertical, ListOrdered, Save, Scale, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { type AppData, type Model, type ModelRoute, type ModelRoutePolicy, type ModelRoutePolicyRoute, type ModelRouteStrategy } from "../core/types";
import { priceMetric } from "../domain/catalog";
import { findProvider, routeProjectScopeSummary } from "../domain/entities";
import { providerTypeLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";

const strategyOptions: Array<{
  value: ModelRouteStrategy;
  label: string;
  summary: string;
  icon: typeof Gauge;
  badge: string;
  useCase: string;
  behavior: string;
  parameterHelp: string;
  example: string;
}> = [
  {
    value: "priority_weighted",
    label: "固定比例",
    summary: "按 Provider 权重形成目标流量比例",
    icon: BarChart3,
    badge: "最常用",
    useCase: "需要稳定控制供应商份额、合同配额或灰度流量",
    behavior: "按相对权重分流；当前 Provider 失败时自动尝试其他可用线路",
    parameterHelp: "只看权重之间的比例，75/25 与 3/1 的分流效果相同",
    example: "A=75、B=25，约 100 次请求会分配为 75 次和 25 次",
  },
  {
    value: "adaptive",
    label: "自适应",
    summary: "基于近期响应质量自动调整基础权重",
    icon: Activity,
    badge: "自动优化",
    useCase: "Provider 的延迟或稳定性经常变化，希望系统自动调节流量",
    behavior: "以基础权重起步，根据近 15 分钟成功率和成功请求延迟动态调整",
    parameterHelp: "至少 5 次真实调用后开始调整；页面百分比表示基础占比，不是实时占比",
    example: "A、B 基础权重均为 100，A 更快更稳定后会自动获得更多新请求",
  },
  {
    value: "quality",
    label: "质量优先",
    summary: "优先选择质量评分更高的 Provider",
    icon: Gauge,
    badge: "固定排序",
    useCase: "已有人工评测或业务质量分，希望最佳结果优先",
    behavior: "每次先调用质量分最高的 Provider，失败后再按分数从高到低回退",
    parameterHelp: "质量分为 1-100，越高越优先；同分时权重更高的线路优先",
    example: "A=90、B=70，请求先走 A；只有 A 失败时才尝试 B",
  },
  {
    value: "cost",
    label: "成本优先",
    summary: "优先选择成本评分更高的 Provider",
    icon: CircleDollarSign,
    badge: "固定排序",
    useCase: "有明确的成本等级，希望更经济的线路始终优先",
    behavior: "每次先调用成本分最高的 Provider，失败后再按分数从高到低回退",
    parameterHelp: "成本分为 1-100，分数越高表示越省、越优先，需要人工维护",
    example: "A=90（更省）、B=50，请求先走 A；A 失败后再尝试 B",
  },
  {
    value: "priority_only",
    label: "主备顺序",
    summary: "主 Provider 失败后按顺序切换备用 Provider",
    icon: ListOrdered,
    badge: "主备模式",
    useCase: "需要明确的主线路和备用线路，不希望正常情况下分流",
    behavior: "每次都从列表第一条开始，只有失败时才尝试下一条",
    parameterHelp: "不需要配置权重或评分，应用策略后拖动 Provider 调整顺序",
    example: "内部 Provider 主用，外部 Provider 只在内部线路失败时兜底",
  },
  {
    value: "balanced",
    label: "综合评分",
    summary: "按权重、质量分和成本分形成综合分流权重",
    icon: Scale,
    badge: "兼容模式",
    useCase: "已有配置同时使用权重、质量分和成本分，需要保持原有行为",
    behavior: "将权重 + 质量分 + 成本分作为有效权重，再按结果进行概率分流",
    parameterHelp: "三项都会影响概率，最终占比不是固定值，新配置通常不需要选择此模式",
    example: "新模型优先选择固定比例或自适应；综合评分主要用于兼容旧配置",
  },
];

type RouteDraft = Omit<ModelRoutePolicyRoute, "route_id">;

export function modelRoutePolicySignature(routes: ModelRoute[]) {
  return routes.map((route) => [route.id, route.strategy, route.priority, route.weight, route.quality_score, route.cost_score, route.status].join(":")).join("|");
}

export function modelRoutePolicyPayload(strategy: ModelRouteStrategy, routes: ModelRoute[]): ModelRoutePolicy {
  return {
    strategy,
    routes: routes.map((route) => ({
      route_id: route.id,
      weight: positiveOr(route.weight, 100),
      quality_score: positiveOr(route.quality_score, 50),
      cost_score: positiveOr(route.cost_score, 50),
    })),
  };
}

export function ModelRoutingPolicyEditor({
  model,
  routes,
  data,
  loading,
  draggedRouteID,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
  onSave,
}: {
  model: Model;
  routes: ModelRoute[];
  data: AppData;
  loading: boolean;
  draggedRouteID: string;
  onDragStart: (routeID: string) => void;
  onDragEnd: () => void;
  onDrop: (targetRouteID: string) => void;
  onEdit: (route: ModelRoute) => void;
  onDelete: (route: ModelRoute) => void;
  onSave: (model: Model, policy: ModelRoutePolicy) => void;
}) {
  const persistedStrategies = useMemo(() => new Set(routes.map((route) => normalizeStrategy(route.strategy))), [routes]);
  const persistedStrategy = persistedStrategies.values().next().value ?? "priority_weighted";
  const [strategy, setStrategy] = useState<ModelRouteStrategy>(persistedStrategy);
  const [guideOpen, setGuideOpen] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, RouteDraft>>(() => Object.fromEntries(
    modelRoutePolicyPayload(persistedStrategy, routes).routes.map(({ route_id, ...draft }) => [route_id, draft]),
  ));
  const selectedOption = strategyOptions.find((option) => option.value === strategy) ?? strategyOptions[0];
  const guideToggleLabel = tx(guideOpen ? "收起当前策略说明" : "查看当前策略说明");
  const mixedStrategies = persistedStrategies.size > 1;
  const dirty = mixedStrategies || strategy !== persistedStrategy || routes.some((route) => {
    const draft = drafts[route.id];
    return !draft || draft.weight !== positiveOr(route.weight, 100) || draft.quality_score !== positiveOr(route.quality_score, 50) || draft.cost_score !== positiveOr(route.cost_score, 50);
  });
  const invalid = routes.some((route) => {
    const draft = drafts[route.id];
    return !draft || !Number.isFinite(draft.weight) || !Number.isFinite(draft.quality_score) || !Number.isFinite(draft.cost_score) || draft.weight < 1 || draft.quality_score < 1 || draft.quality_score > 100 || draft.cost_score < 1 || draft.cost_score > 100;
  });
  const activeWeight = routes.reduce((total, route) => route.status === "active" ? total + (drafts[route.id]?.weight ?? 0) : total, 0);
  const canReorder = strategy === "priority_only" && persistedStrategy === "priority_only" && !dirty && !mixedStrategies;

  function updateDraft(routeID: string, key: keyof RouteDraft, value: number) {
    setDrafts((current) => ({ ...current, [routeID]: { ...current[routeID], [key]: value } }));
  }

  function savePolicy() {
    onSave(model, {
      strategy,
      routes: routes.map((route) => ({ route_id: route.id, ...drafts[route.id] })),
    });
  }

  return (
    <section className="model-route-policy" aria-label={tx("模型路由策略")}>
      <div className="model-route-policy-head">
        <div>
          <div className="model-route-policy-title">
            <strong>{tx("模型路由策略")}</strong>
            <button
              aria-controls={`route-strategy-panel-${strategy}`}
              aria-expanded={guideOpen}
              aria-label={guideToggleLabel}
              className="icon-button route-policy-help"
              onClick={() => setGuideOpen((open) => !open)}
              title={guideToggleLabel}
              type="button"
            >
              <CircleHelp aria-hidden="true" size={15} />
            </button>
          </div>
          <span>{tx(selectedOption.summary)}</span>
        </div>
        <button className="button route-policy-save" disabled={loading || !dirty || invalid} onClick={savePolicy} type="button">
          <Save size={15} />
          {tx("应用策略")}
        </button>
      </div>

      <div className="route-strategy-tabs" role="tablist" aria-label={tx("模型路由策略")}>
        {strategyOptions.map((option) => {
          const Icon = option.icon;
          return (
            <button
              aria-selected={strategy === option.value}
              aria-controls={`route-strategy-panel-${option.value}`}
              className={strategy === option.value ? "route-strategy-tab active" : "route-strategy-tab"}
              disabled={loading}
              id={`route-strategy-tab-${option.value}`}
              key={option.value}
              onClick={() => setStrategy(option.value)}
              role="tab"
              type="button"
            >
              <Icon size={15} />
              <span>{tx(option.label)}</span>
            </button>
          );
        })}
      </div>

      <div
        aria-labelledby={`route-strategy-tab-${strategy}`}
        aria-live="polite"
        className="route-strategy-guide"
        hidden={!guideOpen}
        id={`route-strategy-panel-${strategy}`}
        role="tabpanel"
      >
        <div className="route-strategy-guide-head">
          <div>
            <strong>{tx(selectedOption.label)}</strong>
            <span>{tx(selectedOption.summary)}</span>
          </div>
          <em>{tx(selectedOption.badge)}</em>
        </div>
        <div className="route-strategy-guide-grid">
          <div>
            <span>{tx("适合场景")}</span>
            <p>{tx(selectedOption.useCase)}</p>
          </div>
          <div>
            <span>{tx("实际行为")}</span>
            <p>{tx(selectedOption.behavior)}</p>
          </div>
          <div>
            <span>{tx("参数怎么填")}</span>
            <p>{tx(selectedOption.parameterHelp)}</p>
          </div>
        </div>
        <div className="route-strategy-guide-example">
          <span>{tx("示例")}</span>
          <strong>{tx(selectedOption.example)}</strong>
        </div>
      </div>

      {mixedStrategies ? (
        <div className="route-policy-warning">
          <AlertTriangle size={15} />
          <span>{tx("当前 Provider 线路策略不一致，应用后将统一为所选模型策略。")}</span>
        </div>
      ) : null}

      {strategy === "priority_weighted" || strategy === "adaptive" ? (
        <div className="route-policy-share-note">{tx("项目作用域过滤后将按可用 Provider 重新计算占比。")}</div>
      ) : null}

      <div className="route-policy-list">
        {routes.map((route, index) => {
          const draft = drafts[route.id];
          const share = route.status === "active" && activeWeight > 0 ? draft.weight / activeWeight * 100 : 0;
          return (
            <ModelRoutePolicyRow
              key={route.id}
              route={route}
              index={index}
              data={data}
              strategy={strategy}
              draft={draft}
              share={share}
              loading={loading}
              dragging={draggedRouteID === route.id}
              canReorder={canReorder}
              onChange={(key, value) => updateDraft(route.id, key, value)}
              onDragStart={() => onDragStart(route.id)}
              onDragEnd={onDragEnd}
              onDrop={() => onDrop(route.id)}
              onEdit={() => onEdit(route)}
              onDelete={() => onDelete(route)}
            />
          );
        })}
      </div>
    </section>
  );
}

function ModelRoutePolicyRow({
  route,
  index,
  data,
  strategy,
  draft,
  share,
  loading,
  dragging,
  canReorder,
  onChange,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
}: {
  route: ModelRoute;
  index: number;
  data: AppData;
  strategy: ModelRouteStrategy;
  draft: RouteDraft;
  share: number;
  loading: boolean;
  dragging: boolean;
  canReorder: boolean;
  onChange: (key: keyof RouteDraft, value: number) => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const provider = findProvider(data, route.provider_id);
  const providerModel = data.providerModels.find((model) => model.provider_id === route.provider_id && model.upstream_model === route.provider_model);
  return (
    <div
      className={dragging ? "route-provider-row route-policy-row dragging" : "route-provider-row route-policy-row"}
      draggable={canReorder && !loading}
      onDragStart={(event) => {
        if (!canReorder) return;
        event.dataTransfer.effectAllowed = "move";
        onDragStart();
      }}
      onDragOver={(event) => {
        if (!canReorder) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
      }}
      onDragEnd={onDragEnd}
      onDrop={(event) => {
        if (!canReorder) return;
        event.preventDefault();
        onDrop();
      }}
    >
      <button className="route-drag-handle" disabled={!canReorder || loading} title={tx(canReorder ? "拖动调整调用顺序" : "顺序故障转移策略下可拖动排序")} type="button">
        <GripVertical size={15} />
      </button>
      <div className="route-order-badge">{routeBadge(strategy, index, share, draft)}</div>
      <div className="route-provider-main">
        <strong>{provider?.name || route.provider_id}</strong>
        <span>{providerTypeLabel(provider?.type)} · {provider?.base_url || tx("未配置 Base URL")}</span>
      </div>
      <div className="route-upstream-model">
        <strong>{route.provider_model}</strong>
        <span>{tx("渠道成本")} {priceMetric(providerModel?.input_price_usd_per_1m)} {tx("输入")} / {priceMetric(providerModel?.output_price_usd_per_1m)} {tx("输出")}</span>
        <span>{routeProjectScopeSummary(route, data)}</span>
      </div>
      <RouteParameterControl strategy={strategy} draft={draft} share={share} disabled={loading} onChange={onChange} />
      <StatusPill status={route.status} />
      <div className="route-row-actions">
        <button className="text-button" onClick={onEdit} type="button">{tx("编辑")}</button>
        <button className="danger-button" onClick={onDelete} title={tx("删除")} type="button">
          <Trash2 size={15} />
        </button>
      </div>
    </div>
  );
}

function RouteParameterControl({
  strategy,
  draft,
  share,
  disabled,
  onChange,
}: {
  strategy: ModelRouteStrategy;
  draft: RouteDraft;
  share: number;
  disabled: boolean;
  onChange: (key: keyof RouteDraft, value: number) => void;
}) {
  if (strategy === "priority_only") {
    return <div className="route-policy-order-value">{tx("从上到下")}</div>;
  }
  const fields: Array<{ key: keyof RouteDraft; label: string; max?: number }> = strategy === "quality"
    ? [{ key: "quality_score", label: "质量评分", max: 100 }]
    : strategy === "cost"
      ? [{ key: "cost_score", label: "成本评分", max: 100 }]
      : strategy === "balanced"
        ? [{ key: "weight", label: "权重" }, { key: "quality_score", label: "质量", max: 100 }, { key: "cost_score", label: "成本", max: 100 }]
        : [{ key: "weight", label: strategy === "adaptive" ? "基础权重" : "流量权重" }];
  return (
    <div className={fields.length > 1 ? "route-policy-parameters multi" : "route-policy-parameters"}>
      {fields.map((field) => (
        <label key={field.key}>
          <span>{tx(field.label)}</span>
          <input
            aria-label={tx(field.label)}
            disabled={disabled}
            max={field.max}
            min={1}
            onChange={(event) => {
              const next = Number(event.target.value);
              onChange(field.key, Number.isFinite(next) ? next : 0);
            }}
            type="number"
            value={draft[field.key]}
          />
        </label>
      ))}
      {strategy === "priority_weighted" || strategy === "adaptive" ? <strong>{formatShare(share)}</strong> : null}
    </div>
  );
}

function normalizeStrategy(value?: string): ModelRouteStrategy {
  return strategyOptions.some((option) => option.value === value) ? value as ModelRouteStrategy : "balanced";
}

function positiveOr(value: number | undefined, fallback: number) {
  return value && value > 0 ? value : fallback;
}

function formatShare(value: number) {
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)}%`;
}

function routeBadge(strategy: ModelRouteStrategy, index: number, share: number, draft: RouteDraft) {
  if (strategy === "priority_weighted" || strategy === "adaptive") return formatShare(share);
  if (strategy === "quality") return `Q${draft.quality_score}`;
  if (strategy === "cost") return `C${draft.cost_score}`;
  if (strategy === "balanced") return `W${draft.weight}`;
  return String(index + 1);
}
