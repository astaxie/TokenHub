import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { literalTxKeys, missingKeys } from "./ui-translations.mjs";

const adminRoot = new URL("../frontend/features/admin/", import.meta.url);
const i18nRoot = fileURLToPath(new URL("i18n/", adminRoot));
const read = (path) => readFileSync(new URL(path, adminRoot), "utf8");
const views = {
  "overview": ["企业级 AI 网关"],
  "crud-projects": ["上游可用性"],
  "usage-billing": ["个人用量", "管理层用量报告", "账单连接器", "产品代码"],
  "security-policies": ["不安全", "有争议或不安全"],
};

test("admin page headings and safety option labels use translated copy", () => {
  for (const [view, required] of Object.entries(views)) {
    const keys = literalTxKeys(read(`views/${view}.tsx`));
    for (const key of required) assert.ok(keys.includes(key), `${view}: ${key}`);
  }
});

for (const locale of ["en", "ja"]) {
  test(`built-in Admin UI and page copy have complete ${locale} translations`, () => {
    const keys = [...literalTxKeys(read("i18n/builtin-admin-ui.tsx")), ...Object.values(views).flat()];
    assert.deepEqual(missingKeys(keys, i18nRoot, locale), []);
  });
}
