import type { APIKey } from "../core/types";

// Key 管理页面的搜索框只匹配 Key 名称，避免把 JSON 字段名（如 project_id）也当作搜索目标。
export function filterAPIKeys(items: APIKey[], query: string) {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return items;
  return items.filter((item) => item.name.toLowerCase().includes(normalized));
}
