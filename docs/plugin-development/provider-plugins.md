# Provider Plugins

Language: English | [简体中文](../zh-CN/plugin-development/provider-plugins.md) | [日本語](../ja/plugin-development/provider-plugins.md)

A Provider plugin connects TokenHub to an upstream model service or subscription account. Start from [`examples/provider-mock-go`](../../plugin-devkit/examples/provider-mock-go) for the smallest contract, then inspect the Kimi and GLM examples for larger operation sets.

> **Runtime availability:** These external Provider examples and schemas are development contracts. Packages can be installed and validated, but the current TokenHub runtime rejects external Provider commands before launch because host-level isolation is not yet enforceable. Built-in Provider adapters continue to run in process.

TokenHub represents all 158 entries in the configured `provider-catalog.json` as built-in Provider plugin packages. Each package has an inspectable manifest, README, license, and catalog metadata. Catalog plugins own vendor identity, setup metadata, detail pages, and lifecycle state, while host adapters own executable protocols such as `OpenAI-Compatible`. Multiple catalog plugins can share one adapter without duplicating runtime code. Disabling a catalog plugin removes that vendor from Provider setup; enabling it restores the vendor immediately.

Provider credentials and connections belong on Provider Management pages, not on a synthetic plugin Settings page. A Provider plugin detail page links to Provider Management. Model categories remain catalog metadata and are not separate plugins.

Declare the provider type, resource types, supported operations, protocol policies, model discovery, credential scope, and any required Admin UI contribution. `ServeProvider` receives projected Provider, resource, model, request, and credential data through `stdio-json-v1`; it does not receive Core storage access.

Production requirements:

- Replace every fixed example response with a real, cancellable upstream call.
- Keep secrets out of stdout, errors, audit metadata, and fixtures.
- Test chat, streaming, errors, usage, timeouts, cancellation, discovery, and protocol conversion.
- Put recurring quota refresh or account synchronization in a [background job](background-jobs.md).
- Preserve provider type and capability IDs across compatible releases.

Validate with `tokenhub-plugin-test provider`, then test through a real TokenHub model route and API Key. See the [complete guide](guide.md) for the full Provider projection contract.
