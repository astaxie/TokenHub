import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { magpieCatalogEntry, magpieCommit, magpieProviderPresets } from "./magpie-provider-presets.mjs";

const catalog = JSON.parse(await readFile(new URL("../data/provider-catalog.json", import.meta.url), "utf8"));

test("pinned Magpie account and plan presets match the tracked catalog and packages", async () => {
  for (const preset of magpieProviderPresets) {
    const entry = catalog.providers[preset.id];
    assert.deepEqual(entry, magpieCatalogEntry(preset), preset.id);
    const packaged = JSON.parse(await readFile(new URL(`../data/builtin-plugins/providers/${preset.id}/catalog.json`, import.meta.url), "utf8"));
    assert.deepEqual(packaged, entry, preset.id);
    const notice = await readFile(new URL(`../data/builtin-plugins/providers/${preset.id}/THIRD_PARTY_NOTICES`, import.meta.url), "utf8");
    assert.match(notice, /MIT License/);
    assert.ok(notice.includes(magpieCommit));
    assert.equal(entry.metadata.upstream_commit, magpieCommit);
    for (const model of entry.models) {
      assert.equal(model.cost, undefined, `${preset.id}/${model.id}: unknown price must not be made free`);
      assert.equal(model.limit, undefined, `${preset.id}/${model.id}: no inferred context limit`);
      assert.equal(model.metadata.pricing_status, "unverified");
    }
  }
  assert.match(await readFile(new URL("./licenses/magpie-LICENSE", import.meta.url), "utf8"), /Copyright \(c\) 2026 yetone/);
});

test("plan paths, regions and service identities remain distinct", () => {
  const p = catalog.providers;
  assert.equal(p["baidu-qianfan-personal"].api, "https://qianfan.baidubce.com/v2/tokenplan/personal");
  assert.equal(p["baidu-qianfan-team"].api, "https://qianfan.baidubce.com/v2/tokenplan/team");
  assert.equal(p["huawei-maas-token-plan"].api, "https://api.modelarts-maas.com/plan/v2");
  assert.equal(p["volcengine-coding-plan"].api, "https://ark.cn-beijing.volces.com/api/coding/v3");
  assert.equal(p["volcengine-agent-plan"].api, "https://ark.cn-beijing.volces.com/api/plan/v3");
  assert.notEqual(new URL(p["qwen-token-plan"].api).hostname, new URL(p["alibaba-token-plan-cn"].api).hostname);
  assert.equal(p["kimi-code-global"].api, "https://api.kimi.ai/coding/v1");
  assert.equal(p["stepfun-ai"].api, "https://api.stepfun.ai/step_plan/v1");
  assert.equal(p["tencent-token-plan-enterprise-intl"].api, "https://tokenhub-intl.tencentmaas.com/plan/v3");
  assert.ok(p["baidu-qianfan-team"].models.some(model => model.id === "deepseek-v3.2"));
  assert.ok(!p["baidu-qianfan-personal"].models.some(model => model.id === "deepseek-v3.2"));
});

test("account model presets do not copy unrelated public or local inventories", () => {
  for (const id of ["kimi-code-global", "ollama-cloud", "commandcode", "nous"]) {
    assert.deepEqual(catalog.providers[id].models, [], id);
    assert.equal(catalog.providers[id].metadata.model_discovery, "account", id);
  }
  assert.ok(catalog.providers.ollama.models.length > 0, "legacy library remains intact");
});
