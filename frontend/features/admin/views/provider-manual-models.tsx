import { useId, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { type ProviderCatalogModel } from "../core/types";
import { validManualModelID } from "../domain/provider-manual-models";
import { tx } from "../i18n/runtime";

export function ProviderManualModels({
  models,
  onChange,
  importedIDs,
}: {
  models: ProviderCatalogModel[];
  onChange: (models: ProviderCatalogModel[]) => void;
  importedIDs?: ReadonlySet<string>;
}) {
  const [upstreamModel, setUpstreamModel] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState("");
  const headingID = useId();

  function addModel() {
    const id = upstreamModel.trim();
    if (!validManualModelID(id)) {
      setError(tx("请输入上游模型 ID，不能包含逗号或换行。"));
      return;
    }
    if (importedIDs?.has(id)) {
      setError(tx("该模型已引入。"));
      return;
    }
    if (models.some((model) => model.id === id)) {
      setError(tx("该手动模型已添加。"));
      return;
    }
    const name = displayName.trim() || id;
    onChange([...models, { id, name, display_name: name }]);
    setUpstreamModel("");
    setDisplayName("");
    setError("");
  }

  return (
    <section className="provider-manual-models" aria-labelledby={headingID}>
      <div>
        <strong id={headingID}>{tx("手动添加模型")}</strong>
        <p>{tx("如果上游不提供模型列表，请填写实际调用时使用的模型 ID。手动添加不会测试连接。")}</p>
      </div>
      <div className="provider-form-grid">
        <label className="field">
          <span>{tx("上游模型 ID")}</span>
          <input
            onChange={(event) => setUpstreamModel(event.target.value)}
            value={upstreamModel}
          />
        </label>
        <label className="field">
          <span>{tx("模型显示名称（可选）")}</span>
          <input onChange={(event) => setDisplayName(event.target.value)} value={displayName} />
        </label>
      </div>
      <button className="secondary-button provider-manual-models-add" onClick={addModel} type="button">
        <Plus size={14} />{tx("添加手动模型")}
      </button>
      {error ? <p className="provider-manual-models-error" role="alert">{error}</p> : null}
      {models.length > 0 ? (
        <ul className="provider-manual-models-list">
          {models.map((model) => (
            <li key={model.id}>
              <span><strong>{model.display_name || model.name}</strong><small>{model.id}</small></span>
              <button
                aria-label={`${tx("移除手动模型")} ${model.id}`}
                className="text-button"
                onClick={() => onChange(models.filter((item) => item.id !== model.id))}
                type="button"
              >
                <Trash2 size={13} />{tx("移除手动模型")}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
