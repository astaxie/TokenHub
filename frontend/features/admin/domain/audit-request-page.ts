export type AuditRequestStatus = "all" | "ok" | "error";
export type AuditRequestTimeRange = "all" | "15m" | "1h" | "24h";

export type AuditRequestPageParameters = {
  page: number;
  pageSize: number;
	status: AuditRequestStatus;
	query: string;
	model?: string;
	since?: string;
	until?: string;
};

export function auditRequestTimeRangeParameters(range: AuditRequestTimeRange, now: Date) {
	const durations: Record<Exclude<AuditRequestTimeRange, "all">, number> = {
		"15m": 15 * 60 * 1000,
		"1h": 60 * 60 * 1000,
		"24h": 24 * 60 * 60 * 1000,
	};
	if (range === "all") return { since: "", until: "" };
	return {
		since: new Date(now.getTime() - durations[range]).toISOString(),
		until: now.toISOString(),
	};
}

export function auditRequestPagePath(parameters: AuditRequestPageParameters) {
  const query = new URLSearchParams();
  query.set("page", String(parameters.page));
  query.set("page_size", String(parameters.pageSize));
	query.set("status", parameters.status);
	query.set("q", parameters.query.trim());
	if (parameters.model?.trim()) query.set("model", parameters.model.trim());
	if (parameters.since) query.set("since", parameters.since);
	if (parameters.until) query.set("until", parameters.until);
	return `/api/admin/audit/requests?${query.toString()}`;
}
