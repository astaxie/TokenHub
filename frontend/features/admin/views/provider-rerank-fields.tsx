import { retrievalSettings } from "../domain/retrieval-settings";
import { ProviderRetrievalGuide } from "./provider-retrieval-guide";
import { tx } from "../i18n/runtime";
export function ProviderRerankFields({ values, onUpdate }: { values: Record<string, string>; onUpdate: (key: string, value: string) => void }) {
  const settings = retrievalSettings(values, "rerank");
  return <details className="provider-account-runtime">
    <summary><strong>{tx("文本重排配置")}</strong></summary>
    <p className="field-help">{tx("仅配置当前模型需要的接口；Embedding 与重排彼此独立，无需同时填写。")}</p>
    <div className="provider-form-grid">
      <label className="field"><span>{tx("重排协议")}</span><select value={values.rerank_protocol ?? ""} onChange={(event) => onUpdate("rerank_protocol", event.target.value)}>
        <option value="">{tx("自动选择")}</option>
        {["jina", "cohere", "voyage", "qwen", "dashscope", "tei"].map((protocol) => <option key={protocol} value={protocol}>{protocol}</option>)}
      </select></label>
      <label className="field"><span>{tx("重排接口路径")}</span><input aria-label={tx("重排接口路径")} value={values.rerank_path ?? ""} placeholder={settings.defaultPath} ref={(element) => { element?.setCustomValidity(settings.invalidPath ? tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。") : ""); }} title={tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。")} aria-invalid={settings.invalidPath || undefined} onChange={(event) => onUpdate("rerank_path", event.target.value)} /><small className="field-help">{tx("路径留空时使用协议默认值，追加到 Base URL 后；不是文件路径，也不需要填写完整 URL。")}</small></label>
    </div>
    {settings.invalidPath ? <p className="notice error">{tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。")}</p> : null}
    <ProviderRetrievalGuide values={values} kind="rerank" />
    <p>{tx("本地 BGE、vLLM 或 Xinference 可选择兼容的 jina 协议。阿里云按模型选择 qwen 或 dashscope，并核实地域地址。")}</p>
  </details>;
}
