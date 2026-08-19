import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  providerLatencyLabel,
  providerPerformanceExplanation,
  providerQualityScoreLabel,
} = await importTypeScript(new URL("./provider-monitoring.ts", import.meta.url));

test("provider monitoring copy states the observed latency and quality score meaning", () => {
  assert.equal(providerLatencyLabel, "网关实测延迟");
  assert.equal(providerQualityScoreLabel, "质量分（0-100）");
  assert.equal(providerPerformanceExplanation, "延迟优先取近24小时成功请求的中位总耗时；质量分综合可用率、延迟和资源健康。");
});
