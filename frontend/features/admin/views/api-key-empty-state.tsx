import { ArrowDown, ArrowRight, Box, CheckCircle2, FileKey2, KeyRound, Plus, Sparkles, UserRound } from "lucide-react";
import { tx } from "../i18n/runtime";

const emptyStateModels = ["model-a", "model-b", "model-c"] as const;

export function APIKeyEmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="api-key-empty-state">
      <div className="api-key-empty-heading">
        <div className="api-key-empty-emblem" aria-hidden="true">
          <FileKey2 size={30} strokeWidth={1.7} />
          <Sparkles className="api-key-empty-sparkle" size={13} strokeWidth={2.2} />
        </div>
        <h3>{tx("你还没有创建任何 API Key")}</h3>
        <p>{tx("创建后即可通过 API Key 调用 TokenHub 提供的模型和推理服务。")}</p>
        <button className="button api-key-empty-create" onClick={onCreate} type="button">
          <Plus size={16} />
          {tx("创建 API Key")}
        </button>
      </div>

      <div
        className="api-key-empty-flow"
        role="img"
        aria-label={tx("用户携带 API Key 发起请求，通过鉴权后访问模型服务。")}
      >
        <div className="api-key-flow-node api-key-flow-user">
          <span className="api-key-flow-icon" aria-hidden="true"><UserRound size={19} /></span>
          <strong>{tx("用户")}</strong>
          <span>{tx("发起 API 请求")}</span>
        </div>

        <div className="api-key-flow-link" aria-hidden="true">
          <span>{tx("携带 API Key")}</span>
          <ArrowRight className="api-key-flow-arrow-right" size={16} />
          <ArrowDown className="api-key-flow-arrow-down" size={16} />
        </div>

        <div className="api-key-flow-node api-key-flow-key">
          <span className="api-key-flow-icon accent" aria-hidden="true"><KeyRound size={19} /></span>
          <strong>API Key</strong>
          <span>{tx("验证身份与权限")}</span>
          <small><CheckCircle2 size={15} />{tx("鉴权通过")}</small>
        </div>

        <div className="api-key-flow-branch" aria-hidden="true">
          <span className="api-key-flow-branch-label">{tx("允许访问")}</span>
          <i className="api-key-flow-branch-line top"><ArrowRight size={14} /></i>
          <i className="api-key-flow-branch-line middle"><ArrowRight size={14} /></i>
          <i className="api-key-flow-branch-line bottom"><ArrowRight size={14} /></i>
          <ArrowDown className="api-key-flow-branch-down left" size={14} />
          <ArrowDown className="api-key-flow-branch-down center" size={14} />
          <ArrowDown className="api-key-flow-branch-down right" size={14} />
        </div>

        <div className="api-key-flow-models">
          {emptyStateModels.map((model, index) => (
            <div className="api-key-flow-model" key={model}>
              <span aria-hidden="true"><Box size={15} /></span>
              <strong>{tx(`模型 ${String.fromCharCode(65 + index)}`)}</strong>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
