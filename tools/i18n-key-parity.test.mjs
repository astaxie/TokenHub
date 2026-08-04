// Key parity between the English and Japanese admin console dictionaries.
//
// tx() in frontend/features/admin/i18n/runtime.tsx falls back to `?? value`, and every
// source key is Chinese. A key that exists in the English dictionary but not the Japanese
// one therefore does not fail loudly: a Japanese user just sees the raw Chinese string.
// Fourteen keys sat that way until this gate landed, so the parity check lives in the
// tools test suite CI already runs rather than in a frontend test nobody wires up.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";

const i18nDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "frontend",
  "features",
  "admin",
  "i18n",
);

// The dictionary sources are standalone `.tsx` files with no JSX and no imports, so the
// type annotations below are the only thing between them and valid JavaScript. Evaluating
// them beats scraping with a regex: it handles escaped quotes, several pairs on one line,
// and routing.tsx's computed `[routingKeys.description]` keys for free, and it measures
// the same object tx() reads at runtime.
const TYPE_ANNOTATIONS = [
  ": Record<string, string>",
  ' satisfies Record<"en" | "ja", Record<string, string>>',
  " as const",
];

async function loadDictionarySource(file) {
  const source = readFileSync(join(i18nDir, file), "utf8");
  // A relative import would fail to resolve from a data: URL with a much less obvious
  // message than this one.
  assert.ok(
    !/^\s*import\s/m.test(source),
    `${file} now has an import; this loader only handles self-contained dictionaries.`,
  );
  let javascript = source;
  for (const annotation of TYPE_ANNOTATIONS) javascript = javascript.replaceAll(annotation, "");
  assert.ok(
    !javascript.includes("Record<"),
    `${file} uses a type annotation this loader does not strip.`,
  );
  return import(`data:text/javascript;base64,${Buffer.from(javascript).toString("base64")}`);
}

const { enTranslations } = await loadDictionarySource("en.tsx");
const { jaTranslations } = await loadDictionarySource("ja.tsx");
const { modelGovernanceTranslations } = await loadDictionarySource("model-governance.tsx");
const { routingTranslations } = await loadDictionarySource("routing.tsx");

// Mirrors the merge order in translations.tsx. It matters: a key defined in two sources
// resolves to the one merged last, so parity has to be checked on the merged result and
// not only file by file.
const merged = {
  en: { ...enTranslations, ...routingTranslations.en, ...modelGovernanceTranslations.en },
  ja: { ...jaTranslations, ...routingTranslations.ja, ...modelGovernanceTranslations.ja },
};

function keysMissingFrom(source, target) {
  return Object.keys(source).filter((key) => !Object.hasOwn(target, key));
}

function assertSameKeys(label, en, ja) {
  assert.deepEqual(keysMissingFrom(en, ja), [], `${label}: defined in en, missing from ja`);
  assert.deepEqual(keysMissingFrom(ja, en), [], `${label}: defined in ja, missing from en`);
}

describe("dictionary loading", () => {
  it("parses every dictionary into a non-trivial object", () => {
    // Without this, a transform that silently produced `{}` would make every parity
    // assertion below pass.
    const sources = {
      "en.tsx": enTranslations,
      "ja.tsx": jaTranslations,
      "model-governance.tsx en": modelGovernanceTranslations.en,
      "model-governance.tsx ja": modelGovernanceTranslations.ja,
      "routing.tsx en": routingTranslations.en,
      "routing.tsx ja": routingTranslations.ja,
    };
    for (const [label, dictionary] of Object.entries(sources)) {
      const count = Object.keys(dictionary).length;
      assert.ok(count > 50, `${label} parsed to ${count} keys, which is too few to be real`);
      for (const [key, value] of Object.entries(dictionary)) {
        assert.equal(typeof value, "string", `${label}: ${key} is not a string`);
        assert.notEqual(value.trim(), "", `${label}: ${key} has an empty translation`);
      }
    }
  });
});

describe("single ownership", () => {
  // Scope, stated honestly: this compares the evaluated objects, so it catches a key
  // defined in two different files. A key repeated twice inside one file collapses
  // during evaluation and is invisible here.
  const sourcesByLanguage = {
    en: [
      ["en.tsx", enTranslations],
      ["routing.tsx", routingTranslations.en],
      ["model-governance.tsx", modelGovernanceTranslations.en],
    ],
    ja: [
      ["ja.tsx", jaTranslations],
      ["routing.tsx", routingTranslations.ja],
      ["model-governance.tsx", modelGovernanceTranslations.ja],
    ],
  };

  for (const [language, sources] of Object.entries(sourcesByLanguage)) {
    it(`does not define a ${language} key in multiple sources`, () => {
      // A key defined twice silently resolves to whichever source translations.tsx
      // merges last, leaving the other definition as dead text that still reads like it
      // is in use. "示例" sat that way in en.tsx, shadowed by routing.tsx and disagreeing
      // with it ("Examples" against "Example"), which is the trap this catches.
      const owners = new Map();
      const shadowed = [];
      for (const [file, dictionary] of sources) {
        for (const key of Object.keys(dictionary)) {
          const owner = owners.get(key);
          if (owner) shadowed.push(`${key} (${owner} shadowed by ${file})`);
          else owners.set(key, file);
        }
      }
      assert.deepEqual(shadowed, [], `${language} keys defined in more than one source`);
    });
  }
});

describe("key parity", () => {
  it("keeps en.tsx and ja.tsx in step", () => {
    assertSameKeys("en.tsx vs ja.tsx", enTranslations, jaTranslations);
  });

  it("keeps the routing.tsx sections in step", () => {
    assertSameKeys("routing.tsx", routingTranslations.en, routingTranslations.ja);
  });

  it("keeps the model-governance.tsx sections in step", () => {
    assertSameKeys("model-governance.tsx", modelGovernanceTranslations.en, modelGovernanceTranslations.ja);
  });

  it("keeps the merged dictionary tx() reads in step", () => {
    assertSameKeys("merged translations", merged.en, merged.ja);
  });
});
