import { Boxes, ChevronRight, Plus, Route, ServerCog } from "lucide-react";
import { tx } from "../i18n/runtime";

export type ModelGovernanceStage = "providers" | "models" | "routes";

const governanceSteps: Array<{
  stage: ModelGovernanceStage;
  title: string;
  detail: string;
}> = [
  {
    stage: "providers",
    title: "Provider 渠道",
    detail: "引入上游模型 · 维护真实成本",
  },
  {
    stage: "models",
    title: "模型目录",
    detail: "选择目录模型 · 选择初始线路 · 设置统一价格",
  },
  {
    stage: "routes",
    title: "路由策略",
    detail: "调整优先级、权重与流量策略",
  },
];

export function ModelGovernanceFlow({ currentStage }: { currentStage?: ModelGovernanceStage }) {
  const currentIndex = governanceSteps.findIndex((step) => step.stage === currentStage);
  return (
    <div className="model-governance-flow" aria-label={tx("Provider 渠道、模型目录和路由策略的配置流程")}>
      {governanceSteps.map((step, index) => {
        const state = currentIndex < 0 ? "" : index < currentIndex ? "is-complete" : index === currentIndex ? "is-current" : "is-upcoming";
        return (
          <div
            aria-current={index === currentIndex ? "step" : undefined}
            className={`model-governance-flow-step ${state}`.trim()}
            key={step.stage}
          >
            <span>{index + 1}</span>
            <div>
              <strong>{tx(step.title)}</strong>
              <small>{tx(step.detail)}</small>
            </div>
            {index < governanceSteps.length - 1 ? <ChevronRight size={17} /> : null}
          </div>
        );
      })}
    </div>
  );
}

export function ModelGovernanceEmptyState({
  stage,
  title,
  description,
  actionLabel,
  onAction,
}: {
  stage: ModelGovernanceStage;
  title: string;
  description: string;
  actionLabel?: string;
  onAction?: () => void;
}) {
  const StageIcon = stage === "providers" ? ServerCog : stage === "models" ? Boxes : Route;
  return (
    <div className="model-governance-empty-state">
      <div className="model-governance-empty-heading">
        <div className={`model-governance-empty-emblem ${stage}`} aria-hidden="true">
          <StageIcon size={29} strokeWidth={1.8} />
        </div>
        <h3>{tx(title)}</h3>
        <p>{tx(description)}</p>
        {actionLabel && onAction ? (
          <button className="button model-governance-empty-action" onClick={onAction} type="button">
            <Plus size={16} />
            {tx(actionLabel)}
          </button>
        ) : null}
      </div>
      <ModelGovernanceFlow currentStage={stage} />
    </div>
  );
}
