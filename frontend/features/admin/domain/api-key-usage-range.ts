export type APIKeyUsageRange = { from: string; to: string };

export function apiKeyUsageRangeForDays(days: number, now = new Date()): APIKeyUsageRange {
  const to = new Date(now);
  const from = new Date(Date.UTC(to.getUTCFullYear(), to.getUTCMonth(), to.getUTCDate() - Math.max(0, days - 1)));
  return { from: from.toISOString(), to: to.toISOString() };
}

export function apiKeyCustomUsageRange(fromDate: string, toDate: string, now = new Date()): APIKeyUsageRange | null {
  const from = new Date(`${fromDate}T00:00:00.000Z`);
  const inclusiveTo = new Date(`${toDate}T00:00:00.000Z`);
  if (Number.isNaN(from.getTime()) || Number.isNaN(inclusiveTo.getTime()) || from > inclusiveTo) return null;
  const requestedTo = new Date(inclusiveTo.getTime() + 24 * 60 * 60 * 1000);
  const to = requestedTo > now ? new Date(now) : requestedTo;
  if (from >= to || to.getTime() - from.getTime() > 366 * 24 * 60 * 60 * 1000) return null;
  return { from: from.toISOString(), to: to.toISOString() };
}

export function utcDateInputValue(value: string | Date) {
  const date = typeof value === "string" ? new Date(value) : value;
  return Number.isNaN(date.getTime()) ? "" : date.toISOString().slice(0, 10);
}
