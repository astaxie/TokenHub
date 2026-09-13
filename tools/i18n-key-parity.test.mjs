// Key parity between the English, Japanese, and Russian admin console dictionaries.
//
// tx() in frontend/features/admin/i18n/runtime.tsx falls back to `?? value`, and every
// source key is Chinese. A key that exists in the English dictionary but not the Japanese
// or Russian one therefore does not fail loudly: a user just sees the raw Chinese string.
// The parity check lives in the tools test suite CI runs to guard all locales against regressions.

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
  ': Record<"en" | "ja" | "ru", Record<string, string>>',
  ': Record<"en" | "ja", Record<string, string>>',
  ": Record<string, string>",
  ' satisfies Record<"en" | "ja" | "ru", Record<string, string>>',
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

const { apiKeyAccessTranslations } = await loadDictionarySource("api-key-access.tsx");
const { adminUICopyTranslations } = await loadDictionarySource("admin-ui-copy.tsx");
const { billingStatementTranslations } = await loadDictionarySource("billing-statements.tsx");
const { billingPricingTranslations } = await loadDictionarySource("billing-pricing.tsx");
const { enTranslations } = await loadDictionarySource("en.tsx");
const { jaTranslations } = await loadDictionarySource("ja.tsx");
const { ruTranslations } = await loadDictionarySource("ru.tsx");
const { adminResourcesRuTranslations } = await loadDictionarySource("admin-resources-ru.tsx");
const { adminDomainRuTranslations } = await loadDictionarySource("admin-domain-ru.tsx");
const { adminWorkflowTranslations } = await loadDictionarySource("admin-workflows.tsx");
const { apiKeyUsageTranslations } = await loadDictionarySource("api-key-usage.tsx");
const { auditFilterTranslations } = await loadDictionarySource("audit-filters.tsx");
const { dbEvolutionTranslations } = await loadDictionarySource("db-evolution.tsx");
const { routingTranslations } = await loadDictionarySource("routing.tsx");
const { codexImageTranslations } = await loadDictionarySource("codex-image.tsx");
const { scopedRoutingPolicyTranslations } = await loadDictionarySource("scoped-routing-policy.tsx");
const { modelGovernanceTranslations } = await loadDictionarySource("model-governance.tsx");
const { gatewayDocsTranslations } = await loadDictionarySource("gateway-docs.tsx");
const { loginHomeTranslations } = await loadDictionarySource("login-home.tsx");
const { providerConnectionTranslations } = await loadDictionarySource("provider-connection.tsx");
const { providerMonitoringTranslations } = await loadDictionarySource("provider-monitoring.tsx");
const { usageTranslations } = await loadDictionarySource("usage.tsx");
const { playgroundTranslations } = await loadDictionarySource("playground.tsx");
const { pluginTranslations } = await loadDictionarySource("plugins.tsx");
const { securityTranslations } = await loadDictionarySource("security.tsx");
const { notificationTranslations } = await loadDictionarySource("notifications.tsx");
const { default: syntheticDNSTranslations } = await loadDictionarySource("synthetic-dns.tsx");

// Mirrors the full merge in translations.tsx across all feature dictionaries.
const merged = {
  en: {
    ...apiKeyAccessTranslations.en,
    ...adminUICopyTranslations.en,
    ...billingStatementTranslations.en,
    ...billingPricingTranslations.en,
    ...enTranslations,
    ...adminWorkflowTranslations.en,
    ...apiKeyUsageTranslations.en,
    ...auditFilterTranslations.en,
    ...dbEvolutionTranslations.en,
    ...routingTranslations.en,
    ...codexImageTranslations.en,
    ...scopedRoutingPolicyTranslations.en,
    ...modelGovernanceTranslations.en,
    ...gatewayDocsTranslations.en,
    ...loginHomeTranslations.en,
    ...providerConnectionTranslations.en,
    ...providerMonitoringTranslations.en,
    ...usageTranslations.en,
    ...playgroundTranslations.en,
    ...pluginTranslations.en,
    ...securityTranslations.en,
    ...notificationTranslations.en,
    ...syntheticDNSTranslations.en,
  },
  ja: {
    ...apiKeyAccessTranslations.ja,
    ...adminUICopyTranslations.ja,
    ...billingStatementTranslations.ja,
    ...billingPricingTranslations.ja,
    ...jaTranslations,
    ...adminWorkflowTranslations.ja,
    ...apiKeyUsageTranslations.ja,
    ...auditFilterTranslations.ja,
    ...dbEvolutionTranslations.ja,
    ...routingTranslations.ja,
    ...codexImageTranslations.ja,
    ...scopedRoutingPolicyTranslations.ja,
    ...modelGovernanceTranslations.ja,
    ...gatewayDocsTranslations.ja,
    ...loginHomeTranslations.ja,
    ...providerConnectionTranslations.ja,
    ...providerMonitoringTranslations.ja,
    ...usageTranslations.ja,
    ...playgroundTranslations.ja,
    ...pluginTranslations.ja,
    ...securityTranslations.ja,
    ...notificationTranslations.ja,
    ...syntheticDNSTranslations.ja,
  },
  ru: {
    ...apiKeyAccessTranslations.ru,
    ...adminUICopyTranslations.ru,
    ...ruTranslations,
    ...adminResourcesRuTranslations,
    ...adminDomainRuTranslations,
    ...modelGovernanceTranslations.ru,
  },
};

function keysMissingFrom(source, target) {
  return Object.keys(source).filter((key) => !Object.hasOwn(target, key));
}

function assertSameKeys(label, en, ja) {
  assert.deepEqual(keysMissingFrom(en, ja), [], `${label}: defined in en, missing from ja`);
  assert.deepEqual(keysMissingFrom(ja, en), [], `${label}: defined in ja, missing from en`);
}

function assertSameThreeWayKeys(label, en, ja, ru) {
  assert.deepEqual(keysMissingFrom(en, ja), [], `${label}: defined in en, missing from ja`);
  assert.deepEqual(keysMissingFrom(ja, en), [], `${label}: defined in ja, missing from en`);
  assert.deepEqual(keysMissingFrom(en, ru), [], `${label}: defined in en, missing from ru`);
  assert.deepEqual(keysMissingFrom(ru, en), [], `${label}: defined in ru, missing from en`);
}

describe("dictionary loading", () => {
  it("parses every dictionary into a non-trivial object", () => {
    // Without this, a transform that silently produced `{}` would make every parity
    // assertion below pass.
    const sources = {
      "en.tsx": enTranslations,
      "ja.tsx": jaTranslations,
      "ru.tsx": ruTranslations,
      "admin-domain-ru.tsx": adminDomainRuTranslations,
      "admin-resources-ru.tsx": adminResourcesRuTranslations,
      "model-governance.tsx en": modelGovernanceTranslations.en,
      "model-governance.tsx ja": modelGovernanceTranslations.ja,
      "model-governance.tsx ru": modelGovernanceTranslations.ru,
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
    ru: [
      ["ru.tsx", ruTranslations],
      ["admin-domain-ru.tsx", adminDomainRuTranslations],
      ["admin-resources-ru.tsx", adminResourcesRuTranslations],
      ["model-governance.tsx", modelGovernanceTranslations.ru],
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
    assertSameThreeWayKeys(
      "model-governance.tsx",
      modelGovernanceTranslations.en,
      modelGovernanceTranslations.ja,
      modelGovernanceTranslations.ru,
    );
  });

  it("keeps the merged dictionary tx() reads in step", () => {
    assertSameThreeWayKeys("merged translations", merged.en, merged.ja, merged.ru);
  });
});
