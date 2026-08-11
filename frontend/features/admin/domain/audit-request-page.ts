export type AuditRequestStatus = "all" | "ok" | "error";

export type AuditRequestPageParameters = {
  page: number;
  pageSize: number;
  status: AuditRequestStatus;
  query: string;
};

export function auditRequestPagePath(parameters: AuditRequestPageParameters) {
  const query = new URLSearchParams();
  query.set("page", String(parameters.page));
  query.set("page_size", String(parameters.pageSize));
  query.set("status", parameters.status);
  query.set("q", parameters.query.trim());
  return `/api/admin/audit/requests?${query.toString()}`;
}
