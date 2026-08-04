import { RotateCcw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ApiContext, ProviderResource } from "../core/types";
import { activeLanguage, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { providerResourceAccountLabel, QuotaMetric } from "./provider-account-ui";

type CodexResetCredit = {
  id?: string;
  status?: string;
  expires_at?: string | null;
};

type CodexResetCredits = {
  available_count: number;
  credits?: CodexResetCredit[];
  fetched_at?: number;
  pending_operation?: {
    idempotency_key: string;
    expected_available_count: number;
    credit_id: string;
    expires_at?: string | null;
    state: "pending" | "unknown";
  };
};

type ResetConfirmation = {
  availableCount: number;
  creditID: string;
  expiresAt?: string | null;
  idempotencyKey: string;
  attempted: boolean;
};

class ResetRequestError extends Error {
  constructor(message: string, readonly code: string) {
    super(message);
    this.name = "ResetRequestError";
  }
}

export function ProviderAccountQuotaReset({
  api,
  quotaBusy,
  resource,
  onRefreshQuota,
}: {
  api: ApiContext;
  quotaBusy: boolean;
  resource: ProviderResource;
  onRefreshQuota: () => Promise<boolean>;
}) {
  const [details, setDetails] = useState<CodexResetCredits | null>(null);
  const [detailsBusy, setDetailsBusy] = useState(false);
  const [detailsError, setDetailsError] = useState("");
  const [selectedCreditID, setSelectedCreditID] = useState("");
  const [confirmation, setConfirmation] = useState<ResetConfirmation | null>(() => readStoredResetConfirmation(resource.id));
  const [resetBusy, setResetBusy] = useState(false);
  const [resetError, setResetError] = useState("");
  const [resetNotice, setResetNotice] = useState("");
  const [now, setNow] = useState(() => Date.now());
  const quotaWasBusy = useRef(quotaBusy);
  const resourcePath = `/api/admin/provider-resources/${encodeURIComponent(resource.id)}/quota`;

  const loadResetCredits = useCallback(async () => {
    setDetailsBusy(true);
    setDetailsError("");
    setDetails(null);
    try {
      const resp = await adminFetch(api, `${resourcePath}/reset-credits`);
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("查询 Codex 重置次数")));
      const next = (await resp.json()) as CodexResetCredits;
      setDetails(next);
      setSelectedCreditID((current) => resetCreditIsUsable(next.credits, current, Date.now()) ? current : "");
      if (validPendingOperation(next.pending_operation)) {
        const recovered: ResetConfirmation = {
          availableCount: next.pending_operation.expected_available_count,
          creditID: next.pending_operation.credit_id,
          expiresAt: next.pending_operation.expires_at,
          idempotencyKey: next.pending_operation.idempotency_key,
          attempted: true,
        };
        setConfirmation((current) => current?.idempotencyKey === recovered.idempotencyKey ? current : recovered);
        storeResetConfirmation(resource.id, recovered);
      }
      setNow(Date.now());
      return true;
    } catch (error) {
      if (isAuthExpiredError(error)) return false;
      setDetailsError(error instanceof Error ? error.message : tx("查询 Codex 重置次数失败"));
      return false;
    } finally {
      setDetailsBusy(false);
    }
  }, [api, resource.id, resourcePath]);

  useEffect(() => {
    void loadResetCredits();
  }, [loadResetCredits]);

  useEffect(() => {
    const quotaRefreshFinished = quotaWasBusy.current && !quotaBusy;
    quotaWasBusy.current = quotaBusy;
    if (quotaRefreshFinished && !resetBusy) void loadResetCredits();
  }, [loadResetCredits, quotaBusy, resetBusy]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const rawAvailableCount = details?.available_count;
  const availableCount = typeof rawAvailableCount === "number" && Number.isFinite(rawAvailableCount)
    ? Math.max(0, rawAvailableCount)
    : null;
  const availableCredits = useMemo(() => selectAvailableCredits(details?.credits, now), [details?.credits, now]);
  const earliestCredit = availableCredits[0];
  const selectedCredit = availableCredits.find((credit) => credit.id === selectedCreditID);
  const expiresRelative = formatResetCreditExpiry(earliestCredit?.expires_at, now);
  const expiresAbsolute = formatResetCreditDate(earliestCredit?.expires_at);
  const detailsNeedRefresh = Boolean(availableCount && availableCredits.length === 0);
  const canReset = !confirmation && resource.status === "active" && availableCount !== null && availableCount > 0 && Boolean(selectedCredit?.id) && !detailsNeedRefresh && !detailsBusy && !detailsError && !quotaBusy && !resetBusy;
  const confirmationExpiry = confirmation?.expiresAt;
  const confirmationExpired = Boolean(confirmation && hasExpiryValue(confirmationExpiry) && (!validExpiry(confirmationExpiry) || Date.parse(confirmationExpiry!) <= now));
  const confirmationCanSubmit = Boolean(confirmation && (confirmation.attempted || !confirmationExpired) && !resetBusy);

  function requestReset() {
    if (!canReset || availableCount === null || !selectedCredit?.id) return;
    setResetError("");
    setResetNotice("");
    setConfirmation({
      availableCount,
      creditID: selectedCredit.id,
      expiresAt: selectedCredit.expires_at,
      idempotencyKey: crypto.randomUUID(),
      attempted: false,
    });
  }

  function closeConfirmation() {
    if (resetBusy || confirmation?.attempted) return;
    clearStoredResetConfirmation(resource.id);
    setConfirmation(null);
    setResetError("");
  }

  async function confirmReset() {
    if (!confirmation || resetBusy) return;
    const operation = { ...confirmation, attempted: true };
    if (!storeResetConfirmation(resource.id, operation)) {
      setResetError(tx("无法保存本次重置的安全状态，请检查浏览器存储权限后重试。"));
      return;
    }
    setConfirmation(operation);
    setResetBusy(true);
    setResetError("");
    try {
      const resp = await adminFetch(api, `${resourcePath}/reset`, {
        method: "POST",
        headers: {
          "idempotency-key": operation.idempotencyKey,
          "x-tokenhub-dangerous-operation": "codex-quota-reset",
        },
        body: JSON.stringify({
          confirm: true,
          idempotency_key: operation.idempotencyKey,
          expected_available_count: operation.availableCount,
          credit_id: operation.creditID,
        }),
      });
      if (!resp.ok) throw await readResetError(resp);
      const result = await resp.json().catch(() => ({})) as { code?: string; windows_reset?: number };
      if (result.code !== "reset" && result.code !== "already_redeemed") {
        throw new Error(tx("重置请求返回未知结果。请保留当前弹窗并直接重试。"));
      }
      clearStoredResetConfirmation(resource.id);
      const completed = result.code === "already_redeemed" ? tx("该重置请求此前已完成，正在刷新额度。") : tx("重置请求已完成，正在刷新额度。");
      const windowsReset = typeof result.windows_reset === "number" && Number.isFinite(result.windows_reset) ? result.windows_reset : null;
      setSelectedCreditID("");
      setConfirmation(null);
      const [creditsRefreshed, quotaRefreshed] = await Promise.all([loadResetCredits(), onRefreshQuota()]);
      const completionNotice = windowsReset !== null ? `${completed} ${tx("受影响窗口")}：${windowsReset}` : completed;
      setResetNotice(completionNotice);
      if (!creditsRefreshed || !quotaRefreshed) {
        setResetError(tx("重置已经完成，但最新用量或重置次数刷新失败；旧数据已清除，请点击“刷新用量与重置次数”重试。"));
        return;
      }
      setResetError("");
    } catch (error) {
      if (isAuthExpiredError(error)) {
        clearStoredResetConfirmation(resource.id);
        return;
      }
      if (error instanceof ResetRequestError && resetErrorIsFinal(error.code)) {
        clearStoredResetConfirmation(resource.id);
        setConfirmation((current) => current ? { ...current, attempted: false } : current);
      }
      setResetError(error instanceof Error ? error.message : tx("重置 Codex 用量窗口失败"));
    } finally {
      setResetBusy(false);
    }
  }

  return (
    <div className="provider-quota-details">
      <div className="provider-quota-grid">
        <QuotaMetric label="剩余重置次数" value={detailsBusy ? "查询中" : availableCount === null ? "-" : String(availableCount)} />
        <QuotaMetric label="最近可用次数过期" value={expiresRelative} />
        <QuotaMetric label="最近可用次数过期时间" value={expiresAbsolute} />
      </div>
      {availableCredits.length > 0 ? (
        <fieldset className="provider-reset-credit-picker" disabled={detailsBusy || quotaBusy || resetBusy || Boolean(confirmation)}>
          <legend>{tx("选择要使用的重置次数")}</legend>
          <div className="provider-reset-credit-options">
            {availableCredits.map((credit, index) => (
              <label className={`provider-reset-credit-option${selectedCreditID === credit.id ? " selected" : ""}`} key={credit.id}>
                <input
                  checked={selectedCreditID === credit.id}
                  name={`quota-reset-credit-${resource.id}`}
                  onChange={() => setSelectedCreditID(credit.id || "")}
                  type="radio"
                  value={credit.id}
                />
                <span>
                  <strong>{tx("重置次数")} {index + 1}</strong>
                  <small>{formatResetCreditExpiry(credit.expires_at, now)} · {formatResetCreditDate(credit.expires_at)}</small>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
      ) : null}
      <div className="provider-quota-card-actions">
        <button className="secondary-button" disabled={!canReset} onClick={requestReset} type="button">
          <RotateCcw size={14} />
          {tx("重置套餐")}
        </button>
      </div>
      {detailsError ? <p className="provider-quota-error" role="alert">{detailsError}</p> : null}
      {detailsNeedRefresh ? <p className="provider-quota-error" role="alert">{tx("可用重置次数明细暂不可用、已过期或不一致，请点击“刷新用量与重置次数”重新查询。")}</p> : null}
      {!confirmation && resetError ? <p className="provider-quota-error" role="alert">{resetError}</p> : null}
      {resetNotice ? <p className="provider-credential-note" role="status">{resetNotice}</p> : null}
      {confirmation ? (
        <div className="modal-backdrop provider-account-confirmation-backdrop" role="presentation">
          {resetBusy ? (
            <div aria-labelledby="provider-quota-reset-progress-title" aria-modal="true" className="confirm-modal provider-account-reset-progress-modal" role="dialog">
              <RotateCcw aria-hidden="true" className="provider-account-reset-spinner" size={64} />
              <div>
                <p className="eyebrow">{tx("Codex 额度安全操作")}</p>
                <h2 id="provider-quota-reset-progress-title">{tx("正在重置 Codex 套餐")}</h2>
                <p>{tx("正在提交重置并刷新用量与重置次数，请勿关闭页面或重复操作。")}</p>
              </div>
            </div>
          ) : (
            <div aria-labelledby="provider-quota-reset-confirmation-title" aria-modal="true" className="confirm-modal provider-account-confirmation-modal" role="dialog">
              <div>
                <p className="eyebrow">{tx("Codex 额度安全操作")}</p>
                <h2 id="provider-quota-reset-confirmation-title">{tx("确认重置 Codex 用量窗口")}</h2>
              </div>
              <div className="provider-account-confirmation-target">
                <span>{resource.name} · {resource.id}</span>
                <strong>{providerResourceAccountLabel(resource)}</strong>
              </div>
              <div className="provider-quota-grid">
                <QuotaMetric label="当前剩余重置次数" value={String(confirmation.availableCount)} />
                <QuotaMetric label="选中次数过期" value={formatResetCreditExpiry(confirmation.expiresAt, now)} />
                <QuotaMetric label="选中次数过期时间" value={formatResetCreditDate(confirmation.expiresAt)} />
              </div>
              <p>{tx("确认后将消耗 1 次重置次数，并立即重置当前 Codex 用量窗口；不会更改账号套餐类型。")}</p>
              <p className="provider-quota-error">{tx("已使用的重置次数无法恢复。")}</p>
              {confirmation.attempted ? <p className="provider-quota-error">{tx("该操作已经提交。为避免重复消耗，请保留本弹窗并使用同一次操作重试，直到获得确定结果。")}</p> : null}
              {!confirmation.attempted && confirmationExpired ? <p className="provider-quota-error">{tx("选中的重置次数已经过期，请取消并刷新额度。")}</p> : null}
              {resetError ? <p className="provider-quota-error" role="alert">{resetError}</p> : null}
              <div className="modal-actions">
                <button className="secondary-button" disabled={confirmation.attempted} onClick={closeConfirmation} type="button">{tx("取消")}</button>
                <button className="danger-confirm" disabled={!confirmationCanSubmit} onClick={() => void confirmReset()} type="button">
                  {tx("确认重置")}
                </button>
              </div>
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}

function selectAvailableCredits(credits: CodexResetCredit[] | undefined, now: number) {
  const available = (credits ?? []).filter((credit) => credit.status?.trim().toLowerCase() === "available");
  const usable = available.filter((credit) => !hasExpiryValue(credit.expires_at) || (validExpiry(credit.expires_at) && Date.parse(credit.expires_at!) > now));
  usable.sort((left, right) => expirySortValue(left.expires_at) - expirySortValue(right.expires_at));
  return usable.filter((credit): credit is CodexResetCredit & { id: string } => Boolean(credit.id));
}

function resetCreditIsUsable(credits: CodexResetCredit[] | undefined, creditID: string, now: number) {
  return Boolean(creditID && selectAvailableCredits(credits, now).some((credit) => credit.id === creditID));
}

function expirySortValue(value?: string | null) {
  return validExpiry(value) ? Date.parse(value!) : Number.POSITIVE_INFINITY;
}

function validExpiry(value?: string | null) {
  return Boolean(value && Number.isFinite(Date.parse(value)));
}

function hasExpiryValue(value?: string | null) {
  return typeof value === "string" && value.trim() !== "";
}

function formatResetCreditExpiry(value: string | null | undefined, now: number) {
  if (!validExpiry(value)) return "-";
  const milliseconds = Date.parse(value!) - now;
  if (milliseconds <= 0) return tx("已过期");
  const totalMinutes = Math.max(1, Math.ceil(milliseconds / 60_000));
  const days = Math.floor(totalMinutes / 1_440);
  const hours = Math.floor((totalMinutes % 1_440) / 60);
  const minutes = totalMinutes % 60;
  if (activeLanguage === "en") {
    if (days > 0) return `${days} ${days === 1 ? "day" : "days"} ${hours} ${hours === 1 ? "hour" : "hours"} left`;
    if (hours > 0) return `${hours} ${hours === 1 ? "hour" : "hours"} ${minutes} ${minutes === 1 ? "minute" : "minutes"} left`;
    return `${minutes} ${minutes === 1 ? "minute" : "minutes"} left`;
  }
  if (activeLanguage === "ja") {
    if (days > 0) return `${days}日${hours}時間後`;
    if (hours > 0) return `${hours}時間${minutes}分後`;
    return `${minutes}分後`;
  }
  if (days > 0) return `${days}天${hours}小时后`;
  if (hours > 0) return `${hours}小时${minutes}分钟后`;
  return `${minutes}分钟后`;
}

function formatResetCreditDate(value?: string | null) {
  if (!validExpiry(value)) return "-";
  return new Date(value!).toLocaleString(languageLocale());
}

async function readResetError(resp: Response) {
  const payload = await resp.clone().json().catch(() => null) as { code?: string; error?: { code?: string } } | null;
  const message = await readAdminError(resp, tx("重置 Codex 用量窗口"));
  const code = payload?.error?.code || payload?.code;
  const rendered = code === "openai_quota_reset_outcome_unknown"
    ? `${message} ${tx("上游结果未知。请保留当前弹窗并直接重试，系统会复用同一个幂等键和重置次数。")}`
    : message;
  return new ResetRequestError(rendered, code || "unknown");
}

function resetErrorIsFinal(code: string) {
  return new Set([
    "quota_reset_available_count_changed",
    "quota_reset_credit_unavailable",
    "quota_reset_ineligible",
    "quota_reset_no_credit",
    "quota_reset_nothing_to_reset",
    "quota_reset_operation_failed",
    "quota_reset_operation_mismatch",
    "provider_resource_inactive",
    "provider_resource_quota_reset_unsupported",
    "openai_quota_reset_forbidden",
  ]).has(code);
}

function resetConfirmationStorageKey(resourceID: string) {
  return `tokenhub.codex-quota-reset.${resourceID}`;
}

function storeResetConfirmation(resourceID: string, confirmation: ResetConfirmation) {
  if (typeof window === "undefined") return false;
  try {
    window.localStorage.setItem(resetConfirmationStorageKey(resourceID), JSON.stringify(confirmation));
    return true;
  } catch {
    return false;
  }
}

function clearStoredResetConfirmation(resourceID: string) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.removeItem(resetConfirmationStorageKey(resourceID));
  } catch {
    // The server-side operation record remains the source of truth.
  }
}

function readStoredResetConfirmation(resourceID: string): ResetConfirmation | null {
  if (typeof window === "undefined") return null;
  try {
    const value = JSON.parse(window.localStorage.getItem(resetConfirmationStorageKey(resourceID)) || "null") as Partial<ResetConfirmation> | null;
    if (!value || typeof value.availableCount !== "number" || !Number.isFinite(value.availableCount) || typeof value.creditID !== "string" || !value.creditID || typeof value.idempotencyKey !== "string" || !value.idempotencyKey) return null;
    return { availableCount: value.availableCount, creditID: value.creditID, expiresAt: value.expiresAt, idempotencyKey: value.idempotencyKey, attempted: Boolean(value.attempted) };
  } catch {
    return null;
  }
}

function validPendingOperation(value: CodexResetCredits["pending_operation"]): value is NonNullable<CodexResetCredits["pending_operation"]> {
  return Boolean(value && (value.state === "pending" || value.state === "unknown") && typeof value.idempotency_key === "string" && value.idempotency_key && typeof value.credit_id === "string" && value.credit_id && typeof value.expected_available_count === "number" && Number.isFinite(value.expected_available_count));
}
