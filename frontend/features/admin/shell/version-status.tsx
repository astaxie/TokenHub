import {
  AlertCircle,
  ArrowRight,
  ArrowUpCircle,
  CheckCircle2,
  ChevronDown,
  Download,
  ExternalLink,
  History,
  LoaderCircle,
  Power,
  RefreshCw,
  RotateCcw,
  Terminal,
  X,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { type AdminUser, type ApiContext } from "../core/types";
import { rollbackCompatibilityReasonText } from "../i18n/db-evolution-reasons";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { GitHubBrandIcon } from "../shared/auth";

type VersionReleaseInfo = {
  version: string;
  name: string;
  published_at: string;
  html_url: string;
};

type SystemVersionInfo = {
  current_version: string;
  latest_version: string;
  has_update: boolean;
  build_type: "source" | "release";
  deployment_type: "source" | "container" | "native";
  update_supported: boolean;
  pending_restart_version?: string;
  releases_url: string;
  release_info?: VersionReleaseInfo;
  cached: boolean;
  warning?: string;
};

type SystemVersionPayload = Omit<SystemVersionInfo, "deployment_type" | "update_supported"> & {
  deployment_type?: string;
  update_supported?: boolean;
};

const managedRestartWaitMs = 210_000;

type RollbackVersionInfo = {
  version: string;
  published_at: string;
  html_url: string;
  compatibility?: string;
  compatibility_reason?: string;
  compatibility_reason_code?: string;
  compatibility_reason_params?: {
    version?: string;
    state?: number;
    release?: string;
    min?: number;
    max?: number;
  };
};

function RollbackConfirmDialog({
  currentVersion,
  targetVersion,
  onCancel,
  onConfirm,
}: {
  currentVersion: string;
  targetVersion: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const dialogRef = useRef<HTMLElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    cancelRef.current?.focus();

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onCancel();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;

      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      );
      if (focusable.length === 0) return;
      event.preventDefault();
      event.stopPropagation();
      const activeIndex = focusable.indexOf(document.activeElement as HTMLElement);
      const nextIndex = event.shiftKey
        ? (activeIndex <= 0 ? focusable.length - 1 : activeIndex - 1)
        : (activeIndex < 0 || activeIndex === focusable.length - 1 ? 0 : activeIndex + 1);
      focusable[nextIndex]?.focus();
    }

    document.addEventListener("keydown", handleKeyDown, true);
    return () => {
      document.removeEventListener("keydown", handleKeyDown, true);
      previouslyFocused?.focus();
    };
  }, [onCancel]);

  return (
    <div
      className="modal-backdrop version-confirm-backdrop"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onCancel();
      }}
      role="presentation"
    >
      <section
        ref={dialogRef}
        aria-describedby="version-rollback-confirm-description"
        aria-labelledby="version-rollback-confirm-title"
        aria-modal="true"
        className="confirm-modal version-confirm-modal"
        role="alertdialog"
      >
        <header className="version-confirm-head">
          <span aria-hidden="true" className="version-confirm-icon">
            <RotateCcw size={20} />
          </span>
          <div>
            <h2 id="version-rollback-confirm-title">
              {tx("确认回退到")} v{targetVersion}
            </h2>
            <p>{tx("此操作将在重启后切换运行版本。")}</p>
          </div>
        </header>

        <div className="version-confirm-route">
          <span>
            <small>{tx("当前版本")}</small>
            <strong>v{currentVersion}</strong>
          </span>
          <ArrowRight aria-hidden="true" size={18} />
          <span>
            <small>{tx("回退目标")}</small>
            <strong>v{targetVersion}</strong>
          </span>
        </div>

        <div className="version-confirm-warning" id="version-rollback-confirm-description">
          <AlertCircle aria-hidden="true" size={17} />
          <span>{tx("回退前请确认数据库与目标版本兼容，并先完成备份。")}</span>
        </div>

        <footer className="modal-actions">
          <button ref={cancelRef} className="secondary-button" onClick={onCancel} type="button">
            {tx("取消")}
          </button>
          <button className="danger-confirm" onClick={onConfirm} type="button">
            <RotateCcw aria-hidden="true" size={16} />
            {tx("确认回退")}
          </button>
        </footer>
      </section>
    </div>
  );
}

