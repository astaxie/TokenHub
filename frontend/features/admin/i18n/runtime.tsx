import { languageStorageKey } from "../core/types";
import { type AppLanguage, languageFromLocales, languageOptions, preferredLanguage } from "./language-preference";
import { translations } from "./translations";

export { type AppLanguage, languageFromLocales, languageOptions, preferredLanguage };

export let activeLanguage: AppLanguage = "en";

export function readSavedLanguage(): AppLanguage {
  if (typeof window === "undefined") return "en";
  const saved = window.localStorage.getItem(languageStorageKey);
  return preferredLanguage(saved, navigator.languages?.length ? navigator.languages : [navigator.language]);
}

export function setActiveLanguage(language: AppLanguage) {
  activeLanguage = language;
}

export function tx(value: string | undefined | null) {
  if (!value) return "";
  if (activeLanguage === "zh-CN") return value;
  return translations[activeLanguage][value] ?? translateGeneratedText(value, activeLanguage) ?? value;
}

export function formatTranslationTemplate(template: string, values: Record<string, string>) {
  return Object.entries(values).reduce((message, [key, value]) => message.split(`{${key}}`).join(value), template);
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
    if (language === "ja") return `${base}一覧`;
    if (language === "ru") return `Список: ${base}`;
    return `${base} List`;
  }
  const createMatch = value.match(/^新增(.+)$/);
  if (createMatch) {
    const base = translations[language][createMatch[1]] ?? createMatch[1];
    if (language === "ja") return `${base}を作成`;
    if (language === "ru") return `Создать ${base}`;
    return `Create ${base}`;
  }
  const approvalMatch = value.match(/^已提交审批：(.+)$/);
  if (approvalMatch) {
    if (language === "ja") return `承認申請済み: ${approvalMatch[1]}`;
    if (language === "ru") return `Заявка на согласование отправлена: ${approvalMatch[1]}`;
    return `Approval submitted: ${approvalMatch[1]}`;
  }
  const exportMatch = value.match(/^(.+) 已导出$/);
  if (exportMatch) {
    if (language === "ja") return `${exportMatch[1]} をエクスポートしました`;
    if (language === "ru") return `${exportMatch[1]} экспортировано`;
    return `${exportMatch[1]} exported`;
  }
  const sentMatch = value.match(/^(.+) 已发送$/);
  if (sentMatch) {
    if (language === "ja") return `${sentMatch[1]} を送信しました`;
    if (language === "ru") return `${sentMatch[1]} отправлено`;
    return `${sentMatch[1]} sent`;
  }
  const approvedMatch = value.match(/^(.+) 已批准$/);
  if (approvedMatch) {
    if (language === "ja") return `${approvedMatch[1]} を承認しました`;
    if (language === "ru") return `${approvedMatch[1]} утверждено`;
    return `${approvedMatch[1]} approved`;
  }
  const rejectedMatch = value.match(/^(.+) 已驳回$/);
  if (rejectedMatch) {
    if (language === "ja") return `${rejectedMatch[1]} を却下しました`;
    if (language === "ru") return `${rejectedMatch[1]} отклонено`;
    return `${rejectedMatch[1]} rejected`;
  }
  const confirmedMatch = value.match(/^(.+) 已确认$/);
  if (confirmedMatch) {
    if (language === "ja") return `${confirmedMatch[1]} を確認しました`;
    if (language === "ru") return `${confirmedMatch[1]} подтверждено`;
    return `${confirmedMatch[1]} confirmed`;
  }
  const quotaSubmittedMatch = value.match(/^(.+) 的额度提升申请已提交$/);
  if (quotaSubmittedMatch) {
    if (language === "ja") return `${quotaSubmittedMatch[1]} のクォータ増額申請を送信しました`;
    if (language === "ru") return `Запрос на увеличение квоты отправлен для ${quotaSubmittedMatch[1]}`;
    return `${quotaSubmittedMatch[1]} quota increase request submitted`;
  }
  const quotaSavedMatch = value.match(/^(.+) 的额度已保存$/);
  if (quotaSavedMatch) {
    if (language === "ja") return `${quotaSavedMatch[1]} のクォータを保存しました`;
    if (language === "ru") return `Квота сохранена для ${quotaSavedMatch[1]}`;
    return `${quotaSavedMatch[1]} quota saved`;
  }
  const teamLinkedMatch = value.match(/^(.+) 已关联团队$/);
  if (teamLinkedMatch) {
    if (language === "ja") return `${teamLinkedMatch[1]} にチームを関連付けました`;
    if (language === "ru") return `Команда привязана к ${teamLinkedMatch[1]}`;
    return `Team linked to ${teamLinkedMatch[1]}`;
  }
  const teamRoleMatch = value.match(/^(.+) 权限已更新$/);
  if (teamRoleMatch) {
    if (language === "ja") return `${teamRoleMatch[1]} の権限を更新しました`;
    if (language === "ru") return `Права обновлены для ${teamRoleMatch[1]}`;
    return `${teamRoleMatch[1]} permissions updated`;
  }
  const teamRemovedMatch = value.match(/^(.+) 已移除$/);
  if (teamRemovedMatch) {
    if (language === "ja") return `${teamRemovedMatch[1]} を削除しました`;
    if (language === "ru") return `${teamRemovedMatch[1]} удалено`;
    return `${teamRemovedMatch[1]} removed`;
  }
  const statusMatch = value.match(/^(.+) 已(启用|禁用|轮换，新 Key 已展示)$/);
  if (statusMatch) {
    const action = statusMatch[2];
    if (language === "ja") {
      const label = action === "启用" ? "有効化しました" : action === "禁用" ? "無効化しました" : "ローテーションしました。新しい Key を表示しています";
      return `${statusMatch[1]} を${label}`;
    }
    if (language === "ru") {
      const label = action === "启用" ? "включено" : action === "禁用" ? "отключено" : "обновлено, новый Key отображён";
      return `${statusMatch[1]}: ${label}`;
    }
    const label = action === "启用" ? "enabled" : action === "禁用" ? "disabled" : "rotated; new Key is displayed";
    return `${statusMatch[1]} ${label}`;
  }
  const routeOrderMatch = value.match(/^已更新 (.+) 的 Provider 调用顺序$/);
  if (routeOrderMatch) {
    if (language === "ja") return `${routeOrderMatch[1]} の Provider 呼び出し順を更新しました`;
    if (language === "ru") return `Порядок вызова Provider обновлён для ${routeOrderMatch[1]}`;
    return `Updated Provider call order for ${routeOrderMatch[1]}`;
  }
  const routePolicyMatch = value.match(/^已应用 (.+) 的模型路由策略$/);
  if (routePolicyMatch) {
    if (language === "ja") return `${routePolicyMatch[1]} のモデルルーティング戦略を適用しました`;
    if (language === "ru") return `Применена стратегия маршрутизации модели для ${routePolicyMatch[1]}`;
    return `Applied the model routing strategy for ${routePolicyMatch[1]}`;
  }
  const enabledRoutesMatch = value.match(/^(\d+)\/(\d+) 启用 · (.+)$/);
  if (enabledRoutesMatch) {
    if (language === "ja") return `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} 有効 · ${enabledRoutesMatch[3]}`;
    if (language === "ru") return `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} включено · ${enabledRoutesMatch[3]}`;
    return `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} enabled · ${enabledRoutesMatch[3]}`;
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
  if (activeLanguage === "ru") return "ru-RU";
  return "zh-CN";
}

