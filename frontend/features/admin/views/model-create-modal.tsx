import { Boxes, ChevronRight, CircleCheck, Info, Search, SlidersHorizontal, X } from "lucide-react";
import { type Dispatch, type FormEvent, type SetStateAction, useMemo, useState } from "react";
import { type AdminUser, type AppData, type FieldConfig, type Model, type ResourceConfig } from "../core/types";
import { modelCategory, modelCategoryLabel, priceMetric } from "../domain/catalog";
import { customModelTemplateID, externalModelTemplateValues, filterReferenceModelTemplates, referenceModelTemplates } from "../domain/model-create-wizard";
import { compactNumber } from "../domain/formatting";
import { initialModelRoutes } from "../domain/provider-model-selection";
import { tx } from "../i18n/runtime";
import { FieldInput } from "../shared/ui";
import { ModelBrandIcon } from "./model-catalog";

export function ModelCreateModal({
  config,
  data,
  currentUser,
  values,
  setValues,
  loading,
  onClose,
  onSave,
}: {
  config: ResourceConfig<Model>;
  data: AppData;
  currentUser?: AdminUser | null;
  values: Record<string, string>;
  setValues: Dispatch<SetStateAction<Record<string, string>>>;
  loading: boolean;
  onClose: () => void;
  onSave: (values: Record<string, string>) => void;
}) {
  const [step, setStep] = useState(0);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("all");
  const [selectedTemplate, setSelectedTemplate] = useState("");
  const templates = useMemo(() => referenceModelTemplates(data.models), [data.models]);
  const filteredTemplates = useMemo(
    () => filterReferenceModelTemplates(data.models, query, category),
    [category, data.models, query],
  );
  const categories = useMemo(() => {
    const counts = new Map<string, number>();
    for (const model of templates) {
      const key = modelCategory(model);
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort((left, right) => left[0].localeCompare(right[0]));
  }, [templates]);
  const selectedModel = templates.find((model) => model.name === selectedTemplate);
  const hasInitialRoute = initialModelRoutes(values.initial_provider_models).length > 0;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (step === 0) {
      if (selectedTemplate) setStep(1);
      return;
    }
    if (!hasInitialRoute) return;
    onSave(values);
  }

  function selectTemplate(model?: Model) {
    const next = model?.name ?? customModelTemplateID;
    if (selectedTemplate === next) return;
    setSelectedTemplate(next);
    setValues((current) => ({
      ...current,
      ...externalModelTemplateValues(model ?? { name: "", family: "", modality: "chat" }),
    }));
  }

  function update(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function fieldConfig(key: string, override?: Partial<FieldConfig>) {
    const field = config.fields.find((item) => item.key === key);
    return field ? { ...field, ...override } : undefined;
  }

  function renderField(key: string, override?: Partial<FieldConfig>) {
    const field = fieldConfig(key, override);
    if (!field || !(field.visible?.(values) ?? true)) return null;
    return (
      <FieldInput
        key={key}
        field={field}
        data={data}
        currentUser={currentUser}
        values={values}
        value={values[key] ?? ""}
        editing={false}
        onChange={(value) => update(key, value)}
      />
    );
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal model-create-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("新建对外模型")}</p>
            <h2>{tx(step === 0 ? "从模型目录选择" : "配置 Provider 映射与对外价格")}</h2>
          </div>
          <button className="icon-button" disabled={loading} onClick={onClose} type="button" title={tx("关闭")}><X size={18} /></button>
        </div>

        <div className="wizard-stepper model-create-stepper">
          <button className={`wizard-step ${step === 0 ? "active" : "done"}`} onClick={() => setStep(0)} type="button">
            <span>{step > 0 ? <CircleCheck size={15} /> : "1"}</span>
            <strong>{tx("选择目录模型")}</strong>
          </button>
          <button className={`wizard-step ${step === 1 ? "active" : ""}`} disabled={!selectedTemplate} onClick={() => setStep(1)} type="button">
            <span>2</span>
            <strong>{tx("选择 Provider 模型并定价")}</strong>
          </button>
        </div>

        {step === 0 ? (
          <div className="model-create-catalog-step">
            <div className="model-create-intro">
              <div><strong>{tx("模型参考目录")}</strong><span>{templates.length} {tx("个默认模型，可直接带出能力、上下文和建议价格。")}</span></div>
              <div className="model-create-filters">
                <label className="search-box"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索模型名称、系列或能力")} /></label>
                <select aria-label={tx("模型分类")} value={category} onChange={(event) => setCategory(event.target.value)}>
                  <option value="all">{tx("全部分类")}</option>
                  {categories.map(([key, count]) => <option key={key} value={key}>{tx(modelCategoryLabel(key))} ({count})</option>)}
                </select>
              </div>
            </div>

            <button className={`model-create-template custom ${selectedTemplate === customModelTemplateID ? "selected" : ""}`} onClick={() => selectTemplate()} type="button">
              <span className="model-create-custom-icon"><SlidersHorizontal size={18} /></span>
              <span><strong>{tx("自定义对外模型")}</strong><small>{tx("目录中没有时，从空白配置开始。")}</small></span>
              {selectedTemplate === customModelTemplateID ? <CircleCheck size={18} /> : <ChevronRight size={18} />}
            </button>

            <div className="model-create-template-list">
              {filteredTemplates.length === 0 ? (
                <div className="model-directory-empty compact"><Boxes size={24} /><strong>{tx("没有匹配的目录模型")}</strong></div>
              ) : filteredTemplates.map((model) => {
                const selected = selectedTemplate === model.name;
                const modelCategoryKey = modelCategory(model);
                return (
                  <button className={`model-create-template ${selected ? "selected" : ""}`} key={model.name} onClick={() => selectTemplate(model)} type="button">
                    <ModelBrandIcon category={modelCategoryKey} label={modelCategoryLabel(modelCategoryKey)} compact />
                    <span><strong>{model.name}</strong><small>{model.family || modelCategoryLabel(modelCategoryKey)} · {model.modality || "chat"}</small></span>
                    <span className="model-create-template-metrics"><b>{compactNumber(model.context_window || 0)} ctx</b><small>{priceMetric(model.input_price_usd_per_1m)} / {priceMetric(model.output_price_usd_per_1m)}</small></span>
                    {selected ? <CircleCheck size={18} /> : <ChevronRight size={18} />}
                  </button>
                );
              })}
            </div>
          </div>
        ) : (
          <div className="model-create-config-step">
            <section className="model-create-selection-summary">
              <div>{selectedModel ? <ModelBrandIcon category={modelCategory(selectedModel)} label={selectedModel.name} /> : <span className="model-create-custom-icon"><SlidersHorizontal size={18} /></span>}</div>
              <div><span>{tx(selectedModel ? "已选择目录模型" : "自定义模型")}</span><strong>{selectedModel?.name || values.name || tx("尚未命名")}</strong><small>{tx("模型名可以作为对外别名调整；能力和价格不会随 Provider 线路变化。")}</small></div>
              <button className="secondary-button" onClick={() => setStep(0)} type="button">{tx("重新选择")}</button>
            </section>

            <section className="model-create-section">
              <div className="model-create-section-head"><div><strong>{tx("初始 Provider 线路")}</strong><span>{tx("选择已引入的上游模型，保存时同步创建路由。")}</span></div></div>
              {renderField("initial_provider_models", {
                label: "可用 Provider 模型",
                emptySelectionText: "请选择至少一个已引入的 Provider 模型。",
                required: true,
              })}
            </section>

            <section className="model-create-section">
              <div className="model-create-section-head"><div><strong>{tx("对外模型设置")}</strong><span>{tx("确认客户端使用的名称、能力和状态。")}</span></div></div>
              <div className="model-create-form-grid">
                {renderField("name", { label: "对外模型 ID" })}
                {renderField("display_name")}
                {renderField("category")}
                {renderField("family")}
                {renderField("modality")}
                {renderField("input_modalities")}
                {renderField("context_window")}
                {renderField("status")}
                {renderField("capabilities")}
                {renderField("supported_parameters")}
              </div>
            </section>

            <section className="model-create-section">
              <div className="model-create-section-head"><div><strong>{tx("对外统一价格")}</strong><span>{tx("所有 Provider 线路统一按这里的价格对外计费。")}</span></div></div>
              <div className="model-create-form-grid">
                {renderField("input_price_usd_per_1m")}
                {renderField("cache_read_price_usd_per_1m")}
                {renderField("output_price_usd_per_1m")}
                {renderField("embedding_price_usd_per_1m")}
              </div>
              <div className="model-create-pricing-note"><Info size={17} /><span>{tx("Provider 模型价格继续用于真实成本审计，不会覆盖此处的对外统一价格。")}</span></div>
            </section>
          </div>
        )}

        <div className="modal-actions">
          <button className="secondary-button" disabled={loading} onClick={step === 0 ? onClose : () => setStep(0)} type="button">{tx(step === 0 ? "取消" : "上一步")}</button>
          <button className="button" disabled={loading || !selectedTemplate || (step === 1 && !hasInitialRoute)} type="submit">{tx(step === 0 ? "下一步：选择 Provider 模型" : "创建对外模型")}</button>
        </div>
      </form>
    </div>
  );
}
