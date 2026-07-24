import { languageStorageKey } from "../core/types";
import { formatNumber } from "../domain/formatting";
import { translations } from "./translations";

export type AppLanguage = "zh-CN" | "en" | "ja";

export const languageOptions: Array<{ value: AppLanguage; label: string; nativeLabel: string }> = [
  { value: "zh-CN", label: "Chinese", nativeLabel: "简体中文" },
  { value: "en", label: "English", nativeLabel: "English" },
  { value: "ja", label: "Japanese", nativeLabel: "日本語" },
];

export let activeLanguage: AppLanguage = "en";

export function readSavedLanguage(): AppLanguage {
  if (typeof window === "undefined") return "en";
  const saved = window.localStorage.getItem(languageStorageKey);
  return saved === "en" || saved === "ja" || saved === "zh-CN" ? saved : "en";
}

export function setActiveLanguage(language: AppLanguage) {
  activeLanguage = language;
}

export function tx(value: string | undefined | null) {
  if (!value) return "";
  if (activeLanguage === "zh-CN") return value;
  return translations[activeLanguage][value] ?? translateGeneratedText(value, activeLanguage) ?? value;
}

// Localize the browser's native constraint-validation bubble (e.g. the required-field
// message) so it follows the app language instead of the browser locale.
export function handleRequiredFieldInvalid(event: {
  currentTarget: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
}) {
  const el = event.currentTarget;
  el.setCustomValidity(el.validity.valueMissing ? tx("请填写此字段") : "");
}

// Clear any custom validity message once the user edits the field, so re-validation works.
export function clearCustomValidity(event: {
  currentTarget: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
}) {
  event.currentTarget.setCustomValidity("");
}

export function translateGeneratedText(value: string, language: Exclude<AppLanguage, "zh-CN">) {
  const createListMatch = value.match(/^(.+)列表$/);
  if (createListMatch) {
    const base = translations[language][createListMatch[1]] ?? createListMatch[1];
    return language === "ja" ? `${base}一覧` : `${base} List`;
  }
  const createMatch = value.match(/^新增(.+)$/);
  if (createMatch) {
    const base = translations[language][createMatch[1]] ?? createMatch[1];
    return language === "ja" ? `${base}を作成` : `Create ${base}`;
  }
  const approvalMatch = value.match(/^已提交审批：(.+)$/);
  if (approvalMatch) return language === "ja" ? `承認申請済み: ${approvalMatch[1]}` : `Approval submitted: ${approvalMatch[1]}`;
  const exportMatch = value.match(/^(.+) 已导出$/);
  if (exportMatch) return language === "ja" ? `${exportMatch[1]} をエクスポートしました` : `${exportMatch[1]} exported`;
  const sentMatch = value.match(/^(.+) 已发送$/);
  if (sentMatch) return language === "ja" ? `${sentMatch[1]} を送信しました` : `${sentMatch[1]} sent`;
  const approvedMatch = value.match(/^(.+) 已批准$/);
  if (approvedMatch) return language === "ja" ? `${approvedMatch[1]} を承認しました` : `${approvedMatch[1]} approved`;
  const rejectedMatch = value.match(/^(.+) 已驳回$/);
  if (rejectedMatch) return language === "ja" ? `${rejectedMatch[1]} を却下しました` : `${rejectedMatch[1]} rejected`;
  const confirmedMatch = value.match(/^(.+) 已确认$/);
  if (confirmedMatch) return language === "ja" ? `${confirmedMatch[1]} を確認しました` : `${confirmedMatch[1]} confirmed`;
  const quotaSubmittedMatch = value.match(/^(.+) 的额度提升申请已提交$/);
  if (quotaSubmittedMatch) return language === "ja" ? `${quotaSubmittedMatch[1]} のクォータ増額申請を送信しました` : `${quotaSubmittedMatch[1]} quota increase request submitted`;
  const quotaSavedMatch = value.match(/^(.+) 的额度已保存$/);
  if (quotaSavedMatch) return language === "ja" ? `${quotaSavedMatch[1]} のクォータを保存しました` : `${quotaSavedMatch[1]} quota saved`;
  const statusMatch = value.match(/^(.+) 已(启用|禁用|轮换，新 Key 已展示)$/);
  if (statusMatch) {
    const action = statusMatch[2];
    if (language === "ja") {
      const label = action === "启用" ? "有効化しました" : action === "禁用" ? "無効化しました" : "ローテーションしました。新しい Key を表示しています";
      return `${statusMatch[1]} を${label}`;
    }
    const label = action === "启用" ? "enabled" : action === "禁用" ? "disabled" : "rotated; new Key is displayed";
    return `${statusMatch[1]} ${label}`;
  }
  const routeOrderMatch = value.match(/^已更新 (.+) 的 Provider 调用顺序$/);
  if (routeOrderMatch) return language === "ja" ? `${routeOrderMatch[1]} の Provider 呼び出し順を更新しました` : `Updated Provider call order for ${routeOrderMatch[1]}`;
  const enabledRoutesMatch = value.match(/^(\d+)\/(\d+) 启用 · (.+)$/);
  if (enabledRoutesMatch) {
    return language === "ja"
      ? `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} 有効 · ${enabledRoutesMatch[3]}`
      : `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} enabled · ${enabledRoutesMatch[3]}`;
  }
  return undefined;
}