export function formatLocaleNumber(value: number) {
  return new Intl.NumberFormat(languageLocale()).format(value);
}

export function countWithUnit(count: number, zhUnit: string, enUnit: string, jaUnit: string, enPluralUnit = `${enUnit}s`) {
  const formatted = formatLocaleNumber(count);
  if (activeLanguage === "en") return `${formatted} ${count === 1 ? enUnit : enPluralUnit}`;
  if (activeLanguage === "ja") return `${formatted} ${jaUnit}`;
  if (activeLanguage === "ru") {
    const ruUnit = tx(zhUnit);
    return `${formatted} ${ruUnit !== zhUnit ? ruUnit : (count === 1 ? enUnit : enPluralUnit)}`;
  }
  return `${formatted} ${zhUnit}`;
}

export function countRatioWithUnit(current: number, total: number, zhUnit: string, enUnit: string, jaUnit: string, enPluralUnit = `${enUnit}s`) {
  const ratio = `${formatLocaleNumber(current)}/${formatLocaleNumber(total)}`;
  if (activeLanguage === "en") return `${ratio} ${current === 1 ? enUnit : enPluralUnit}`;
  if (activeLanguage === "ja") return `${ratio} ${jaUnit}`;
  if (activeLanguage === "ru") {
    const ruUnit = tx(zhUnit);
    return `${ratio} ${ruUnit !== zhUnit ? ruUnit : (current === 1 ? enUnit : enPluralUnit)}`;
  }
  return `${ratio} ${zhUnit}`;
}

