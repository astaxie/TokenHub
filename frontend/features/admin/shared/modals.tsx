import { Boxes, Check, KeyRound, ShieldCheck, Sparkles } from "lucide-react";
import { type Dispatch, type FormEvent, type SetStateAction, useMemo, useState } from "react";
import { type AdminResource, type AdminUser, type AppData, type FieldConfig, type ModalState } from "../core/types";
import { findProject, projectOwnerLabel, projectSelectOptions, projectTeamLabel } from "../domain/entities";
import { keyWizardModelOptions, modelAvailabilitySummary } from "../domain/formatting";
import { identityProviderDefaultGrantLabel, identityProviderIconLabel, identityProviderTypeLabel, splitList } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { defaultFormValues } from "../resources/payloads";
import { apiKeyConfig } from "../resources/project-key-config";
import { FieldInput } from "./ui";
import { identityProviderTemplateByKey, identityProviderTemplateHelp, identityProviderTemplates, inferIdentityProviderTemplateKey, loginIdentityProviderIconConfig, updateIdentityProviderFormValue } from "../shell/auth";
import { DetailField } from "../views/audit";

export function IdentityProviderEditModal({
  state,
  data,
  currentUser,
  values,
  setValues,
  loading,
  onClose,
  onSave,
}: {
  state: ModalState<AdminResource>;
  data: AppData;
  currentUser?: AdminUser | null;
  values: Record<string, string>;
  setValues: Dispatch<SetStateAction<Record<string, string>>>;
  loading: boolean;
  onClose: () => void;
  onSave: (values: Record<string, string>) => void;
}) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const templateKey = inferIdentityProviderTemplateKey(values);
  const template = identityProviderTemplateByKey(templateKey);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(values);
  }

  function update(key: string, value: string) {
    setValues((prev) => updateIdentityProviderFormValue(prev, key, value));
  }

  function fieldConfig(key: string, override?: Partial<FieldConfig>) {
    const field = state.config.fields.find((item) => item.key === key);
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
        value={values[key] ?? ""}
        editing={Boolean(state.item)}
        onChange={(value) => update(key, value)}
      />
    );
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal identity-provider-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{state.item ? tx("编辑") : tx("新增")}</p>
            <h2>{tx("身份源")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>

        <div className="identity-provider-body">
          <section className="identity-provider-template-panel">
            <div className="identity-provider-section-head">
              <h3>{tx("选择身份源模板")}</h3>
              <span>{tx("选择后会自动填充协议、登录图标、Scope、Claim 和常见端点。")}</span>
            </div>
            <div className="identity-template-grid">
              {identityProviderTemplates.map((item) => {
                const iconConfig = loginIdentityProviderIconConfig(item.iconKey);
                const Icon = iconConfig.icon;
                return (
                  <button
                    className={template.key === item.key ? "identity-template-card active" : "identity-template-card"}
                    key={item.key}
                    onClick={() => update("provider_template", item.key)}
                    type="button"
                  >
                    <span className={`login-sso-icon ${iconConfig.key}`}><Icon size={16} /></span>
                    <strong>{tx(item.label)}</strong>
                    <em>{identityProviderTypeLabel(item.providerType)}</em>
                    <small>{tx(identityProviderTemplateHelp(item))}</small>
                  </button>
                );
              })}
            </div>
            <div className="identity-template-summary">
              <DetailField label="登录按钮" value={values.login_label || template.loginLabel || template.label} />
              <DetailField label="默认 Scope" value={values.scopes || template.scopes} />
              <DetailField label="必填项" value={tx("Issuer、Client ID、Client Secret、Callback URL")} />
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("连接方式")}</h3>
              <span>{tx(template.label)}</span>
            </div>
            <div className="identity-provider-grid">
              {renderField("name")}
              {renderField("provider_type")}
              {renderField("status")}
              {renderField("issuer_url", { placeholder: template.issuerPlaceholder })}
              {renderField("client_id")}
              {renderField("client_secret", {
                placeholder: state.item ? "留空则不修改" : "",
                help: state.item ? "留空则不修改已保存密钥。" : "来自身份源应用的密钥。",
              })}
              {renderField("redirect_uri")}
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("登录入口")}</h3>
              <span>{identityProviderIconLabel(values.icon_key)} / {values.login_label || values.name || tx("SSO")}</span>
            </div>
            <div className="identity-provider-grid compact">
              {renderField("icon_key")}
              {renderField("login_label")}
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("首次登录授权")}</h3>
              <span>{identityProviderDefaultGrantLabel(data, { ...state.item, fields: values } as AdminResource)}</span>
            </div>
            <div className="identity-provider-grid">
              {renderField("default_role")}
              {renderField("default_team_id")}
              {renderField("default_project_id")}
              {renderField("default_project_role")}
            </div>
          </section>

          <details
            className="identity-provider-advanced"
            open={advancedOpen}
            onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
          >
            <summary>
              <strong>{tx("高级配置")}</strong>
              <span>{tx("端点、Scope 与 Claim 映射")}</span>
            </summary>
            <div className="identity-provider-grid">
              {renderField("authorize_url")}
              {renderField("token_url")}
              {renderField("userinfo_url")}
              {renderField("scopes")}
              {renderField("username_claim")}
              {renderField("email_claim")}
              {renderField("team_claim")}
            </div>
          </details>
        </div>

        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{tx("保存")}</button>
        </div>
      </form>
    </div>
  );
}

