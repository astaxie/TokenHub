import {
  allowanceFieldRows,
  type AllowanceFieldRow,
} from "../core/provider-allowance-rows";
import type {
  AllowanceConfig,
  AllowanceEntry,
  AllowancePreset,
  AllowanceSource,
  AllowanceWindowRule,
} from "../core/provider-allowance";
import { tx } from "../i18n/runtime";

export function AllowanceSourceFields({
  draft,
  entries,
  presets,
  onChange,
  headers,
  onHeadersChange,
}: {
  draft: AllowanceConfig;
  headers: AllowanceFieldRow<string>[];
  onHeadersChange: (rows: AllowanceFieldRow<string>[]) => void;
  entries: AllowanceEntry[];
  presets: AllowancePreset[];
  onChange: (value: AllowanceConfig) => void;
}) {
  const source = draft.source;
  const set = (patch: Partial<AllowanceSource>) =>
    onChange({ ...draft, source: { ...source, ...patch } });
  const windows = source.windows ?? [];
  const updateWindow = (index: number, patch: Partial<AllowanceWindowRule>) =>
    set({
      windows: windows.map((window, i) =>
        i === index ? { ...window, ...patch } : window,
      ),
    });
  return (
    <>
      <label className="allowance-check">
        <input
          type="checkbox"
          checked={source.enabled}
          onChange={(event) => set({ enabled: event.target.checked })}
        />
        {tx("启用通用额度采集")}
      </label>
      <p className="muted">
        {tx("关闭时使用订阅适配器的额度查询。API Key 可单独配置余额接口。")}
      </p>
      <div className="allowance-form-grid">
        <label>
          {tx("额度预设")}
          <select
            value={source.preset ?? ""}
            onChange={(event) => {
              const preset = presets.find(
                (item) => item.id === event.target.value,
              );
              if (!preset) {
                set({ preset: "", expression: "", windows: [] });
                return;
              }
              onHeadersChange(allowanceFieldRows(preset.config.headers));
              onChange({
                ...draft,
                source: {
                  ...preset.config,
                  enabled: source.enabled,
                  preset: preset.id,
                  refresh_seconds: source.refresh_seconds,
                  shared_account_id: source.shared_account_id,
                  shared_account_revision: source.shared_account_revision,
                },
              });
            }}
          >
            <option value="">{tx("自定义额度")}</option>
            {presets.map((preset) => (
              <option key={preset.id} value={preset.id}>
                {preset.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          {tx("刷新间隔（秒）")}
          <input
            type="number"
            min={30}
            max={86400}
            value={source.refresh_seconds || 60}
            onChange={(event) =>
              set({ refresh_seconds: Number(event.target.value) })
            }
          />
        </label>
        <label className="wide">
          {tx("额度接口 URL")}
          <input
            type="url"
            value={source.url ?? ""}
            onChange={(event) => set({ url: event.target.value })}
            placeholder="https://provider.example/v1/usage"
          />
        </label>
        <label>
          {tx("请求方法")}
          <select
            value={source.method || "GET"}
            onChange={(event) => set({ method: event.target.value })}
          >
            <option>GET</option>
            <option>POST</option>
          </select>
        </label>
        <label>
          {tx("认证方式")}
          <select
            value={source.auth || "provider"}
            onChange={(event) => set({ auth: event.target.value })}
          >
            <option value="provider">{tx("使用账号密钥")}</option>
            <option value="bearer">Bearer</option>
            <option value="api_key">x-api-key</option>
            <option value="raw">Authorization</option>
            <option value="volcengine">Volcengine HMAC</option>
            <option value="tencent">Tencent TC3</option>
            <option value="alibaba">Alibaba ACS3</option>
            <option value="none">{tx("无需认证")}</option>
          </select>
        </label>
        {["volcengine", "tencent", "alibaba"].includes(source.auth ?? "") ? (
          <>
            <label>
              {tx("云账号 AccessKey ID")}
              <input
                value={source.access_key_id ?? ""}
                onChange={(event) => set({ access_key_id: event.target.value })}
              />
            </label>
            <label>
              {tx("云账号区域")}
              <input
                value={source.region ?? ""}
                onChange={(event) => set({ region: event.target.value })}
              />
            </label>
            {source.auth === "tencent" ? (
              <label>
                {tx("套餐 TeamId")}
                <input
                  value={source.plan_id ?? ""}
                  onChange={(event) => set({ plan_id: event.target.value })}
                />
              </label>
            ) : null}
          </>
        ) : null}
        <label className="wide">
          {tx("独立额度令牌")}
          <input
            type="password"
            autoComplete="new-password"
            value={draft.token ?? ""}
            onChange={(event) =>
              onChange({ ...draft, token: event.target.value })
            }
            placeholder={
              draft.has_token ? tx("已保存，留空保留") : tx("留空使用账号密钥")
            }
          />
        </label>
        {draft.has_token ? (
          <label className="allowance-check">
            <input
              type="checkbox"
              checked={draft.clear_token ?? false}
              onChange={(event) =>
                onChange({ ...draft, clear_token: event.target.checked })
              }
            />
            {tx("删除独立额度令牌")}
          </label>
        ) : null}
        <label className="wide">
          {tx("额度请求体")}
          <textarea
            rows={2}
            value={source.body ?? ""}
            onChange={(event) => set({ body: event.target.value })}
            placeholder="{}"
          />
        </label>
        <label className="wide">
          {tx("余额表达式")}
          <textarea
            aria-label={tx("余额表达式")}
            rows={3}
            value={source.expression ?? ""}
            onChange={(event) => set({ expression: event.target.value })}
            placeholder="$remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %"
          />
          <small>
            {tx("支持 Magpie 的字段路径、计算、筛选、聚合和多段表达式。")}
          </small>
        </label>
        <label>
          {tx("共享余额账号")}
          <select
            value={source.shared_account_id ?? ""}
            onChange={(event) =>
              set({
                shared_account_id: event.target.value,
                shared_account_revision: undefined,
              })
            }
          >
            <option value="">{tx("独立余额")}</option>
            {entries
              .filter((entry) => !entry.config.source.shared_account_id)
              .map((entry) => (
                <option value={entry.resource_id} key={entry.resource_id}>
                  {entry.resource_name}
                </option>
              ))}
          </select>
          <small>
            {tx("仅共享明确属于同一钱包的余额；模型额度仍独立计算。")}
          </small>
        </label>
        <label>
          {tx("余额单位")}
          <input
            value={source.unit ?? ""}
            onChange={(event) => set({ unit: event.target.value })}
            placeholder="USD"
          />
        </label>
        <label>
          {tx("套餐字段路径")}
          <input
            value={source.plan_path ?? ""}
            onChange={(event) => set({ plan_path: event.target.value })}
            placeholder="subscription.name"
          />
        </label>
        <label>
          {tx("到期字段路径")}
          <input
            value={source.expires_path ?? ""}
            onChange={(event) => set({ expires_path: event.target.value })}
            placeholder="subscription.expires_at"
          />
        </label>
      </div>
      <fieldset>
        <legend>{tx("额度请求头")}</legend>
        <p>{tx("请求头加密保存；已保存的值不会再次显示。")}</p>
        {headers.map((row) => (
          <div className="allowance-inline" key={row.id}>
            <input
              aria-label={tx("请求头名称")}
              value={row.name}
              onChange={(event) =>
                onHeadersChange(
                  headers.map((item) =>
                    item.id === row.id
                      ? { ...item, name: event.target.value }
                      : item,
                  ),
                )
              }
            />
            <input
              type="password"
              aria-label={tx("请求头值")}
              value={row.value}
              onChange={(event) =>
                onHeadersChange(
                  headers.map((item) =>
                    item.id === row.id
                      ? { ...item, value: event.target.value }
                      : item,
                  ),
                )
              }
            />
            <button
              type="button"
              onClick={() =>
                onHeadersChange(headers.filter((item) => item.id !== row.id))
              }
            >
              {tx("删除")}
            </button>
          </div>
        ))}
        <button
          type="button"
          className="secondary"
          onClick={() =>
            onHeadersChange([
              ...headers,
              { id: crypto.randomUUID(), name: "", value: "" },
            ])
          }
        >
          {tx("添加请求头")}
        </button>
      </fieldset>
      <fieldset>
        <legend>{tx("结构化额度窗口")}</legend>
        <p>
          {tx(
            "可为每个窗口指定用量、上限、重置时间及适用模型。未返回的必需字段保持未知。",
          )}
        </p>
        {windows.map((window, index) => (
          <article className="allowance-rule" key={index}>
            <div className="allowance-form-grid">
              <label>
                {tx("窗口名称")}
                <input
                  required
                  value={window.name}
                  onChange={(event) =>
                    updateWindow(index, { name: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("已用比例表达式")}
                <input
                  value={window.used ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { used: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("已用金额表达式")}
                <input
                  value={window.amount ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { amount: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("上限表达式")}
                <input
                  value={window.limit ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { limit: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("余额表达式")}
                <input
                  value={window.balance ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { balance: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("余额单位")}
                <input
                  value={window.unit ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { unit: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("周期（秒）")}
                <input
                  type="number"
                  min={0}
                  value={window.period_seconds ?? ""}
                  onChange={(event) =>
                    updateWindow(index, {
                      period_seconds: Number(event.target.value),
                    })
                  }
                />
              </label>
              <label>
                {tx("重置字段路径")}
                <input
                  value={window.reset_path ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { reset_path: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("到期字段路径")}
                <input
                  value={window.expires_path ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { expires_path: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("共享额度池")}
                <input
                  value={window.pool ?? ""}
                  onChange={(event) =>
                    updateWindow(index, { pool: event.target.value })
                  }
                />
              </label>
              <label>
                {tx("适用模型（逗号分隔）")}
                <input
                  value={(window.models ?? []).join(",")}
                  onChange={(event) =>
                    updateWindow(index, {
                      models: event.target.value.split(","),
                    })
                  }
                />
              </label>
              <label>
                {tx("排除模型（逗号分隔）")}
                <input
                  value={(window.excluded_models ?? []).join(",")}
                  onChange={(event) =>
                    updateWindow(index, {
                      excluded_models: event.target.value.split(","),
                    })
                  }
                />
              </label>
            </div>
            <div className="allowance-checks">
              <label>
                <input
                  type="checkbox"
                  checked={window.optional ?? false}
                  onChange={(event) =>
                    updateWindow(index, { optional: event.target.checked })
                  }
                />
                {tx("可选窗口")}
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={window.informational ?? false}
                  onChange={(event) =>
                    updateWindow(index, { informational: event.target.checked })
                  }
                />
                {tx("仅供参考")}
              </label>
            </div>
            <button
              type="button"
              className="text-button danger"
              onClick={() =>
                set({ windows: windows.filter((_, i) => i !== index) })
              }
            >
              {tx("删除窗口")}
            </button>
          </article>
        ))}
        <button
          type="button"
          className="secondary"
          onClick={() => set({ windows: [...windows, { name: "" }] })}
        >
          {tx("添加窗口")}
        </button>
      </fieldset>
    </>
  );
}