export function guardrailDetectionItemName(index: number) {
  const formatted = formatLocaleNumber(index);
  if (activeLanguage === "en") return `Detection item ${formatted}`;
  if (activeLanguage === "ja") return `検出項目 ${formatted}`;
  if (activeLanguage === "ru") return `Элемент проверки ${formatted}`;
  return `检测项 ${formatted}`;
}

export function millisecondsText(value: number) {
  const formatted = formatLocaleNumber(value);
  if (activeLanguage === "en") return `${formatted} ms`;
  if (activeLanguage === "ja") return `${formatted} ミリ秒`;
  if (activeLanguage === "ru") return `${formatted} мс`;
  return `${formatted} 毫秒`;
}

export function guardrailBlockedDiagnostic(reasonLabels: string[], policyLabels: string[], requestID: string) {
  const joinLabels = (labels: string[]) => labels.join(activeLanguage === "en" || activeLanguage === "ru" ? ", " : "、");
  if (activeLanguage === "en") {
    const details = [
      reasonLabels.length > 0 ? `Reasons: ${joinLabels(reasonLabels)}` : "",
      policyLabels.length > 0 ? `Matched policies: ${joinLabels(policyLabels)}` : "",
      requestID ? `Request ID: ${requestID}` : "",
    ].filter(Boolean);
    return ["The request was blocked by a content security policy.", details.join("; ")].filter(Boolean).join(" ");
  }
  if (activeLanguage === "ja") {
    const details = [
      reasonLabels.length > 0 ? `理由：${joinLabels(reasonLabels)}` : "",
      policyLabels.length > 0 ? `一致したポリシー：${joinLabels(policyLabels)}` : "",
      requestID ? `リクエスト ID：${requestID}` : "",
    ].filter(Boolean);
    return ["コンテンツセキュリティポリシーによりリクエストがブロックされました。", details.join("；")].filter(Boolean).join(" ");
  }
  if (activeLanguage === "ru") {
    const details = [
      reasonLabels.length > 0 ? `Причина: ${joinLabels(reasonLabels)}` : "",
      policyLabels.length > 0 ? `Сработавшая политика: ${joinLabels(policyLabels)}` : "",
      requestID ? `ID запроса: ${requestID}` : "",
    ].filter(Boolean);
    return ["Запрос заблокирован политикой безопасности контента.", details.join("; ")].filter(Boolean).join(" ");
  }
  const details = [
    reasonLabels.length > 0 ? `原因：${joinLabels(reasonLabels)}` : "",
    policyLabels.length > 0 ? `命中策略：${joinLabels(policyLabels)}` : "",
    requestID ? `请求 ID：${requestID}` : "",
  ].filter(Boolean);
  return ["请求已被内容安全策略阻断。", details.join("；")].filter(Boolean).join(" ");
}

export function providerSaveMessage(updated: boolean, accountResourceCreated: boolean, imported: number, categoryLabel: string) {
  const modelCount = imported > 0
    ? countWithUnit(imported, `个${categoryLabel}上游模型`, `${categoryLabel} upstream model`, `${categoryLabel} 上流モデル`)
    : "";
  if (activeLanguage === "en") {
    return [
      `Provider ${updated ? "updated" : "created"}`,
      accountResourceCreated ? "account resource created" : "",
      modelCount ? `${modelCount} imported` : "",
    ].filter(Boolean).join(", ");
  }
  if (activeLanguage === "ja") {
    return [
      `Provider を${updated ? "更新" : "作成"}しました`,
      accountResourceCreated ? "アカウントリソースを作成しました" : "",
      modelCount ? `${modelCount}を取り込みました` : "",
    ].filter(Boolean).join("、");
  }
  if (activeLanguage === "ru") {
    return [
      `Провайдер ${updated ? "обновлён" : "создан"}`,
      accountResourceCreated ? "ресурс аккаунта создан" : "",
      modelCount ? `${modelCount} импортировано` : "",
    ].filter(Boolean).join(", ");
  }
  return [
    `Provider 已${updated ? "更新" : "新增"}`,
    accountResourceCreated ? "已创建账号资源" : "",
    modelCount ? `引入 ${modelCount}` : "",
  ].filter(Boolean).join("，");
}

