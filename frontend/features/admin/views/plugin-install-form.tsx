import { Download } from "lucide-react";
import { type Dispatch, type FormEvent, type SetStateAction } from "react";
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

export function PluginInstallForm({
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
  return (
    <section className="section" data-plugin-manager-section="install">
      <div className="section-header">
        <h2>{tx("安装插件包")}</h2>
      </div>
      <div className="section-body">
        <form className="plugin-action-runner" onSubmit={onInstall}>
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
          {draft.source === "url" ? (
            <label className="plugin-action-field" key="plugin-install-url-source">
              <span>{tx("下载 URL")}</span>
              <input
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setDraft((current) => ({ ...current, downloadURL: value }));
                }}
                required
                type="url"
                value={draft.downloadURL}
              />
            </label>
          ) : (
            <label className="plugin-action-field plugin-upload-field" key="plugin-install-upload-source">
              <span>{tx("插件 ZIP 包")}</span>
              <input
                accept=".zip,application/zip,application/x-zip-compressed"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0] ?? null;
                  setDraft((current) => ({ ...current, packageFile: file, error: "" }));
                }}
                required
                type="file"
              />
            </label>
          )}
          <label className="plugin-action-field">
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
          <label className="plugin-action-field">
            <span>{tx("允许替换")}</span>
            <input
              checked={draft.replace}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setDraft((current) => ({ ...current, replace: checked }));
              }}
              type="checkbox"
            />
          </label>
          <label className="plugin-action-field">
            <span>{tx("安装后启用")}</span>
            <input
              checked={draft.enable}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setDraft((current) => ({ ...current, enable: checked }));
              }}
              type="checkbox"
            />
          </label>
          <button className="secondary-button plugin-action-button" disabled={draft.busy} type="submit">
            <Download size={14} />
            <span>{tx(draft.busy ? "安装中" : "安装插件")}</span>
          </button>
          <PluginPermissionDiffPreview
            disabled={draft.source !== "url" || !draft.downloadURL.trim() || !draft.checksumSHA256.trim()}
            draft={permissionPreviewDraft}
            onPreview={onPermissionPreview}
          />
          {draft.error ? <p className="provider-quota-error">{draft.error}</p> : null}
          {draft.result ? <p className="empty-state">{draft.result}</p> : null}
        </form>
      </div>
    </section>
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
