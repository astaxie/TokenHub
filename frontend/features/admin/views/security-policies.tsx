"use client";

import { AlertCircle, ChevronDown, ChevronRight, FlaskConical, Info, Pencil, Plus, Search, ShieldCheck, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { type ApiContext, type AppData } from "../core/types";
import { formatTime } from "../domain/formatting";
import { countWithUnit, guardrailDetectionItemName, millisecondsText, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { ConfirmDialog, StatusPill } from "../shared/ui";

type GuardrailDetectorType = "pattern" | "sensitive_data" | "model";
type GuardrailAction = "audit" | "mask" | "block";

type GuardrailDetectionItem = {
  id?: string;
  name: string;
  detector_type: GuardrailDetectorType;
  action: GuardrailAction;
  config: Record<string, unknown>;
};

type GuardrailBinding = {
  scope_type: "all_projects" | "project";
  scope_id?: string;
  checkpoint?: "before_provider";
  protocol?: "all";
};

type GuardrailPolicy = {
  id: string;
  name: string;
  description?: string;
  status: "active" | "disabled";
  detection_items: GuardrailDetectionItem[];
  bindings: GuardrailBinding[];
  updated_at?: string;
};

type GuardrailPolicyDraft = Omit<GuardrailPolicy, "id" | "updated_at"> & { id?: string };

type GuardrailTestFinding = {
  policy_name: string;
  detection_item_name: string;
  detector_type: GuardrailDetectorType;
  action: GuardrailAction;
  category: string;
  reason_code: string;
  start: number;
  end: number;
};

type GuardrailTestResult = {
  action: "allow" | GuardrailAction;
  findings: GuardrailTestFinding[];
  short_circuited: boolean;
  detection_degraded: boolean;
  duration_ms: number;
  masked_text?: string;
};

const detectorTypes: GuardrailDetectorType[] = ["pattern", "sensitive_data", "model"];
const dataTypes = ["credential", "email", "phone", "cn_id_card", "bank_card", "person_name", "address", "birth_date"];
const sensitiveDataExamples: Record<string, string> = {
  credential: "sk-example_12345678901234567890",
  email: "demo@example.com",
  phone: "13812345678",
  cn_id_card: "11010519491231002X",
  bank_card: "银行卡号：6222020200123456789",
  person_name: "姓名：王浩宇",
  address: "联系地址：上海市黄浦区南京东路 123 弄 4 号 602 室",
  birth_date: "出生日期：1992 年 05 月 16 日",
};
const sensitiveDataTestSample = `姓名：王浩宇
身份证号码：11010519491231002X
手机号码：13812345678
联系地址：上海市黄浦区南京东路 123 弄 4 号 602 室
电子邮箱：demo@example.com
银行卡号：6222020200123456789
访问密钥：sk-example_12345678901234567890
紧急联系人：李雯 联系电话：13987654321
出生日期：1992 年 05 月 16 日
户籍地址：上海市黄浦区人民路 89 号`;

export function SecurityPolicyTabs({ active, onChange }: { active: "access" | "content"; onChange: (value: "access" | "content") => void }) {
  return (
    <div className="security-policy-tabs settings-tabs" role="tablist" aria-label={tx("安全策略分类")}>
      <button aria-selected={active === "access"} className={active === "access" ? "settings-tab active" : "settings-tab"} onClick={() => onChange("access")} role="tab" type="button">
        {tx("访问安全")}
      </button>
      <button aria-selected={active === "content"} className={active === "content" ? "settings-tab active" : "settings-tab"} onClick={() => onChange("content")} role="tab" type="button">
        {tx("内容安全")}
      </button>
    </div>
  );
}

export function ContentSecurityPolicies({ api, data }: { api: ApiContext; data: AppData }) {
  const [policies, setPolicies] = useState<GuardrailPolicy[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [draft, setDraft] = useState<GuardrailPolicyDraft | null>(null);
  const [deletePolicy, setDeletePolicy] = useState<GuardrailPolicy | null>(null);

  const filteredPolicies = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return policies;
    return policies.filter((policy) => `${policy.name} ${policy.description ?? ""} ${policy.status}`.toLowerCase().includes(normalized));
  }, [policies, query]);

  async function loadPolicies() {
    setLoading(true);
    setError("");
    try {
      const response = await adminFetch(api, "/api/admin/guardrail-policies");
      if (!response.ok) throw new Error(await readAdminError(response, tx("读取内容安全策略失败")));
      const payload = await response.json() as { data?: GuardrailPolicy[] };
      setPolicies(payload.data ?? []);
    } catch (loadError) {
      if (!isAuthExpiredError(loadError)) setError(loadError instanceof Error ? loadError.message : tx("读取内容安全策略失败"));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadPolicies();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- api identity is the load key; loadPolicies is also reused by mutation handlers.
  }, [api]);

  async function savePolicy() {
    if (!draft) return;
    const validationError = validateDraft(draft);
    if (validationError) {
      setError(validationError);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const path = draft.id ? `/api/admin/guardrail-policies/${draft.id}` : "/api/admin/guardrail-policies";
      const response = await adminFetch(api, path, {
        method: draft.id ? "PUT" : "POST",
        body: JSON.stringify(policyPayload(draft)),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("保存内容安全策略失败")));
      setDraft(null);
      setNotice(tx("策略已保存并立即生效"));
      await loadPolicies();
    } catch (saveError) {
      if (!isAuthExpiredError(saveError)) setError(saveError instanceof Error ? saveError.message : tx("保存内容安全策略失败"));
    } finally {
      setSaving(false);
    }
  }

  async function removePolicy() {
    if (!deletePolicy) return;
    setSaving(true);
    setError("");
    try {
      const response = await adminFetch(api, `/api/admin/guardrail-policies/${deletePolicy.id}`, { method: "DELETE" });
      if (!response.ok && response.status !== 204) throw new Error(await readAdminError(response, tx("删除内容安全策略失败")));
      setDeletePolicy(null);
      setNotice(tx("内容安全策略已删除"));
      await loadPolicies();
    } catch (removeError) {
      if (!isAuthExpiredError(removeError)) setError(removeError instanceof Error ? removeError.message : tx("删除内容安全策略失败"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="section content-security-section">
      <div className="section-header">
        <h2>{tx("内容安全策略")}</h2>
      </div>
      <div className="section-body">
        <div className="content-security-tip" role="note">
          <Info aria-hidden="true" size={17} />
          <div>
            <strong>{tx("内容安全如何工作")}</strong>
            <span>{tx("检查发给模型的请求和返回应用的响应。当前版本仅支持请求发送前；多条策略累积，保存后立即生效。")}</span>
          </div>
        </div>

        {error ? <div className="guardrail-inline-message error"><AlertCircle size={16} /><span>{error}</span><button aria-label={tx("关闭")} className="icon-button subtle" onClick={() => setError("")} title={tx("关闭")} type="button"><X size={14} /></button></div> : null}
        {notice ? <div className="guardrail-inline-message success"><ShieldCheck size={16} /><span>{notice}</span><button aria-label={tx("关闭")} className="icon-button subtle" onClick={() => setNotice("")} title={tx("关闭")} type="button"><X size={14} /></button></div> : null}

        <div className="table-toolbar guardrail-toolbar">
          <div className="search-box">
            <Search size={16} />
            <input onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索策略名称或说明")} type="search" value={query} />
          </div>
          <div className="table-toolbar-actions">
            <span className="table-result-count">{countWithUnit(filteredPolicies.length, "条策略", "policy", "件のポリシー")}</span>
            <button className="button" onClick={() => setDraft(newPolicyDraft())} type="button"><Plus size={17} />{tx("新增策略")}</button>
          </div>
        </div>

        {loading ? (
          <div className="guardrail-empty"><span>{tx("正在读取内容安全策略...")}</span></div>
        ) : filteredPolicies.length === 0 ? (
          <div className="guardrail-empty">
            <ShieldCheck size={22} />
            <strong>{query ? tx("没有匹配的内容安全策略") : tx("还没有内容安全策略")}</strong>
            <span>{query ? tx("请调整搜索条件。") : tx("创建一条策略，添加检测项并选择应用项目。")}</span>
            {!query ? <button className="secondary-button" onClick={() => setDraft(newPolicyDraft())} type="button"><Plus size={16} />{tx("新增策略")}</button> : null}
          </div>
        ) : (
          <div className="guardrail-policy-list">
            <div className="guardrail-policy-row guardrail-policy-head" aria-hidden="true">
              <span>{tx("安全策略")}</span><span>{tx("检测项")}</span><span>{tx("应用范围")}</span><span>{tx("检查位置")}</span><span>{tx("更新时间")}</span><span />
            </div>
            {filteredPolicies.map((policy) => (
              <div className="guardrail-policy-row" key={policy.id}>
                <button className="guardrail-policy-main" onClick={() => setDraft(editPolicyDraft(policy))} type="button">
                  <span><strong>{policy.name}</strong><small>{policy.description || tx("暂无说明")}</small></span>
                  <StatusPill status={policy.status} />
                </button>
                <span>{countWithUnit(policy.detection_items.length, "项", "item", "件")}</span>
                <span>{scopeSummary(policy, data)}</span>
                <span>{tx("请求发送前")}</span>
                <span>{formatTime(policy.updated_at ?? "")}</span>
                <span className="guardrail-row-actions">
                  <button aria-label={tx("编辑策略")} className="icon-button subtle" onClick={() => setDraft(editPolicyDraft(policy))} title={tx("编辑策略")} type="button"><Pencil size={15} /></button>
                  <button aria-label={tx("删除策略")} className="icon-button subtle danger" onClick={() => setDeletePolicy(policy)} title={tx("删除策略")} type="button"><Trash2 size={15} /></button>
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {draft ? <GuardrailPolicyEditor api={api} data={data} draft={draft} saving={saving} onChange={setDraft} onClose={() => !saving && setDraft(null)} onSave={() => void savePolicy()} /> : null}
      {deletePolicy ? <ConfirmDialog title={tx("删除内容安全策略")} message={tx("删除后，这条策略将立即停止生效。此操作无法撤销。")} confirmLabel={tx("删除")} loading={saving} onCancel={() => setDeletePolicy(null)} onConfirm={() => void removePolicy()} /> : null}
    </section>
  );
}

function GuardrailPolicyEditor({ api, data, draft, saving, onChange, onClose, onSave }: { api: ApiContext; data: AppData; draft: GuardrailPolicyDraft; saving: boolean; onChange: (draft: GuardrailPolicyDraft) => void; onClose: () => void; onSave: () => void }) {
  const allProjects = draft.bindings.some((binding) => binding.scope_type === "all_projects");
  const selectedProjects = new Set(draft.bindings.filter((binding) => binding.scope_type === "project").map((binding) => binding.scope_id));
  const [testOpen, setTestOpen] = useState(false);
  const [testText, setTestText] = useState("");
  const [testResult, setTestResult] = useState<GuardrailTestResult | null>(null);
  const [testError, setTestError] = useState("");
  const [testing, setTesting] = useState(false);
  const testRequestID = useRef(0);

  useEffect(() => {
    testRequestID.current += 1;
    setTestResult(null);
    setTestError("");
    setTesting(false);
  }, [draft, testText]);

  function updateItem(index: number, item: GuardrailDetectionItem) {
    onChange({ ...draft, detection_items: draft.detection_items.map((current, itemIndex) => itemIndex === index ? item : current) });
  }

  async function runTest() {
    const validationError = validateDraft(draft);
    if (validationError) {
      setTestError(validationError);
      return;
    }
    if (!testText.trim()) {
      setTestError(tx("请输入测试内容"));
      return;
    }
    const requestID = ++testRequestID.current;
    setTesting(true);
    setTestError("");
    setTestResult(null);
    try {
      const response = await adminFetch(api, "/api/admin/guardrail-policies/test", {
        method: "POST",
        body: JSON.stringify({ policy: policyPayload(draft), text: testText }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("测试内容安全策略失败")));
      const result = await response.json() as GuardrailTestResult;
      if (requestID === testRequestID.current) setTestResult(result);
    } catch (requestError) {
      if (requestID === testRequestID.current && !isAuthExpiredError(requestError)) setTestError(requestError instanceof Error ? requestError.message : tx("测试内容安全策略失败"));
    } finally {
      if (requestID === testRequestID.current) setTesting(false);
    }
  }

  return (
    <div className="guardrail-editor-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <aside aria-labelledby="guardrail-editor-title" aria-modal="true" className={testOpen ? "guardrail-editor testing" : "guardrail-editor"} role="dialog">
        <header className="guardrail-editor-header">
          <div><p className="eyebrow">{tx(draft.id ? "编辑内容安全策略" : "新增内容安全策略")}</p><h2 id="guardrail-editor-title">{draft.id ? draft.name : tx("新增策略")}</h2><span>{tx("保存后立即应用，不需要发布或审批。")}</span></div>
          <button aria-label={tx("关闭")} className="icon-button" disabled={saving} onClick={onClose} title={tx("关闭")} type="button"><X size={18} /></button>
        </header>
        <div className={testOpen ? "guardrail-editor-workspace testing" : "guardrail-editor-workspace"}>
        <div className="guardrail-editor-body">
          <section className="guardrail-editor-section">
            <div className="guardrail-editor-section-title"><h3>{tx("基本信息")}</h3></div>
            <div className="guardrail-field-grid">
              <label className="field"><span>{tx("策略名称")}</span><input maxLength={120} onChange={(event) => onChange({ ...draft, name: event.target.value })} placeholder={tx("例如：研发数据保护")} required value={draft.name} /></label>
              <label className="field"><span>{tx("状态")}</span><select onChange={(event) => onChange({ ...draft, status: event.target.value as GuardrailPolicyDraft["status"] })} value={draft.status}><option value="active">{tx("已启用")}</option><option value="disabled">{tx("已停用")}</option></select></label>
              <label className="field guardrail-description-field"><span>{tx("说明")}</span><textarea onChange={(event) => onChange({ ...draft, description: event.target.value })} placeholder={tx("简要说明这条策略保护什么内容")} value={draft.description ?? ""} /></label>
            </div>
          </section>

          <section className="guardrail-editor-section">
            <div className="guardrail-editor-section-title"><div><h3>{tx("检测项")}</h3><span>{tx("命中时执行每个检测项配置的动作；冲突时采用最严格动作。")}</span></div><button className="secondary-button" onClick={() => onChange({ ...draft, detection_items: [...draft.detection_items, newDetectionItem("sensitive_data")] })} type="button"><Plus size={15} />{tx("添加检测项")}</button></div>
            <div className="guardrail-detector-list">
              {draft.detection_items.map((item, index) => (
                <DetectionItemEditor item={item} itemNumber={index + 1} key={`${item.id ?? "new"}-${index}`} onChange={(nextItem) => updateItem(index, nextItem)} onRemove={() => onChange({ ...draft, detection_items: draft.detection_items.filter((_, itemIndex) => itemIndex !== index) })} />
              ))}
            </div>
          </section>

          <section className="guardrail-editor-section">
            <div className="guardrail-editor-section-title"><div><h3>{tx("应用范围")}</h3><span>{tx("API Key 自动继承所属项目的内容安全策略。")}</span></div></div>
            <div className="guardrail-scope-switch" role="radiogroup" aria-label={tx("应用范围")}>
              <button aria-checked={allProjects} className={allProjects ? "active" : ""} onClick={() => onChange({ ...draft, bindings: [{ scope_type: "all_projects" }] })} role="radio" type="button">{tx("全部项目")}</button>
              <button aria-checked={!allProjects} className={!allProjects ? "active" : ""} onClick={() => onChange({ ...draft, bindings: [] })} role="radio" type="button">{tx("指定项目")}</button>
            </div>
            {!allProjects ? (
              <div className="guardrail-project-list">
                {data.projects.length === 0 ? <span className="guardrail-project-empty">{tx("暂无可选项目")}</span> : data.projects.map((project) => (
                  <label className="guardrail-project-option" key={project.id}>
                    <input checked={selectedProjects.has(project.id)} onChange={(event) => {
                      const bindings = draft.bindings.filter((binding) => binding.scope_type === "project" && binding.scope_id !== project.id);
                      if (event.target.checked) bindings.push({ scope_type: "project", scope_id: project.id });
                      onChange({ ...draft, bindings });
                    }} type="checkbox" />
                    <span><strong>{project.name}</strong><small>{project.id}</small></span>
                  </label>
                ))}
              </div>
            ) : null}
          </section>
        </div>
        {testOpen ? <GuardrailPolicyTestPanel error={testError} result={testResult} text={testText} testing={testing} onClose={() => setTestOpen(false)} onLoadSample={() => setTestText(sensitiveDataTestSample)} onRun={() => void runTest()} onTextChange={(value) => { setTestText(value); setTestResult(null); setTestError(""); }} /> : null}
        </div>
        <footer className="guardrail-editor-footer"><span><ShieldCheck size={15} />{tx("当前检查位置：请求发送前")}</span><div><button aria-label={tx("测试当前配置")} className="secondary-button" disabled={saving || testing} onClick={() => setTestOpen((value) => !value)} title={tx("测试当前配置")} type="button"><FlaskConical size={15} />{tx("测试")}</button><button className="secondary-button" disabled={saving || testing} onClick={onClose} type="button">{tx("取消")}</button><button className="button" disabled={saving || testing} onClick={onSave} type="button">{saving ? tx("保存中...") : tx("保存并应用")}</button></div></footer>
      </aside>
    </div>
  );
}

function GuardrailPolicyTestPanel({ error, result, text, testing, onClose, onLoadSample, onRun, onTextChange }: { error: string; result: GuardrailTestResult | null; text: string; testing: boolean; onClose: () => void; onLoadSample: () => void; onRun: () => void; onTextChange: (value: string) => void }) {
  const highlightedSegments = result ? testHighlightSegments(text, result.findings) : [];
  return (
    <aside aria-label={tx("测试当前配置")} className="guardrail-test-panel">
      <header><div><h3>{tx("测试当前配置")}</h3><span>{tx("使用未保存配置，不记录测试内容。")}</span></div><button aria-label={tx("关闭测试")} className="icon-button subtle" onClick={onClose} title={tx("关闭测试")} type="button"><X size={16} /></button></header>
      <div className="guardrail-test-body">
        <label className="field"><span>{tx("测试内容")}</span><textarea autoFocus onChange={(event) => onTextChange(event.target.value)} placeholder={tx("输入一段待检查的请求内容")} value={text} /></label>
        <div className="guardrail-test-sample"><button className="secondary-button" onClick={onLoadSample} type="button"><FlaskConical size={14} />{tx("载入敏感数据样例")}</button><span>{tx("样例均为虚构数据，仅用于本地策略测试。")}</span></div>
        <button className="button guardrail-test-run" disabled={testing || !text.trim()} onClick={onRun} type="button"><FlaskConical size={15} />{testing ? tx("测试中...") : tx("运行测试")}</button>
        {error ? <div className="guardrail-test-error"><AlertCircle size={15} /><span>{error}</span></div> : null}
        {result ? (
          <div className="guardrail-test-result">
            <div className={`guardrail-test-decision ${result.action}`}><span>{tx("最终动作")}</span><strong>{tx(testActionLabel(result.action))}</strong><small>{millisecondsText(result.duration_ms)}{result.short_circuited ? ` · ${tx("已短路")}` : ""}{result.detection_degraded ? ` · ${tx("检测降级")}` : ""}</small></div>
            {highlightedSegments.some((segment) => segment.matched) ? <div className="guardrail-test-highlight"><span>{tx("命中位置")}</span><pre>{highlightedSegments.map((segment, index) => segment.matched ? <mark key={index}>{segment.text}</mark> : segment.text)}</pre></div> : null}
            {result.masked_text ? <div className="guardrail-test-masked"><span>{tx("脱敏结果")}</span><pre>{result.masked_text}</pre></div> : null}
            <div className="guardrail-test-findings">
              <span>{tx("命中详情")}</span>
              {result.findings.length === 0 ? <p>{tx("未命中检测项")}</p> : result.findings.map((finding, index) => <article key={`${finding.detection_item_name}-${index}`}><strong>{finding.detection_item_name}</strong><small>{tx(detectorTypeLabel(finding.detector_type))} · {tx(actionLabel(finding.action))}</small><span>{tx(findingCategoryLabel(finding.category))}</span><code>{finding.reason_code}</code></article>)}
            </div>
          </div>
        ) : null}
      </div>
    </aside>
  );
}

function DetectionItemEditor({ item, itemNumber, onChange, onRemove }: { item: GuardrailDetectionItem; itemNumber: number; onChange: (item: GuardrailDetectionItem) => void; onRemove: () => void }) {
  const [open, setOpen] = useState(true);
  const supportedActions: GuardrailAction[] = item.detector_type === "sensitive_data" ? ["audit", "mask", "block"] : ["audit", "block"];
  return (
    <article className={open ? "guardrail-detector open" : "guardrail-detector"}>
      <header>
        <button aria-expanded={open} className="guardrail-detector-toggle" onClick={() => setOpen((value) => !value)} type="button">{open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}<span><strong>{item.name || guardrailDetectionItemName(itemNumber)}</strong><small>{tx(detectorTypeLabel(item.detector_type))} · {tx(actionLabel(item.action))}</small></span></button>
        <button aria-label={tx("移除检测项")} className="icon-button subtle danger" onClick={onRemove} title={tx("移除检测项")} type="button"><Trash2 size={15} /></button>
      </header>
      {open ? (
        <div className="guardrail-detector-body">
          <label className="field"><span>{tx("名称")}</span><input onChange={(event) => onChange({ ...item, name: event.target.value })} placeholder={tx("检测项名称")} value={item.name} /></label>
          <label className="field"><span>{tx("检测类型")}</span><select onChange={(event) => onChange(newDetectionItem(event.target.value as GuardrailDetectorType))} value={item.detector_type}>{detectorTypes.map((type) => <option key={type} value={type}>{tx(detectorTypeLabel(type))}</option>)}</select><small>{tx(detectorTypeDescription(item.detector_type))}</small></label>
          <label className="field"><span>{tx("命中动作")}</span><select onChange={(event) => onChange({ ...item, action: event.target.value as GuardrailAction })} value={item.action}>{supportedActions.map((action) => <option key={action} value={action}>{tx(actionLabel(action))}</option>)}</select></label>
          {item.detector_type === "pattern" ? <PatternConfig item={item} onChange={onChange} /> : null}
          {item.detector_type === "sensitive_data" ? <SensitiveDataConfig item={item} onChange={onChange} /> : null}
          {item.detector_type === "model" ? <ModelConfig item={item} onChange={onChange} /> : null}
        </div>
      ) : null}
    </article>
  );
}

function PatternConfig({ item, onChange }: { item: GuardrailDetectionItem; onChange: (item: GuardrailDetectionItem) => void }) {
  return <><div className="guardrail-pattern-note guardrail-wide-field"><Info size={14} /><span>{tx("任意一个关键词或正则表达式匹配时，即视为命中。")}</span></div><label className="field guardrail-wide-field"><span>{tx("关键词（直接匹配）")}</span><textarea onChange={(event) => onChange({ ...item, config: { ...item.config, keywords: lines(event.target.value) } })} placeholder={tx("每行一项，例如：内部机密")} value={stringList(item.config.keywords).join("\n")} /><small>{tx("适合禁止词、内部代号和固定短语。")}</small></label><label className="field guardrail-wide-field"><span>{tx("正则表达式（高级）")}</span><textarea onChange={(event) => onChange({ ...item, config: { ...item.config, regex: lines(event.target.value) } })} placeholder={tx("每行一个表达式，例如：TH-[0-9]{6}")} value={stringList(item.config.regex).join("\n")} /><small>{tx("适合编号、格式规则或需要灵活匹配的内容。")}</small></label></>;
}

function SensitiveDataConfig({ item, onChange }: { item: GuardrailDetectionItem; onChange: (item: GuardrailDetectionItem) => void }) {
  const selected = new Set(stringList(item.config.data_types));
  return <fieldset className="guardrail-data-types guardrail-wide-field"><legend>{tx("敏感数据类型")}</legend>{dataTypes.map((type) => <label key={type}><input checked={selected.has(type)} onChange={(event) => { const next = new Set(selected); if (event.target.checked) next.add(type); else next.delete(type); onChange({ ...item, config: { data_types: Array.from(next) } }); }} type="checkbox" /><span><strong>{tx(dataTypeLabel(type))}</strong><small>{tx("样例")}：{sensitiveDataExamples[type]}</small></span></label>)}</fieldset>;
}

function ModelConfig({ item, onChange }: { item: GuardrailDetectionItem; onChange: (item: GuardrailDetectionItem) => void }) {
  return <><div className="guardrail-model-note guardrail-wide-field"><Info size={14} /><span>{tx("使用独立配置的 Qwen3Guard 服务；未配置或不可用时执行下方策略。")}</span></div><label className="field"><span>{tx("阻断范围")}</span><select onChange={(event) => onChange({ ...item, config: { ...item.config, block_on: event.target.value } })} value={String(item.config.block_on ?? "unsafe")}><option value="unsafe">Unsafe</option><option value="controversial_or_unsafe">Controversial + Unsafe</option></select></label><label className="field"><span>{tx("模型不可用时")}</span><select onChange={(event) => onChange({ ...item, config: { ...item.config, on_unavailable: event.target.value } })} value={String(item.config.on_unavailable ?? "allow_and_audit")}><option value="allow_and_audit">{tx("放行并记录")}</option><option value="block">{tx("阻断请求")}</option></select></label></>;
}

function newPolicyDraft(): GuardrailPolicyDraft {
  return { name: "", description: "", status: "active", detection_items: [newDetectionItem("sensitive_data")], bindings: [{ scope_type: "all_projects" }] };
}

function newDetectionItem(type: GuardrailDetectorType): GuardrailDetectionItem {
  if (type === "pattern") return { name: tx("自定义文本规则"), detector_type: type, action: "block", config: { keywords: [], regex: [], case_sensitive: false } };
  if (type === "model") return { name: tx("模型内容分类"), detector_type: type, action: "audit", config: { block_on: "unsafe", on_unavailable: "allow_and_audit" } };
  return { name: tx("敏感信息识别"), detector_type: type, action: "block", config: { data_types: ["credential"] } };
}

function editPolicyDraft(policy: GuardrailPolicy): GuardrailPolicyDraft {
  return JSON.parse(JSON.stringify(policy)) as GuardrailPolicyDraft;
}

function policyPayload(draft: GuardrailPolicyDraft) {
  return { name: draft.name.trim(), description: draft.description?.trim() ?? "", status: draft.status, detection_items: draft.detection_items.map((item) => ({ id: item.id, name: item.name.trim(), detector_type: item.detector_type, action: item.action, config: item.config })), bindings: draft.bindings.map((binding) => ({ ...binding, checkpoint: "before_provider", protocol: "all" })) };
}

function validateDraft(draft: GuardrailPolicyDraft) {
  if (!draft.name.trim()) return tx("请输入策略名称");
  if (draft.detection_items.length === 0) return tx("请至少添加一个检测项");
  if (draft.detection_items.some((item) => !item.name.trim())) return tx("请输入检测项名称");
  if (draft.bindings.length === 0) return tx("请选择至少一个项目");
  for (const item of draft.detection_items) {
    if (item.detector_type === "pattern" && stringList(item.config.keywords).length === 0 && stringList(item.config.regex).length === 0) return tx("自定义文本规则至少需要一个关键词或正则表达式");
    if (item.detector_type === "sensitive_data" && stringList(item.config.data_types).length === 0) return tx("请至少选择一种敏感数据类型");
  }
  return "";
}

function scopeSummary(policy: GuardrailPolicy, data: AppData) {
  if (policy.bindings.some((binding) => binding.scope_type === "all_projects")) return tx("全部项目");
  const count = policy.bindings.filter((binding) => binding.scope_type === "project").length;
  if (count === 1) return data.projects.find((project) => project.id === policy.bindings[0]?.scope_id)?.name ?? countWithUnit(1, "个项目", "project", "プロジェクト");
  return countWithUnit(count, "个项目", "project", "プロジェクト");
}

function stringList(value: unknown): string[] { return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []; }
function lines(value: string) { return value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean); }
function testHighlightSegments(text: string, findings: GuardrailTestFinding[]) {
  const ranges = findings
    .filter((finding) => finding.end > finding.start && finding.start >= 0)
    .map((finding) => ({ start: utf8ByteOffsetToStringIndex(text, finding.start), end: utf8ByteOffsetToStringIndex(text, finding.end) }))
    .filter((range) => range.end > range.start)
    .sort((left, right) => left.start - right.start || left.end - right.end);
  const merged: Array<{ start: number; end: number }> = [];
  for (const range of ranges) {
    const previous = merged[merged.length - 1];
    if (previous && range.start <= previous.end) previous.end = Math.max(previous.end, range.end);
    else merged.push({ ...range });
  }
  const segments: Array<{ text: string; matched: boolean }> = [];
  let cursor = 0;
  for (const range of merged) {
    if (range.start > cursor) segments.push({ text: text.slice(cursor, range.start), matched: false });
    segments.push({ text: text.slice(range.start, range.end), matched: true });
    cursor = range.end;
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor), matched: false });
  return segments;
}
function utf8ByteOffsetToStringIndex(text: string, byteOffset: number) {
  let bytes = 0;
  let index = 0;
  for (const character of text) {
    if (bytes >= byteOffset) break;
    bytes += new TextEncoder().encode(character).length;
    index += character.length;
  }
  return index;
}
function detectorTypeLabel(type: GuardrailDetectorType) { return type === "pattern" ? "自定义文本规则" : type === "sensitive_data" ? "敏感信息识别" : "AI 内容审核"; }
function detectorTypeDescription(type: GuardrailDetectorType) { return type === "pattern" ? "按关键词或正则表达式匹配请求文本。" : type === "sensitive_data" ? "识别身份证号、手机号、凭证等敏感信息。" : "使用独立审核模型判断不安全或争议内容。"; }
function actionLabel(action: GuardrailAction) { return action === "audit" ? "审计" : action === "mask" ? "脱敏" : "阻断"; }
function testActionLabel(action: GuardrailTestResult["action"]) { return action === "allow" ? "放行" : actionLabel(action); }
function dataTypeLabel(type: string) {
  const labels: Record<string, string> = { credential: "云凭据与访问密钥", email: "邮箱地址", phone: "手机号码", cn_id_card: "中国身份证号", bank_card: "银行卡号", person_name: "姓名", address: "地址", birth_date: "出生日期" };
  return labels[type] ?? type;
}
function findingCategoryLabel(category: string) {
  if (category === "pattern") return "关键词或正则匹配";
  if (category === "unsafe") return "模型判定为不安全";
  if (category === "controversial") return "模型判定为争议内容";
  return dataTypeLabel(category);
}