export function countWithLabel(count: number, label: string) {
  if (activeLanguage !== "zh-CN") return `${formatLocaleNumber(count)} ${tx(label)}`;
  return `${formatLocaleNumber(count)} ${label}`;
}

export function selectedModelsText(count: number) {
  if (activeLanguage === "en") return `${count} models selected`;
  if (activeLanguage === "ja") return `${count} 件のモデルを選択済み`;
  if (activeLanguage === "ru") return `Выбрано моделей: ${count}`;
  return `已选择 ${count} 个模型`;
}

export function selectedOptionsText(count: number) {
  if (activeLanguage === "en") return `${count} options selected`;
  if (activeLanguage === "ja") return `${count} 件の項目を選択済み`;
  if (activeLanguage === "ru") return `Выбрано вариантов: ${count}`;
  return `已选择 ${count} 个选项`;
}

export function defaultPlaygroundSystemPrompt() {
  return tx("做一个乐于助人的助手");
}

export function isDefaultPlaygroundSystemPrompt(value: string) {
  return [
    "做一个乐于助人的助手",
    translations.en["做一个乐于助人的助手"],
    translations.ja["做一个乐于助人的助手"],
    translations.ru?.["做一个乐于助人的助手"],
  ].filter(Boolean).includes(value);
}

export function importUsersDoneMessage(created: number, updated: number, skipped: number) {
  if (activeLanguage === "en") {
    return `User import complete: ${created} created, ${updated} updated${skipped > 0 ? `, ${skipped} skipped` : ""}`;
  }
  if (activeLanguage === "ja") {
    return `ユーザーインポート完了: 作成 ${created}、更新 ${updated}${skipped > 0 ? `、スキップ ${skipped}` : ""}`;
  }
  if (activeLanguage === "ru") {
    return `Импорт пользователей завершен: создано ${created}, обновлено ${updated}${skipped > 0 ? `, пропущено ${skipped}` : ""}`;
  }
  return `用户导入完成：新增 ${created}，更新 ${updated}${skipped > 0 ? `，跳过 ${skipped}` : ""}`;
}

export function importUsersSkippedMessage(skipped: number, errors: string) {
  if (activeLanguage === "en") return `${skipped} rows were not imported: ${errors}`;
  if (activeLanguage === "ja") return `${skipped} 件はインポートされませんでした: ${errors}`;
  if (activeLanguage === "ru") return `Не импортировано строк: ${skipped}. Ошибки: ${errors}`;
  return `有 ${skipped} 条未导入：${errors}`;
}

export function deleteConfirmMessage(name: string) {
  if (activeLanguage === "en") return `After deleting "${name}", the current in-memory data will be removed immediately.`;
  if (activeLanguage === "ja") return `「${name}」を削除すると、現在のメモリ上のデータはすぐに削除されます。`;
  if (activeLanguage === "ru") return `После удаления «${name}» данные в оперативной памяти будут немедленно удалены.`;
  return `删除「${name}」后，当前内存数据会立即移除。`;
}

export function bulkDeleteConfirmMessage(count: number) {
  if (activeLanguage === "en") return `After deleting ${formatLocaleNumber(count)} selected records, the current in-memory data will be removed immediately.`;
  if (activeLanguage === "ja") return `選択した ${formatLocaleNumber(count)} 件を削除すると、現在のメモリ上のデータはすぐに削除されます。`;
  if (activeLanguage === "ru") return `После удаления выбранных записей (${formatLocaleNumber(count)}) данные в оперативной памяти будут немедленно удалены.`;
  return `删除选中的 ${formatLocaleNumber(count)} 条记录后，当前内存数据会立即移除。`;
}

function russianPlural(count: number, one: string, few: string, many: string) {
  const n = Math.abs(count) % 100;
  const last = n % 10;
  if (n >= 11 && n <= 14) return many;
  if (last === 1) return one;
  if (last >= 2 && last <= 4) return few;
  return many;
}

export function routeAttemptCountText(count: number) {
  if (activeLanguage === "ru") {
    return `${formatLocaleNumber(count)} ${russianPlural(count, "попытка", "попытки", "попыток")}, с fallback`;
  }
  if (count > 1) {
    if (activeLanguage === "en") return `${formatLocaleNumber(count)} attempts, with fallback`;
    if (activeLanguage === "ja") return `${formatLocaleNumber(count)} 回、fallback 含む`;
    return `${formatLocaleNumber(count)} 次，含 fallback`;
  }
  return countWithUnit(count, "次", "attempt", "回");
}
