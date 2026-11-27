import { useId, useState } from "react";
import type { AllowanceConfig } from "../core/provider-allowance";
import type { ApiContext } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";

function providersIn(text: string): Record<string, unknown>[] {
  try {
    const value = JSON.parse(text);
    return Array.isArray(value)
      ? value
      : Array.isArray(value.providers)
        ? value.providers
        : [value];
  } catch {
    return [];
  }
}

export function AllowanceMagpieImport({
  api,
  draft,
  onImported,
}: {
  api: ApiContext;
  draft: AllowanceConfig;
  onImported: (config: AllowanceConfig) => void;
}) {
  const id = useId();
  const [providers, setProviders] = useState("");
  const [settings, setSettings] = useState("");
  const [providerID, setProviderID] = useState("");
  const [account, setAccount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const choices = providersIn(providers).filter(
    (value) => value && typeof value === "object" && !Array.isArray(value),
  );
  const chosen = choices.find((p) => p.id === providerID) ?? choices[0];
  const refs = new Set<string>();
  for (const name of [
    "accountCaps",
    "accountWindowCaps",
    "accountWindowAmounts",
    "keySpendCaps",
    "keyOwnBalances",
  ]) {
    const map = chosen?.[name];
    if (map && typeof map === "object")
      Object.keys(map).forEach((key) => refs.add(key));
  }
  try {
    const value = JSON.parse(settings);
    for (const name of [
      "codexAutoReset",
      "codexNoCredits",
      "codexCreditsFirst",
      "codexCreditsLast",
    ]) {
      if (
        String((chosen?.id ?? "") + " " + (chosen?.preset ?? ""))
          .toLowerCase()
          .includes("codex") &&
        Array.isArray(value[name])
      )
        value[name].forEach((ref: unknown) => {
          if (typeof ref === "string") refs.add(ref);
        });
    }
  } catch {
    /* An incomplete settings file has no account choices. */
  }
  async function importConfig() {
    setBusy(true);
    setError("");
    try {
      const response = await adminFetch(
        api,
        `/api/admin/provider-resources/${encodeURIComponent(draft.resource_id)}/allowance/import-magpie`,
        {
          method: "POST",
          body: JSON.stringify({
            providers: JSON.parse(providers),
            settings: settings.trim() ? JSON.parse(settings) : {},
            provider_id: chosen?.id ?? "",
            account_ref: account,
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
            revision: draft.revision,
          }),
        },
      );
      if (!response.ok)
        throw new Error(
          await readAdminError(response, tx("Magpie 额度配置导入失败")),
        );
      onImported(await response.json());
      setProviders("");
      setSettings("");
      setProviderID("");
      setAccount("");
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : tx("Magpie 额度配置导入失败"),
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="allowance-magpie-import">
      <summary>{tx("导入 Magpie 额度配置")}</summary>
      <p>
        {tx(
          "导入 providers.json 和可选的 settings.json。选择供应商及账号，迁移余额、窗口限额、开销上限、手动余额、积分、重置和提醒。保存后生效。",
        )}
      </p>
      <label htmlFor={`${id}-providers`}>{tx("Magpie 供应商 JSON")}</label>
      <input
        aria-label={tx("选择 Magpie 供应商配置文件")}
        type="file"
        accept="application/json,.json"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void file.text().then(setProviders);
        }}
      />
      <textarea
        id={`${id}-providers`}
        aria-label={tx("Magpie 供应商 JSON")}
        rows={4}
        value={providers}
        onChange={(event) => {
          setProviders(event.target.value);
          setProviderID("");
          setAccount("");
        }}
      />
      <label htmlFor={`${id}-settings`}>{tx("Magpie 设置 JSON（可选）")}</label>
      <input
        aria-label={tx("选择 Magpie 设置文件")}
        type="file"
        accept="application/json,.json"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void file.text().then(setSettings);
        }}
      />
      <textarea
        id={`${id}-settings`}
        aria-label={tx("Magpie 设置 JSON（可选）")}
        rows={3}
        value={settings}
        onChange={(event) => setSettings(event.target.value)}
      />
      {choices.length > 1 ? (
        <label>
          {tx("Magpie 供应商")}
          <select
            value={String(chosen?.id ?? "")}
            onChange={(event) => {
              setProviderID(event.target.value);
              setAccount("");
            }}
          >
            {choices.map((p, i) => (
              <option key={i} value={String(p.id ?? "")}>
                {String(p.name ?? p.id ?? i + 1)}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      <label>
        {tx("Magpie 账号或 Key ID")}
        <input
          list={`${id}-accounts`}
          value={account}
          placeholder={tx("自动匹配当前账号")}
          onChange={(event) => setAccount(event.target.value)}
        />
        <datalist id={`${id}-accounts`}>
          {[...refs].sort().map((ref) => (
            <option key={ref} value={ref} />
          ))}
        </datalist>
      </label>
      <button
        type="button"
        className="secondary"
        disabled={busy || !providers.trim()}
        onClick={() => void importConfig()}
      >
        {busy ? tx("正在导入…") : tx("填入额度配置")}
      </button>
      {error ? (
        <p role="alert" className="error-banner">
          {error}
        </p>
      ) : null}
    </details>
  );
}
