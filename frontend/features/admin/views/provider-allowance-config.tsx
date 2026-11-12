import { useEffect, useState } from "react";
import type { AdminResource, ApiContext } from "../core/types";
import type {
  AllowanceConfig,
  AllowanceEntry,
  AllowancePreset,
} from "../core/provider-allowance";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { AllowanceSourceFields } from "./provider-allowance-source";
import { AllowancePolicyFields } from "./provider-allowance-policy";

export function ProviderAllowanceConfigForm({
  api,
  entry,
  entries = [],
  presets,
  onSaved,
  readOnly = false,
}: {
  api: ApiContext;
  entry: AllowanceEntry;
  entries?: AllowanceEntry[];
  presets: AllowancePreset[];
  onSaved: () => Promise<void>;
  readOnly?: boolean;
}) {
  const [draft, setDraft] = useState<AllowanceConfig>(() =>
    structuredClone(entry.config),
  );
  const [tab, setTab] = useState<"source" | "policy">("source");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [channels, setChannels] = useState<AdminResource[]>([]);
  useEffect(() => {
    if (tab !== "policy") return;
    let active = true;
    void (async () => {
      try {
        const response = await adminFetch(
          api,
          "/api/admin/notification-channels",
        );
        if (response.ok) {
          const result = await response.json();
          if (active) setChannels(result.data ?? []);
        }
      } catch {
        /* Quota settings remain available when channel access is unavailable. */
      }
    })();
    return () => {
      active = false;
    };
  }, [api, tab]);
  async function save(event: React.FormEvent) {
    event.preventDefault();
    if (readOnly) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const response = await adminFetch(
        api,
        `/api/admin/provider-resources/${encodeURIComponent(entry.resource_id)}/allowance/config`,
        { method: "PUT", body: JSON.stringify(draft) },
      );
      if (!response.ok)
        throw new Error(await readAdminError(response, tx("额度配置保存失败")));
      setDraft(await response.json());
      setSaved(true);
      await onSaved();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : tx("额度配置保存失败"));
    } finally {
      setSaving(false);
    }
  }
  return (
    <form className="allowance-config" onSubmit={(event) => void save(event)}>
      <div
        className="allowance-tabs"
        role="group"
        aria-label={tx("额度配置分类")}
      >
        <button
          type="button"
          aria-pressed={tab === "source"}
          onClick={() => setTab("source")}
        >
          {tx("额度采集")}
        </button>
        <button
          type="button"
          aria-pressed={tab === "policy"}
          onClick={() => setTab("policy")}
        >
          {tx("使用与权益")}
        </button>
      </div>
      <fieldset disabled={readOnly} className="allowance-fields">
        {tab === "source" ? (
          <AllowanceSourceFields
            draft={draft}
            entries={entries}
            presets={presets}
            onChange={setDraft}
          />
        ) : (
          <AllowancePolicyFields
            channels={channels}
            draft={draft}
            onChange={setDraft}
          />
        )}
      </fieldset>
      {error ? (
        <p className="error-banner" role="alert">
          {error}
        </p>
      ) : null}
      {saved ? <p role="status">{tx("额度配置已保存")}</p> : null}
      {!readOnly ? (
        <div className="allowance-save">
          <button className="primary" type="submit" disabled={saving}>
            {saving ? tx("保存中…") : tx("保存额度配置")}
          </button>
          <small>
            {tx("保存后后台刷新。查询失败不会清空上次成功的数据。")}
          </small>
        </div>
      ) : null}
    </form>
  );
}
