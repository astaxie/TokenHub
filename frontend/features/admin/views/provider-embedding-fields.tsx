import { retrievalSettings, validEmbeddingSpaces } from "../domain/retrieval-settings";
import { ProviderRetrievalGuide } from "./provider-retrieval-guide";
import { tx } from "../i18n/runtime";
export function ProviderEmbeddingFields({ values, onUpdate }: { values: Record<string, string>; onUpdate: (key: string, value: string) => void }) {
  const settings = retrievalSettings(values, "embedding");
  const invalidSpaces = !validEmbeddingSpaces(values.embedding_spaces);
  return <details className="provider-account-runtime">
    <summary><strong>{tx("文本 Embedding 配置")}</strong></summary>
    <p className="field-help">{tx("仅配置当前模型需要的接口；Embedding 与重排彼此独立，无需同时填写。")}</p>
    <div className="provider-form-grid">
      <label className="field"><span>{tx("Embedding 协议")}</span>
        <select value={values.embedding_protocol ?? ""} onChange={(event) => onUpdate("embedding_protocol", event.target.value)}>
          <option value="">{tx("自动选择")}</option>
          {["openai", "cohere", "jina", "voyage", "dashscope", "tei"].map((protocol) => <option key={protocol} value={protocol}>{protocol}</option>)}
        </select>
      </label>
      <label className="field"><span>{tx("Embedding 接口路径")}</span><input aria-label={tx("Embedding 接口路径")} value={values.embedding_path ?? ""} placeholder={settings.defaultPath} ref={(element) => { element?.setCustomValidity(settings.invalidPath ? tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。") : ""); }} title={tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。")} aria-invalid={settings.invalidPath || undefined} onChange={(event) => onUpdate("embedding_path", event.target.value)} /><small className="field-help">{tx("路径留空时使用协议默认值，追加到 Base URL 后；不是文件路径，也不需要填写完整 URL。")}</small></label>
      <label className="field"><span>{tx("向量空间映射")}</span><textarea aria-label={tx("向量空间映射")} ref={(element) => { element?.setCustomValidity(invalidSpaces ? tx("向量空间映射应为模型名到非空空间标识的 JSON 对象；单一上游可留空。") : ""); }} aria-invalid={invalidSpaces || undefined} value={values.embedding_spaces ?? ""} placeholder={'{"upstream-model":"verified-space-id"}'} onChange={(event) => onUpdate("embedding_spaces", event.target.value)} /><small className="field-help">{tx("单一上游通常可留空向量空间映射。仅在确认多个部署兼容时设置相同标识；维度相同不代表兼容，更换空间需要重建索引。")}</small></label>
    </div>
    {settings.invalidPath ? <p className="notice error">{tx("填写以 / 开头的接口路径，不要填写完整 URL、查询参数或父级路径。")}</p> : null}
    {invalidSpaces ? <p className="notice error">{tx("向量空间映射应为模型名到非空空间标识的 JSON 对象；单一上游可留空。")}</p> : null}
    <ProviderRetrievalGuide values={values} kind="embedding" />
  </details>;
}
