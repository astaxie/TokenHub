import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  providerReasoningOverrideFormValues,
  serializeProviderReasoningOptions,
} from "../frontend/features/admin/domain/provider-reasoning-options.ts";

describe("Provider Resource reasoning overrides", () => {
  it("persists explicit omit, false, and empty values over Provider defaults", () => {
    const options = serializeProviderReasoningOptions({
      _existing_options: JSON.stringify({
        environment_hint: "gpu-a",
        reasoning_effort_values: "low,medium,high",
        reasoning_effort_map: '{"xhigh":"high"}',
        reasoning_effort_unsupported: "reject",
        reasoning_budget_map: '{"*":"high"}',
        preserve_reasoning_content: "true",
      }),
      _reasoning_override: "true",
      reasoning_effort_values: "",
      reasoning_effort_map: "",
      reasoning_effort_unsupported: "omit",
      reasoning_budget_map: "",
      preserve_reasoning_content: "false",
    });

    assert.deepEqual(options, {
      environment_hint: "gpu-a",
      reasoning_effort_values: "",
      reasoning_effort_map: "",
      reasoning_effort_unsupported: "omit",
      reasoning_budget_map: "",
      preserve_reasoning_content: "false",
    });
  });

  it("treats explicit resource opt-outs as overrides instead of Provider inheritance", () => {
    const values = providerReasoningOverrideFormValues(
      {
        reasoning_effort_values: "",
        reasoning_effort_map: "",
        reasoning_effort_unsupported: "omit",
        reasoning_budget_map: "",
        preserve_reasoning_content: "false",
      },
      {
        reasoning_effort_values: "low,medium,high",
        reasoning_effort_map: '{"xhigh":"high"}',
        reasoning_effort_unsupported: "reject",
        reasoning_budget_map: '{"*":"high"}',
        preserve_reasoning_content: "true",
      },
    );

    assert.equal(values._reasoning_override, "true");
    assert.equal(values.reasoning_effort_unsupported, "omit");
    assert.equal(values.preserve_reasoning_content, "false");
    assert.equal(values.reasoning_effort_values, "");
    assert.equal(values.reasoning_effort_map, "");
    assert.equal(values.reasoning_budget_map, "");
  });

  it("removes resource overrides when inheritance is selected", () => {
    const options = serializeProviderReasoningOptions({
      _existing_options: JSON.stringify({
        environment_hint: "gpu-a",
        reasoning_effort_unsupported: "omit",
        preserve_reasoning_content: "false",
      }),
      _reasoning_override: "false",
      reasoning_effort_unsupported: "reject",
      preserve_reasoning_content: "true",
    });

    assert.deepEqual(options, { environment_hint: "gpu-a" });
  });

  it("preserves explicit resource tombstones through the normal edit round trip", () => {
    const resourceOptions = {
      environment_hint: "gpu-a",
      reasoning_effort_values: "",
      reasoning_effort_map: "",
      reasoning_effort_unsupported: "omit",
      reasoning_budget_map: "",
      preserve_reasoning_content: "false",
    };
    const values = providerReasoningOverrideFormValues(resourceOptions);

    assert.equal(values._reasoning_override, "true");
    assert.deepEqual(serializeProviderReasoningOptions(values), resourceOptions);
  });

  it("displays effective values for a partial resource override without merging existing options", () => {
    const resourceOptions = {
      environment_hint: "gpu-a",
      reasoning_effort_values: "low,high",
    };
    const values = providerReasoningOverrideFormValues(resourceOptions, {
      reasoning_effort_values: "none,low,medium,high,max",
      reasoning_effort_map: '{"xhigh":"max"}',
      reasoning_effort_unsupported: "reject",
      reasoning_budget_map: '{"*":"high"}',
      preserve_reasoning_content: "true",
    });

    assert.equal(values._reasoning_override, "true");
    assert.equal(values.reasoning_effort_values, "low,high");
    assert.equal(values.reasoning_effort_map, '{"xhigh":"max"}');
    assert.equal(values.reasoning_effort_unsupported, "reject");
    assert.equal(values.reasoning_budget_map, '{"*":"high"}');
    assert.equal(values.preserve_reasoning_content, "true");
    assert.deepEqual(JSON.parse(values._existing_options), resourceOptions);
    assert.deepEqual(serializeProviderReasoningOptions(values), {
      environment_hint: "gpu-a",
      reasoning_effort_values: "low,high",
      reasoning_effort_map: '{"xhigh":"max"}',
      reasoning_effort_unsupported: "reject",
      reasoning_budget_map: '{"*":"high"}',
      preserve_reasoning_content: "true",
    });
  });
});
