import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  literalPropertyKeys,
  literalTxKeys,
  missingKeys,
  newLiteralTxKeys,
} from "./ui-translations.mjs";

const i18nRoot = fileURLToPath(new URL("../frontend/features/admin/i18n", import.meta.url));
const playgroundSource = await import("node:fs/promises").then(({ readFile }) => readFile(
  new URL("../frontend/features/admin/views/playground.tsx", import.meta.url),
  "utf8",
));

test("extracts only literal translation calls and object keys", () => {
  assert.deepEqual(literalTxKeys('tx("你好"); tx(dynamic); tx("带\\\"引号")'), ["你好", '带"引号']);
  assert.deepEqual(literalPropertyKeys('{ "你好": "Hello", [dynamic]: "ignored" }'), ["你好"]);
  assert.deepEqual(newLiteralTxKeys('tx("已有")', 'tx("已有"); tx("新增"); tx("新增")'), ["新增"]);
});

for (const locale of ["en", "ja"]) {
  test(`the Playground has complete ${locale} translations`, () => {
    const missing = missingKeys(literalTxKeys(playgroundSource), i18nRoot, locale);
    assert.deepEqual(missing, [], missing.map((key) => `Missing ${locale} translation for ${JSON.stringify(key)}`).join("\n"));
  });
}
