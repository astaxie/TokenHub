import { Download, Upload, X } from "lucide-react";
import { type Dispatch, type FormEvent, type SetStateAction, useRef } from "react";
import { tx } from "../i18n/runtime";
import { PluginPermissionDiffPreview, type PluginPermissionDiffPreviewDraft } from "./plugin-permission-diff-preview";

export type PluginInstallDraft = {
  source: "url" | "upload";
  downloadURL: string;
  checksumSHA256: string;
  packageFile: File | null;
  replace: boolean;
  enable: boolean;
  busy: boolean;
  error: string;
  result: string;
};

export function PluginInstallDialog({
  draft,
  permissionPreviewDraft,
  onClose,
  onInstall,
  onPermissionPreview,
  setDraft,
}: {
  draft: PluginInstallDraft;
  permissionPreviewDraft: PluginPermissionDiffPreviewDraft;
  onClose: () => void;
  onInstall: (event: FormEvent<HTMLFormElement>) => void;
  onPermissionPreview: () => void;
  setDraft: Dispatch<SetStateAction<PluginInstallDraft>>;
}) {
  return (
    <div className="modal-backdrop plugin-install-backdrop" role="presentation">
      <div aria-labelledby="plugin-install-title" aria-modal="true" className="modal plugin-install-modal" role="dialog">
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("插件管理")}</p>
            <h2 id="plugin-install-title">{tx("安装插件包")}</h2>
          </div>
          <button aria-label={tx("关闭")} className="icon-button" onClick={onClose} type="button">
            <X size={18} />
          </button>
        </div>
        <div className="plugin-install-modal-body">
          <PluginInstallFields
            draft={draft}
            onInstall={onInstall}
            onPermissionPreview={onPermissionPreview}
            permissionPreviewDraft={permissionPreviewDraft}
            setDraft={setDraft}
          />
        </div>
      </div>
    </div>
  );
}