export function displayText(value: string | undefined | null) {
  return tx(value);
}

export function isIssuedAPIKey(value: string) {
  return /^[A-Za-z][A-Za-z0-9_-]{0,23}_[A-Za-z0-9_-]{24,}$/.test(value.trim());
}

export function translatedCell(value: React.ReactNode) {
  return typeof value === "string" ? tx(value) : value;
}

export function languageLocale() {
  if (activeLanguage === "en") return "en-US";
  if (activeLanguage === "ja") return "ja-JP";
  return "zh-CN";
}

export function countWithUnit(count: number, zhUnit: string, enUnit: string, jaUnit: string) {
  const formatted = formatNumber(count);
  if (activeLanguage === "en") return `${formatted} ${enUnit}${count === 1 ? "" : "s"}`;
  if (activeLanguage === "ja") return `${formatted} ${jaUnit}`;
  return `${formatted} ${zhUnit}`;
}

export function countWithLabel(count: number, label: string) {
  if (activeLanguage === "en") return `${formatNumber(count)} ${tx(label)}`;
  if (activeLanguage === "ja") return `${formatNumber(count)} ${tx(label)}`;
  return `${formatNumber(count)} ${label}`;
}

export function selectedModelsText(count: number) {
  if (activeLanguage === "en") return `${count} models selected`;
  if (activeLanguage === "ja") return `${count} 件のモデルを選択済み`;
  return `已选择 ${count} 个模型`;
}

export function defaultPlaygroundSystemPrompt() {
  return tx("做一个乐于助人的助手");
}

export function isDefaultPlaygroundSystemPrompt(value: string) {
  return [
    "做一个乐于助人的助手",
    translations.en["做一个乐于助人的助手"],
    translations.ja["做一个乐于助人的助手"],
  ].includes(value);
}

export function importUsersDoneMessage(created: number, updated: number, skipped: number) {
  if (activeLanguage === "en") {
    return `User import complete: ${created} created, ${updated} updated${skipped > 0 ? `, ${skipped} skipped` : ""}`;
  }
  if (activeLanguage === "ja") {
    return `ユーザーインポート完了: 作成 ${created}、更新 ${updated}${skipped > 0 ? `、スキップ ${skipped}` : ""}`;
  }
  return `用户导入完成：新增 ${created}，更新 ${updated}${skipped > 0 ? `，跳过 ${skipped}` : ""}`;
}

export function importUsersSkippedMessage(skipped: number, errors: string) {
  if (activeLanguage === "en") return `${skipped} rows were not imported: ${errors}`;
  if (activeLanguage === "ja") return `${skipped} 件はインポートされませんでした: ${errors}`;
  return `有 ${skipped} 条未导入：${errors}`;
}

export function deleteConfirmMessage(name: string) {
  if (activeLanguage === "en") return `After deleting "${name}", the current in-memory data will be removed immediately.`;
  if (activeLanguage === "ja") return `「${name}」を削除すると、現在のメモリ上のデータはすぐに削除されます。`;
  return `删除「${name}」后，当前内存数据会立即移除。`;
}

export function routeAttemptCountText(count: number) {
  if (count > 1) {
    if (activeLanguage === "en") return `${formatNumber(count)} attempts, with fallback`;
    if (activeLanguage === "ja") return `${formatNumber(count)} 回、fallback 含む`;
    return `${formatNumber(count)} 次，含 fallback`;
  }
  return countWithUnit(count, "次", "attempt", "回");
}