const fallbackVersionInfo: SystemVersionInfo = {
  current_version: "",
  latest_version: "",
  has_update: false,
  build_type: "source",
  deployment_type: "source",
  update_supported: false,
  releases_url: "https://github.com/astaxie/TokenHub/releases",
  cached: false,
};

export function ResponsiveVersionStatus({ api, user }: { api: ApiContext; user: AdminUser }) {
  const [target, setTarget] = useState<HTMLElement | null>(null);

  useEffect(() => {
    const media = window.matchMedia("(max-width: 980px)");
    const updateTarget = () => {
      const targetID = media.matches ? "top-version-status" : "sidebar-version-status";
      setTarget(document.getElementById(targetID));
    };
    updateTarget();
    media.addEventListener("change", updateTarget);
    return () => media.removeEventListener("change", updateTarget);
  }, []);

  return target ? createPortal(<VersionStatus api={api} user={user} />, target) : null;
}

export function VersionStatus({ api, user }: { api: ApiContext; user: AdminUser }) {
  const canInspectVersions = ["admin", "system_admin"].includes(user.role.trim().toLowerCase());
  const [info, setInfo] = useState<SystemVersionInfo>(fallbackVersionInfo);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(canInspectVersions);
  const [loadError, setLoadError] = useState("");
  const [rollbackOpen, setRollbackOpen] = useState(false);
  const [rollbackLoading, setRollbackLoading] = useState(false);
  const [rollbackLoaded, setRollbackLoaded] = useState(false);
  const [rollbackError, setRollbackError] = useState("");
  const [rollbackVersions, setRollbackVersions] = useState<RollbackVersionInfo[]>([]);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [systemOperation, setSystemOperation] = useState<"" | "update" | "rollback" | "restart">("");
  const [operationError, setOperationError] = useState("");
  const [pendingRestartVersion, setPendingRestartVersion] = useState("");
  const [appliedOperation, setAppliedOperation] = useState<"" | "update" | "rollback">("");
  const [rollbackConfirmationVersion, setRollbackConfirmationVersion] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const modalRef = useRef<HTMLElement>(null);

  const loadVersion = useCallback(async (force: boolean, signal?: AbortSignal) => {
    if (!canInspectVersions) return;
    setLoading(true);
    setLoadError("");
    try {
      const suffix = force ? "?force=true" : "";
      const response = await adminFetch(api, `/api/admin/system/version${suffix}`, { signal });
      if (!response.ok) {
        throw new Error(await readAdminError(response, tx("检查更新失败")));
      }
      const payload = await response.json() as SystemVersionPayload;
      const normalized = normalizeVersionInfo(payload);
      setInfo(normalized);
      setPendingRestartVersion(normalized.pending_restart_version ?? "");
      setAppliedOperation("");
      if (force) {
        setRollbackConfirmationVersion("");
        setRollbackOpen(false);
        setRollbackLoaded(false);
        setRollbackVersions([]);
        setRollbackError("");
        setSelectedVersion("");
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      setLoadError(error instanceof Error ? error.message : tx("检查更新失败"));
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, [api, canInspectVersions]);

  const loadRollbackVersions = useCallback(async () => {
    if (!canInspectVersions || rollbackLoading) return;
    setRollbackLoading(true);
    setRollbackError("");
    try {
      const response = await adminFetch(api, "/api/admin/system/rollback-versions");
      if (!response.ok) {
        throw new Error(await readAdminError(response, tx("加载版本失败")));
      }
      const payload = await response.json() as { versions?: RollbackVersionInfo[] };
      setRollbackVersions(payload.versions ?? []);
      setRollbackLoaded(true);
    } catch (error) {
      setRollbackError(error instanceof Error ? error.message : tx("加载版本失败"));
    } finally {
      setRollbackLoading(false);
    }
  }, [api, canInspectVersions, rollbackLoading]);

  useEffect(() => {
    if (!canInspectVersions) return;
    const controller = new AbortController();
    void loadVersion(false, controller.signal);
    return () => controller.abort();
  }, [canInspectVersions, loadVersion]);

  useEffect(() => {
    if (!open) return;
    closeRef.current?.focus();
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
        return;
      }
      if (event.key !== "Tab" || !modalRef.current) return;
      const focusable = Array.from(
        modalRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open]);

  function closeModal() {
    setRollbackConfirmationVersion("");
    setOpen(false);
    triggerRef.current?.focus();
  }

  function toggleRollback() {
    const nextOpen = !rollbackOpen;
    setRollbackOpen(nextOpen);
    if (nextOpen && !rollbackLoaded && !rollbackLoading) {
      void loadRollbackVersions();
    }
  }

  async function applyManagedUpdate() {
    if (systemOperation || !info.latest_version) return;
    setSystemOperation("update");
    setOperationError("");
    try {
      const response = await adminFetch(api, "/api/admin/system/update", { method: "POST" });
      if (!response.ok) {
        throw new Error(await readAdminError(response, tx("更新失败")));
      }
      const payload = await response.json() as { target_version?: string };
      setPendingRestartVersion(payload.target_version || info.latest_version);
      setAppliedOperation("update");
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : tx("更新失败"));
    } finally {
      setSystemOperation("");
    }
  }

  async function applyManagedRollback(targetVersion: string) {
    if (systemOperation || !targetVersion) return;
    setRollbackConfirmationVersion("");
    setSystemOperation("rollback");
    setOperationError("");
    try {
      const response = await adminFetch(api, "/api/admin/system/rollback", {
        method: "POST",
        body: JSON.stringify({ version: targetVersion }),
      });
      if (!response.ok) {
        throw new Error(await readAdminError(response, tx("回退失败")));
      }
      const payload = await response.json() as { target_version?: string };
      setPendingRestartVersion(payload.target_version || targetVersion);
      setAppliedOperation("rollback");
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : tx("回退失败"));
    } finally {
      setSystemOperation("");
    }
  }

  async function restartManagedDeployment() {
    if (systemOperation || !pendingRestartVersion) return;
    setSystemOperation("restart");
    setOperationError("");
    try {
      const response = await adminFetch(api, "/api/admin/system/restart", { method: "POST" });
      if (!response.ok) {
        throw new Error(await readAdminError(response, tx("重启失败")));
      }
      await waitForManagedVersion(api, pendingRestartVersion);
      window.location.reload();
    } catch (error) {
      setOperationError(error instanceof Error ? error.message : tx("重启失败"));
      setSystemOperation("");
    }
  }

  if (!canInspectVersions) {
    return null;
  }

  const versionLabel = info.current_version || "...";
  const managedUpdateUnavailable = Boolean(
    info.warning?.includes("does not include managed release assets") ||
      info.warning?.includes("does not include native assets"),
  );
  return (
    <>
      <button
        ref={triggerRef}
        aria-haspopup="dialog"
        className={info.has_update ? "version version-trigger update-available" : "version version-trigger"}
        onClick={() => setOpen(true)}
        title={info.has_update ? tx("发现新版本") : tx("当前版本")}
        type="button"
      >
        <span>v{versionLabel}</span>
        {info.has_update ? <span className="version-update-dot" aria-label={tx("发现新版本")} /> : null}
      </button>

      {open && typeof document !== "undefined"
        ? createPortal(
            <div
              className="modal-backdrop version-modal-backdrop"
              onMouseDown={(event) => {
                if (event.currentTarget === event.target) closeModal();
              }}
              role="presentation"
            >
              <article
                ref={modalRef}
                aria-labelledby="version-modal-title"
                aria-modal="true"
                className="version-modal"
                role="dialog"
              >
                <header className="version-modal-header">
                  <h2 id="version-modal-title">{tx("当前版本")}</h2>
                  <div className="version-modal-actions">
                    <button
                      aria-label={tx("检查更新")}
                      className="icon-button"
                      disabled={loading || Boolean(systemOperation)}
                      onClick={() => void loadVersion(true)}
                      title={tx("检查更新")}
                      type="button"
                    >
                      <RefreshCw className={loading ? "spin" : undefined} size={18} />
                    </button>
                    <button
                      ref={closeRef}
                      aria-label={tx("关闭")}
                      className="icon-button"
                      onClick={closeModal}
                      title={tx("关闭")}
                      type="button"
                    >
                      <X size={18} />
                    </button>
                  </div>
                </header>

                <div className="version-modal-body">
                  <VersionSummary info={info} loading={loading} />

                  {loadError || info.warning ? (
                    <div className="version-message warning" role="status">
                      <AlertCircle aria-hidden="true" size={17} />
                      <span>{versionWarningText(info.warning, loadError)}</span>
                      <button disabled={loading} onClick={() => void loadVersion(true)} type="button">
                        {tx("重试")}
                      </button>
                    </div>
                  ) : null}

                  {operationError ? (
                    <div className="version-message warning" role="alert">
                      <AlertCircle aria-hidden="true" size={17} />
                      <span>{operationError}</span>
                      <button onClick={() => setOperationError("")} type="button">
                        {tx("关闭")}
                      </button>
                    </div>
                  ) : null}

                  {info.has_update && info.release_info ? (
                    <section className="version-release-card">
                      <div className="version-release-icon" aria-hidden="true">
                        <ArrowUpCircle size={20} />
                      </div>
                      <div className="version-release-copy">
                        <span>{tx("最新版本")}</span>
                        <strong>v{info.latest_version}</strong>
                        {info.release_info.published_at ? <small>{formatReleaseDate(info.release_info.published_at)}</small> : null}
                      </div>
                      <a href={info.release_info.html_url} rel="noopener noreferrer" target="_blank">
                        {tx("查看发布")}
                        <ExternalLink size={14} />
                      </a>
                    </section>
                  ) : (
                    <a
                      className="version-release-link"
                      href={info.release_info?.html_url || info.releases_url}
                      rel="noopener noreferrer"
                      target="_blank"
                    >
                      <GitHubBrandIcon size={17} />
                      {info.release_info ? tx("查看发布") : tx("查看全部发布")}
                      <ExternalLink size={13} />
                    </a>
                  )}

                  {pendingRestartVersion ? (
                    <section aria-live="polite" className="version-native-result">
                      <CheckCircle2 aria-hidden="true" size={21} />
                      <div>
                        <strong>
                          {appliedOperation === "rollback"
                            ? tx("回退已应用")
                            : appliedOperation === "update"
                              ? tx("更新已应用")
                              : tx("版本切换已应用")}
                        </strong>
                        <span>v{pendingRestartVersion} · {tx("重启后生效")}</span>
                      </div>
                      <button
                        disabled={systemOperation === "restart"}
                        onClick={() => void restartManagedDeployment()}
                        type="button"
                      >
                        {systemOperation === "restart"
                          ? <LoaderCircle aria-hidden="true" className="spin" size={16} />
                          : <Power aria-hidden="true" size={16} />}
                        {systemOperation === "restart" ? tx("正在重启") : tx("立即重启")}
                      </button>
                    </section>
                  ) : null}

                  {info.has_update && info.update_supported && !pendingRestartVersion ? (
                    <button
                      className="version-native-action"
                      disabled={Boolean(systemOperation) || managedUpdateUnavailable}
                      onClick={() => void applyManagedUpdate()}
                      type="button"
                    >
                      {systemOperation === "update"
                        ? <LoaderCircle aria-hidden="true" className="spin" size={17} />
                        : <Download aria-hidden="true" size={17} />}
                      {systemOperation === "update" ? tx("正在更新") : tx("立即更新")}
                    </button>
                  ) : null}

                  {info.has_update && info.deployment_type === "container" && !info.update_supported ? (
                    <div className="version-message neutral">
                      <Terminal aria-hidden="true" size={17} />
                      <span>{tx("容器部署请使用初始部署时的 Compose 文件和环境配置手动切换版本。")}</span>
                    </div>
                  ) : null}

                  {info.has_update && info.deployment_type === "source" ? (
                    <div className="version-message neutral">
                      <Terminal aria-hidden="true" size={17} />
                      <span>{tx("源码部署请根据发布说明手动切换版本。")}</span>
                    </div>
                  ) : null}

                  <section className={rollbackOpen ? "version-rollback open" : "version-rollback"}>
                    <button
                      aria-expanded={rollbackOpen}
                      className="version-rollback-trigger"
                      onClick={toggleRollback}
                      type="button"
                    >
                      <span>
                        <History size={17} />
                        {tx("版本回退")}
                      </span>
                      <ChevronDown size={17} />
                    </button>

                    {rollbackOpen ? (
                      <div className="version-rollback-content">
                        <p>{tx("选择要回退到的版本（近 3 个版本）")}</p>
                        {rollbackLoading ? (
                          <div className="version-list-state" role="status">
                            <LoaderCircle className="spin" size={20} />
                            <span>{tx("正在加载")}</span>
                          </div>
                        ) : rollbackError ? (
                          <div className="version-message warning" role="alert">
                            <AlertCircle aria-hidden="true" size={17} />
                            <span>{tx("加载版本失败")}</span>
                            <button onClick={() => void loadRollbackVersions()} type="button">{tx("重试")}</button>
                          </div>
                        ) : rollbackLoaded && rollbackVersions.length === 0 ? (
                          <div className="version-list-state">{tx("暂无可回退的版本")}</div>
                        ) : (
                          <fieldset className="version-options">
                            <legend className="sr-only">{tx("版本回退")}</legend>
                            {rollbackVersions.map((version) => {
                              // Without a compatibility verdict (older backend) the option
                              // stays selectable; a verdict that is not "compatible" blocks
                              // rollback until the database state or record changes.
                              const blocked = version.compatibility !== undefined && version.compatibility !== "compatible";
                              return (
                                <div
                                  className={selectedVersion === version.version ? "version-option selected" : "version-option"}
                                  key={version.version}
                                >
                                  <label title={blocked ? rollbackCompatibilityReasonText(version) : undefined}>
                                    <input
                                      checked={selectedVersion === version.version}
                                      disabled={blocked}
                                      name="rollback-version"
                                      onChange={() => {
                                        if (!blocked) setSelectedVersion(version.version);
                                      }}
                                      type="radio"
                                      value={version.version}
                                    />
                                    <strong>v{version.version}</strong>
                                    <span className="version-option-meta">
                                      <span>{formatReleaseDate(version.published_at)}</span>
                                      {version.compatibility === "compatible" ? (
                                        <em className="version-compat-badge ok">{tx("数据库兼容")}</em>
                                      ) : blocked ? (
                                        <em className="version-compat-badge blocked">
                                          {version.compatibility === "incompatible" ? tx("数据库不兼容") : tx("兼容性未知")}
                                        </em>
                                      ) : null}
                                    </span>
                                  </label>
                                  <a
                                    aria-label={`${tx("查看发布")} v${version.version}`}
                                    href={version.html_url}
                                    rel="noopener noreferrer"
                                    target="_blank"
                                  >
                                    <ExternalLink size={14} />
                                  </a>
                                </div>
                              );
                            })}
                          </fieldset>
                        )}

                        {selectedVersion && info.deployment_type === "container" && !info.update_supported ? (
                          <>
                            <div className="version-message neutral">
                              <Terminal aria-hidden="true" size={17} />
                              <span>{tx("容器部署请使用初始部署时的 Compose 文件和环境配置手动切换版本。")}</span>
                            </div>
                            <div className="version-rollback-warning">
                              <AlertCircle aria-hidden="true" size={15} />
                              <span>{tx("回退前请确认数据库与目标版本兼容，并先完成备份。")}</span>
                            </div>
                          </>
                        ) : null}

                        {selectedVersion && info.update_supported && !pendingRestartVersion ? (
                          <>
                            <button
                              className="version-native-action rollback"
                              disabled={Boolean(systemOperation)}
                              onClick={() => setRollbackConfirmationVersion(selectedVersion)}
                              type="button"
                            >
                              {systemOperation === "rollback"
                                ? <LoaderCircle aria-hidden="true" className="spin" size={17} />
                                : <RotateCcw aria-hidden="true" size={17} />}
                              {systemOperation === "rollback" ? tx("正在回退") : tx("回退到所选版本")}
                            </button>
                            <div className="version-rollback-warning">
                              <AlertCircle aria-hidden="true" size={15} />
                              <span>{tx("回退前请确认数据库与目标版本兼容，并先完成备份。")}</span>
                            </div>
                          </>
                        ) : null}

                        {selectedVersion && info.deployment_type === "source" ? (
                          <div className="version-message neutral">
                            <Terminal aria-hidden="true" size={17} />
                            <span>{tx("源码部署请根据发布说明手动切换版本。")}</span>
                          </div>
                        ) : null}
                      </div>
                    ) : null}
                  </section>
                </div>
              </article>
              {rollbackConfirmationVersion ? (
                <RollbackConfirmDialog
                  currentVersion={info.current_version}
                  onCancel={() => setRollbackConfirmationVersion("")}
                  onConfirm={() => void applyManagedRollback(rollbackConfirmationVersion)}
                  targetVersion={rollbackConfirmationVersion}
                />
              ) : null}
            </div>,
            document.querySelector(".app-shell") ?? document.body,
          )
        : null}
    </>
  );
}

function VersionSummary({ info, loading }: { info: SystemVersionInfo; loading: boolean }) {
  const hasRelease = Boolean(info.release_info);
  return (
    <div className="version-summary">
      <div className="version-summary-line">
        <strong>v{info.current_version}</strong>
        {loading ? (
          <LoaderCircle aria-label={tx("正在检查版本")} className="spin" size={23} />
        ) : info.has_update ? (
          <ArrowUpCircle aria-label={tx("发现新版本")} className="update" size={25} />
        ) : hasRelease ? (
          <CheckCircle2 aria-label={tx("已是最新版本")} className="current" size={25} />
        ) : (
          <span aria-hidden="true" className="neutral"><GitHubBrandIcon size={24} /></span>
        )}
      </div>
      <p>
        {loading
          ? tx("正在检查版本")
          : info.has_update
            ? `${tx("发现新版本")} v${info.latest_version}`
            : hasRelease
              ? tx("已是最新版本")
              : tx("GitHub 暂无正式 Release。")}
      </p>
      <span className="version-build-type">
        {info.deployment_type === "native"
          ? tx("原生 Release")
          : info.deployment_type === "container"
            ? tx("容器构建")
            : tx("源码构建")}
      </span>
    </div>
  );
}

function normalizeVersionInfo(payload: SystemVersionPayload): SystemVersionInfo {
  const deploymentType = payload.deployment_type === "native" ||
    payload.deployment_type === "container" ||
    payload.deployment_type === "source"
    ? payload.deployment_type
    : payload.build_type === "release"
      ? "container"
      : "source";
  const updateSupported = typeof payload.update_supported === "boolean"
    ? payload.update_supported
    : deploymentType === "native";
  return {
    ...payload,
    deployment_type: deploymentType,
    update_supported: updateSupported,
  };
}

async function waitForManagedVersion(api: ApiContext, targetVersion: string) {
  const deadline = Date.now() + managedRestartWaitMs;
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 1_000));
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 4_000);
    try {
      const response = await adminFetch(api, "/api/admin/system/version", {
        cache: "no-store",
        signal: controller.signal,
      });
      if (response.ok) {
        const payload = await response.json() as SystemVersionPayload;
        if (normalizeVersionInfo(payload).current_version === targetVersion) return;
      }
    } catch {
      // A temporary connection failure is expected while the service supervisor restarts TokenHub.
    } finally {
      window.clearTimeout(timeout);
    }
  }
  throw new Error(tx("服务未在预期时间内恢复，请刷新页面重试。"));
}

function formatReleaseDate(value: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(languageLocale(), {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

function versionWarningText(warning: string | undefined, loadError: string) {
  if (warning?.includes("semantic release version")) {
    return tx("当前构建不是语义化发布版本，无法自动比较更新。");
  }
  if (
    warning?.includes("does not include managed release assets") ||
    warning?.includes("does not include native assets")
  ) {
    return tx("最新版本不包含当前平台可用的原生安装包。");
  }
  return loadError || tx("暂时无法检查 GitHub Release，请稍后重试。");
}