function PluginInstallFields({
  draft,
  permissionPreviewDraft,
  onInstall,
  onPermissionPreview,
  setDraft,
}: {
  draft: PluginInstallDraft;
  permissionPreviewDraft: PluginPermissionDiffPreviewDraft;
  onInstall: (event: FormEvent<HTMLFormElement>) => void;
  onPermissionPreview: () => void;
  setDraft: Dispatch<SetStateAction<PluginInstallDraft>>;
}) {
  const packageInputRef = useRef<HTMLInputElement | null>(null);
  return (
    <form className="plugin-action-runner plugin-install-runner" onSubmit={onInstall}>
      <section className="plugin-install-panel plugin-install-source-panel">
        <div className="plugin-install-panel-head">
          <h3>{tx("安装方式")}</h3>
        </div>
        <div className="plugin-install-source settings-tabs" role="tablist" aria-label={tx("安装方式")}>
          <button
            aria-selected={draft.source === "url"}
            className={draft.source === "url" ? "settings-tab active" : "settings-tab"}
            onClick={() => setDraft((current) => ({ ...current, source: "url", error: "" }))}
            role="tab"
            type="button"
          >
            {tx("URL 安装")}
          </button>
          <button
            aria-selected={draft.source === "upload"}
            className={draft.source === "upload" ? "settings-tab active" : "settings-tab"}
            onClick={() => setDraft((current) => ({ ...current, source: "upload", error: "" }))}
            role="tab"
            type="button"
          >
            {tx("上传 ZIP")}
          </button>
        </div>
        <div className="plugin-install-source-body">
          {draft.source === "url" ? (
            <label className="plugin-action-field" key="plugin-install-url-source">
              <span>{tx("下载 URL")}</span>
              <input
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setDraft((current) => ({ ...current, downloadURL: value }));
                }}
                placeholder="https://plugins.example/plugin.zip"
                required
                type="url"
                value={draft.downloadURL}
              />
            </label>
          ) : (
            <div className="plugin-action-field plugin-upload-field" key="plugin-install-upload-source">
              <span>{tx("插件 ZIP 包")}</span>
              <div className="plugin-install-upload-row">
                <input
                  accept=".zip,application/zip,application/x-zip-compressed"
                  aria-label={tx("插件 ZIP 包")}
                  className="plugin-install-file-input"
                  onChange={(event) => {
                    const file = event.currentTarget.files?.[0] ?? null;
                    setDraft((current) => ({ ...current, packageFile: file, error: "" }));
                  }}
                  ref={packageInputRef}
                  required
                  type="file"
                />
                <button
                  className="secondary-button plugin-install-file-button"
                  onClick={() => packageInputRef.current?.click()}
                  type="button"
                >
                  <Upload size={14} />
                  <span>{tx("选择 ZIP")}</span>
                </button>
                <span className={`plugin-install-file-name${draft.packageFile ? " has-value" : ""}`}>
                  {draft.packageFile?.name ?? tx("未选择文件")}
                </span>
              </div>
            </div>
          )}
        </div>
      </section>
      <section className="plugin-install-panel plugin-install-settings-panel">
        <div className="plugin-install-panel-head">
          <h3>{tx("安装选项")}</h3>
        </div>
        <label className="plugin-action-field plugin-install-checksum-field">
          <span>{tx("SHA-256 校验")}</span>
          <input
            onChange={(event) => {
              const value = event.currentTarget.value;
              setDraft((current) => ({ ...current, checksumSHA256: value }));
            }}
            required={draft.source === "url"}
            value={draft.checksumSHA256}
          />
        </label>
        <div className="plugin-install-toggle-grid">
          <label className="plugin-install-toggle">
            <input
              checked={draft.replace}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setDraft((current) => ({ ...current, replace: checked }));
              }}
              type="checkbox"
            />
            <span>{tx("允许替换")}</span>
          </label>
          <label className="plugin-install-toggle">
            <input
              checked={draft.enable}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setDraft((current) => ({ ...current, enable: checked }));
              }}
              type="checkbox"
            />
            <span>{tx("安装后启用")}</span>
          </label>
        </div>
      </section>
      <section className="plugin-install-panel plugin-install-preview-panel">
        <div className="plugin-install-panel-head">
          <h3>{tx("安全校验")}</h3>
          <button
            className="secondary-button compact-button plugin-install-preview-button"
            disabled={draft.source !== "url" || !draft.downloadURL.trim() || !draft.checksumSHA256.trim()}
            onClick={onPermissionPreview}
            type="button"
          >
            {tx(permissionPreviewDraft.busy ? "预览中" : "预览权限")}
          </button>
        </div>
        <PluginPermissionDiffPreview
          disabled={draft.source !== "url" || !draft.downloadURL.trim() || !draft.checksumSHA256.trim()}
          draft={permissionPreviewDraft}
          onPreview={onPermissionPreview}
          showAction={false}
        />
      </section>
      <div className="plugin-install-footer">
        <div className="plugin-install-status">
          {draft.error ? <p className="provider-quota-error">{draft.error}</p> : null}
          {draft.result ? <p className="empty-state">{draft.result}</p> : null}
        </div>
        <button className="secondary-button plugin-action-button plugin-install-submit" disabled={draft.busy} type="submit">
          <Download size={14} />
          <span>{tx(draft.busy ? "安装中" : "安装插件")}</span>
        </button>
      </div>
    </form>
  );
}

export function emptyInstallDraft(): PluginInstallDraft {
  return { source: "url", downloadURL: "", checksumSHA256: "", packageFile: null, replace: false, enable: false, busy: false, error: "", result: "" };
}

export function pluginInstallRequestBody(draft: PluginInstallDraft) {
  if (draft.source === "upload") {
    const form = new FormData();
    if (draft.packageFile) form.append("package", draft.packageFile);
    if (draft.checksumSHA256.trim()) form.append("checksum_sha256", draft.checksumSHA256.trim());
    form.append("replace", String(draft.replace));
    form.append("enable", String(draft.enable));
    return form;
  }
  return JSON.stringify({
    download_url: draft.downloadURL,
    checksum_sha256: draft.checksumSHA256,
    replace: draft.replace,
    enable: draft.enable,
  });
}
