import { AlertCircle, Image as ImageIcon, LoaderCircle } from "lucide-react";
import { useMemo, useState } from "react";
import { type ApiContext, type ModelRoute, type Provider, type ProviderResource } from "../core/types";
import { codexImageModelState, codexImageResources, codexImageRouteEnabled, defaultCodexImageResourceID } from "../domain/codex-image-capability";
import { formatTranslationTemplate, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { formatImageGenerationCapabilityTag, providerResourceAccountLabel } from "./provider-account-ui";

type CodexImageCapabilityResult = {
  enabled: boolean;
  tested: boolean;
  capability?: string;
  resource_id: string;
  route_id?: string;
};

export function ProviderCodexImageCapability({
  api,
  provider,
  routes,
  resources,
  selectedAccountID,
  onChanged,
  setNotice,
}: {
  api: ApiContext;
  provider: Provider;
  routes: ModelRoute[];
  resources: ProviderResource[];
  selectedAccountID: string;
  onChanged: () => Promise<void>;
  setNotice: (value: string) => void;
}) {
  const providerResources = useMemo(() => codexImageResources(resources, provider.id), [provider.id, resources]);
  const enabled = codexImageRouteEnabled(routes, provider.id);
  const modelState = codexImageModelState(resources, provider.id, enabled);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [resourceID, setResourceID] = useState("");
  const [busy, setBusy] = useState<"enable" | "disable" | "">("");
  const [error, setError] = useState("");
  const selectedResource = providerResources.find((resource) => resource.id === resourceID);
  const stateLabel = {
    enabled: "已启用，生图测试通过",
    enabled_without_account: "线路已启用，但当前没有测试通过的可用账号",
    unsupported: "当前可用账号无法创建图片",
    tested_disabled: "生图测试已通过，当前未启用",
    untested: "未测试生图能力",
  }[modelState];

  function openCapabilityTest() {
    setResourceID(defaultCodexImageResourceID(resources, provider.id, selectedAccountID));
    setError("");
    setDialogOpen(true);
  }

  async function configure(enabledValue: boolean, targetResourceID: string) {
    if (!targetResourceID) {
      setError(tx("没有可用于生图测试的 Codex 账号，请先启用账号。"));
      return;
    }
    setBusy(enabledValue ? "enable" : "disable");
    setError("");
    try {
      const response = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(targetResourceID)}/image-capability`, {
        method: "POST",
        body: JSON.stringify({ enabled: enabledValue }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("配置 Codex 订阅生图")));
      const result = await response.json() as CodexImageCapabilityResult;
      setNotice(tx(result.enabled ? "Codex 订阅生图测试通过并已启用。" : "Codex 订阅生图已停用；能力测试结果已保留。"));
      await onChanged().catch(() => undefined);
      setDialogOpen(false);
    } catch (caught) {
      if (isAuthExpiredError(caught)) return;
      setError(caught instanceof Error ? caught.message : tx("Codex 订阅生图配置失败"));
      await onChanged().catch(() => undefined);
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <label className={`model-option provider-codex-image-option ${modelState}`}>
        <input
          checked={enabled}
          disabled={Boolean(busy) || providerResources.length === 0}
          onChange={(event) => {
            if (event.target.checked) openCapabilityTest();
            else void configure(false, providerResources[0]?.id ?? "");
          }}
          type="checkbox"
        />
        <div>
          <strong>{tx("Codex 订阅生图")}</strong>
          <span>codex-gpt-image-2 ← gpt-image-2</span>
          <small>{tx(stateLabel)} · {tx("勾选后会向所选真实账号发送一次低质量生图测试，测试会消耗少量订阅额度。")}</small>
          <div className="capability-row">
            <em>{tx("图片生成")}</em>
            <em>{tx("订阅额度")}</em>
            {busy === "disable" ? <em>{tx("正在停用")}</em> : null}
          </div>
          {!dialogOpen && error ? <p className="provider-quota-error" role="alert">{error}</p> : null}
        </div>
      </label>
      {dialogOpen ? (
        <div className="modal-backdrop provider-account-confirmation-backdrop" role="presentation">
          <div aria-labelledby="codex-image-capability-title" aria-modal="true" className="confirm-modal provider-codex-image-modal" role="dialog">
            <div className="provider-codex-image-modal-title">
              <ImageIcon aria-hidden="true" size={20} />
              <div>
                <p className="eyebrow">{tx("Codex 订阅模型")}</p>
                <h2 id="codex-image-capability-title">{tx("测试并启用生图能力")}</h2>
              </div>
            </div>
            <p>{tx("TokenHub 将立即向所选真实 Codex 账号发送一次低质量生图请求。只有收到有效图片后才会添加线路；本次测试会消耗少量订阅额度。")}</p>
            <label className="field">
              <span>{tx("测试账号")}</span>
              <select disabled={busy === "enable"} onChange={(event) => { setResourceID(event.target.value); setError(""); }} value={resourceID}>
                <option value="">{tx("请选择可用账号")}</option>
                {providerResources.map((resource) => {
                  const available = resource.status === "active" && resource.healthy !== false;
                  return <option disabled={!available} key={resource.id} value={resource.id}>
                    {resource.name} · {providerResourceAccountLabel(resource)} · {tx(available ? formatImageGenerationCapabilityTag(resource.options?.image_generation_capability) : "账号不可用")}
                  </option>;
                })}
              </select>
            </label>
            {busy === "enable" ? (
              <div className="provider-codex-image-testing" role="status">
                <LoaderCircle aria-hidden="true" className="spin" size={20} />
                <div><strong>{tx("正在测试生图能力")}</strong><span>{tx("正在等待真实 Codex 上游返回图片，请勿关闭弹窗。")}</span></div>
              </div>
            ) : null}
            {error ? <div className="provider-codex-image-error" role="alert"><AlertCircle aria-hidden="true" size={18} /><span>{error}</span></div> : null}
            {selectedResource?.options?.image_generation_capability ? (
              <p className="provider-credential-note">
                {formatTranslationTemplate(tx("该账号上次测试结果：{result}"), {
                  result: tx(formatImageGenerationCapabilityTag(selectedResource.options.image_generation_capability)),
                })}
              </p>
            ) : null}
            <div className="modal-actions">
              <button className="secondary-button" disabled={busy === "enable"} onClick={() => setDialogOpen(false)} type="button">{tx("取消")}</button>
              <button className="button" disabled={busy === "enable" || !resourceID} onClick={() => void configure(true, resourceID)} type="button">
                {tx(busy === "enable" ? "正在测试生图能力" : error ? "重新测试并启用" : "开始测试并启用")}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
}
