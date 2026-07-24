import { type AdminResource, type ApiContext, type ApprovalRequest, type FieldConfig, type ResourceConfig, type ViewKey } from "../core/types";
import { fieldSummary, stringifyValue } from "../domain/entities";
import { formatTime } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminDelete, adminFetch, adminMutate, isAuthExpiredError, keyCreatePayload, readAdminError, resourcePayload } from "./payloads";
import { StatusPill } from "../shared/ui";

export function genericResourceConfig(kind: string, title: string, description: string, fields: FieldConfig[]): ResourceConfig<AdminResource> {
  return {
    view: kind as ViewKey,
    title,
    eyebrow: `${title}列表`,
    description,
    createLabel: `新增${title}`,
    columns: [
      { key: "name", label: "名称" },
      { key: "description", label: "说明" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "fields", label: "配置", render: (item) => fieldSummary(item.fields) },
      { key: "updated_at", label: "更新时间", render: (item) => formatTime(item.updated_at ?? "") },
    ],
    fields: [
      { key: "name", label: "名称", required: true },
      { key: "description", label: "说明", type: "textarea" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled", "draft", "archived"], required: true },
      ...fields,
    ],
    list: (ctx) => ctx.resources[kind] ?? [],
    create: (ctx, values) => adminMutate(ctx, `/api/admin/resources/${kind}`, "POST", resourcePayload(values, fields)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/${kind}/${item.id}`, "PATCH", resourcePayload(values, fields)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/resources/${kind}/${item.id}`),
    toForm: (item) => {
      const form: Record<string, string> = {
        name: item.name,
        description: item.description ?? "",
        status: item.status,
      };
      for (const field of fields) {
        form[field.key] = stringifyValue(item.fields?.[field.key]);
      }
      return form;
    },
  };
}

export async function runApprovalAction(ctx: ApiContext, item: ApprovalRequest, action: "approve" | "reject") {
  if (item.status !== "pending") return;
  const resp = await adminFetch(ctx, `/api/admin/approvals/${item.id}/${action}`, {
    method: "POST",
    body: JSON.stringify({}),
  });
  if (!resp.ok) throw new Error(`${action} approval ${resp.status}`);
  const payload = (await resp.json()) as { result?: { api_key?: string } };
  if (payload.result?.api_key) {
    window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: payload.result.api_key }));
  }
}

export async function createKeyWithCapture(
  ctx: ApiContext,
  values: Record<string, string>,
  setIssuedKey: (value: string) => void,
  setNotice: (value: string) => void,
  load: () => Promise<void>,
  setLoading: (value: boolean) => void,
  setError: (value: string) => void,
  closeForm: () => void,
) {
  setLoading(true);
  setError("");
  setNotice("");
  try {
    const payload = keyCreatePayload(values);
    const endpoint = values.project_id ? `/api/admin/projects/${values.project_id}/keys` : "/api/admin/api-keys";
    const resp = await adminFetch(ctx, endpoint, {
      method: "POST",
      body: JSON.stringify(payload),
    });
    if (!resp.ok) throw new Error(await readAdminError(resp, "项目 Key 发放"));
    const data = (await resp.json()) as { api_key?: string; approval_required?: boolean; approval?: ApprovalRequest };
    if (data.approval_required) {
      setIssuedKey("");
      setNotice(`已提交审批：${data.approval?.id ?? ""}`);
    } else if (data.api_key) {
      setNotice("");
      setIssuedKey(data.api_key);
    }
    closeForm();
    await load();
  } catch (err) {
    if (isAuthExpiredError(err)) return;
    setError(err instanceof Error ? err.message : tx("发放 Key 失败"));
  } finally {
    setLoading(false);
  }
}