export function APIKeyWizardModal({
  data,
  currentUser,
  initialValues,
  loading,
  onClose,
  onCreate,
}: {
  data: AppData;
  currentUser?: AdminUser | null;
  initialValues?: Record<string, string>;
  loading: boolean;
  onClose: () => void;
  onCreate: (values: Record<string, string>) => void;
}) {
  const config = useMemo(() => apiKeyConfig(), []);
  const [step, setStep] = useState(0);
  const [modelScope, setModelScope] = useState<"all" | "selected">(initialValues?.allowed_models ? "selected" : "all");
  const [values, setValues] = useState<Record<string, string>>(() => ({
    ...defaultFormValues(config, data, currentUser),
    status: "active",
    ...(initialValues ?? {}),
  }));
  const projectOptions = projectSelectOptions(data, currentUser);
  const selectedProject = findProject(data, values.project_id);
  const selectableModels = keyWizardModelOptions(data);
  const selectedModels = splitList(values.allowed_models);
  const steps = [
    { title: "选择项目", icon: Boxes },
    { title: "填写用途", icon: KeyRound },
    { title: "模型范围", icon: Sparkles },
    { title: "安全护栏", icon: ShieldCheck },
    { title: "确认发放", icon: Check },
  ];
  const fieldByKey = (key: string, override?: Partial<FieldConfig>) => {
    const field = config.fields.find((item) => item.key === key);
    return field ? { ...field, ...override } : undefined;
  };

  function update(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function renderField(key: string, override?: Partial<FieldConfig>) {
    const field = fieldByKey(key, override);
    if (!field) return null;
    return (
      <FieldInput
        key={key}
        field={field}
        data={data}
        currentUser={currentUser}
        value={values[key] ?? ""}
        editing={false}
        onChange={(value) => update(key, value)}
      />
    );
  }

  function toggleModel(modelName: string) {
    const current = new Set(splitList(values.allowed_models));
    if (current.has(modelName)) current.delete(modelName);
    else current.add(modelName);
    update("allowed_models", Array.from(current).join(", "));
  }

  function canContinue(targetStep = step) {
    if (targetStep === 0) return Boolean(values.project_id);
    if (targetStep === 1) return Boolean(values.name?.trim());
    if (targetStep === 2) return modelScope === "all" || selectedModels.length > 0;
    return true;
  }

  function goNext() {
    if (!canContinue()) return;
    setStep((current) => Math.min(current + 1, steps.length - 1));
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (step < steps.length - 1) {
      goNext();
      return;
    }
    if (!canContinue(0) || !canContinue(1) || !canContinue(2)) return;
    onCreate({
      ...values,
      allowed_models: modelScope === "all" ? "" : values.allowed_models,
      status: "active",
    });
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal api-key-wizard-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("发放 Key")}</p>
            <h2>{tx("创建内部调用 Key")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")} disabled={loading}>×</button>
        </div>

        <div className="wizard-stepper" aria-label={tx("创建 Key 步骤")}>
          {steps.map((item, index) => {
            const Icon = item.icon;
            return (
              <button
                aria-current={step === index ? "step" : undefined}
                className={step === index ? "wizard-step active" : index < step ? "wizard-step done" : "wizard-step"}
                disabled={index > step || loading}
                key={item.title}
                onClick={() => setStep(index)}
                type="button"
              >
                <span><Icon size={14} /></span>
                <strong>{tx(item.title)}</strong>
              </button>
            );
          })}
        </div>

        <div className="api-key-wizard-body">
          {step === 0 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("选择 Key 归属项目")}</h3>
                <p>{tx("Key 必须挂在项目空间下，用量和成本会归集到这个项目。")}</p>
              </div>
              {projectOptions.length === 0 ? (
                <div className="empty wizard-empty">{tx("当前账号没有可发放 Key 的项目权限，请联系项目负责人或管理员把你加入项目。")}</div>
              ) : (
                <div className="wizard-project-grid">
                  {projectOptions.map((option) => {
                    const project = findProject(data, option.value);
                    return (
                      <button
                        className={values.project_id === option.value ? "wizard-project-card active" : "wizard-project-card"}
                        key={option.value}
                        onClick={() => update("project_id", option.value)}
                        type="button"
                      >
                        <strong>{project?.name || option.label}</strong>
                        <span>{tx("团队")}：{projectTeamLabel(data, option.value)}</span>
                        <span>{tx("负责人")}：{projectOwnerLabel(data, option.value)}</span>
                      </button>
                    );
                  })}
                </div>
              )}
            </section>
          ) : null}

          {step === 1 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("说明用途和环境")}</h3>
                <p>{tx("名称建议能看出调用方、环境和用途，后续审计会更容易定位。")}</p>
              </div>
              <div className="wizard-form-grid">
                {renderField("name", { placeholder: selectedProject ? `${selectedProject.name} production` : "backend production" })}
                {renderField("group", { placeholder: "prod、dev、backend-service" })}
              </div>
            </section>
          ) : null}

          {step === 2 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("设置模型范围")}</h3>
                <p>{tx("留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录、路由策略和项目权限约束。")}</p>
              </div>
              <div className="wizard-choice-row">
                <button className={modelScope === "all" ? "wizard-choice active" : "wizard-choice"} onClick={() => setModelScope("all")} type="button">
                  <strong>{tx("全部可路由模型")}</strong>
                  <span>{tx("由平台路由策略决定最终可调用范围")}</span>
                </button>
                <button className={modelScope === "selected" ? "wizard-choice active" : "wizard-choice"} onClick={() => setModelScope("selected")} type="button">
                  <strong>{tx("指定模型白名单")}</strong>
                  <span>{tx("只允许这个 Key 调用已勾选的模型")}</span>
                </button>
              </div>
              {modelScope === "selected" ? (
                <div className="wizard-model-list">
                  {selectableModels.length === 0 ? (
                    <div className="empty wizard-empty">{tx("当前没有可选择的启用模型。请先在模型目录和路由策略里启用模型。")}</div>
                  ) : (
                    selectableModels.map((model) => (
                      <label className="wizard-model-option" key={model.name}>
                        <input
                          checked={selectedModels.includes(model.name)}
                          onChange={() => toggleModel(model.name)}
                          type="checkbox"
                        />
                        <span>
                          <strong>{model.name}</strong>
                          <em>{modelAvailabilitySummary(model, data, false).label}</em>
                        </span>
                      </label>
                    ))
                  )}
                </div>
              ) : null}
            </section>
          ) : null}

          {step === 3 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("设置安全护栏")}</h3>
                <p>{tx("可以先使用默认额度，之后再按调用量调整。IP 白名单留空表示不限来源。")}</p>
              </div>
              <div className="wizard-form-grid">
                {renderField("ip_allowlist")}
                {renderField("max_concurrency")}
                {renderField("daily_requests")}
                {renderField("monthly_requests")}
                {renderField("daily_tokens")}
                {renderField("monthly_tokens")}
                {renderField("daily_cost_usd")}
                {renderField("monthly_cost_usd")}
              </div>
            </section>
          ) : null}

          {step === 4 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("确认后生成 Key")}</h3>
                <p>{tx("完整 Key 只会展示一次。关闭弹窗后只能看到前后缀，后续需要通过轮换生成新 Key。")}</p>
              </div>
              <div className="wizard-review-grid">
                <ReviewItem label="归属项目" value={selectedProject?.name || values.project_id || "-"} />
                <ReviewItem label="用途/环境" value={values.group || "default"} />
                <ReviewItem label="Key 名称" value={values.name || "-"} />
                <ReviewItem label="模型范围" value={modelScope === "all" ? tx("全部可路由模型") : selectedModels.join(", ") || "-"} />
                <ReviewItem label="IP 白名单" value={splitList(values.ip_allowlist).join(", ") || tx("不限")} />
                <ReviewItem label="最大并发" value={values.max_concurrency || "-"} />
                <ReviewItem label="日请求" value={values.daily_requests || "-"} />
                <ReviewItem label="月成本 USD" value={values.monthly_cost_usd || "-"} />
              </div>
            </section>
          ) : null}
        </div>

        <div className="modal-actions wizard-actions">
          <button className="secondary-button" onClick={onClose} type="button" disabled={loading}>{tx("取消")}</button>
          {step > 0 ? (
            <button className="secondary-button" onClick={() => setStep((current) => Math.max(current - 1, 0))} type="button" disabled={loading}>
              {tx("上一步")}
            </button>
          ) : null}
          <button className="button" disabled={loading || !canContinue()} type="submit">
            {step === steps.length - 1 ? (loading ? tx("发放中") : tx("生成 Key")) : tx("下一步")}
          </button>
        </div>
      </form>
    </div>
  );
}

export function ReviewItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="wizard-review-item">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function UserImportModal({
  loading,
  onClose,
  onImport,
}: {
  loading: boolean;
  onClose: () => void;
  onImport: (content: string) => void;
}) {
  const [content, setContent] = useState("");

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onImport(content);
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal user-import-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("批量导入")}</p>
            <h2>{tx("导入用户")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        <div className="modal-body user-import-body">
          <label className="field">
            <span>{tx("CSV 内容")}</span>
            <textarea
              className="user-import-textarea"
              value={content}
              onChange={(event) => setContent(event.target.value)}
              placeholder={"username,name,email,role,team_id,status\nzhangsan,张三,zhangsan@example.com,user,team_platform,active"}
              required
            />
            <small>{tx("按 username 或 email 匹配已有用户；匹配到则更新，未匹配则创建。")}</small>
          </label>
          <div className="user-import-example">
            <strong>{tx("字段顺序")}</strong>
            <code>username,name,email,role,team_id,status</code>
            <span>{tx("role 可填 admin、team_leader、user；status 可填 active 或 disabled。")}</span>
          </div>
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{loading ? tx("导入中") : tx("开始导入")}</button>
        </div>
      </form>
    </div>
  );
}
