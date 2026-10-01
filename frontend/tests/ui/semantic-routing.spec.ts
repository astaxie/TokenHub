import type { ModelRoute, ModelRoutePolicy, Provider, ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { model, shellResponses } from "./fixtures/shell";

for (const state of ["save", "failure", "mobile", "classifier"] as const) {
  test(`semantic-routing ${state}`, async ({ page, api }, testInfo) => {
    if (state === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    const routes: ModelRoute[] = [0, 1].map(index => ({ id: `route_ui_${index}`, model_name: model.name, provider_id: `provider_ui_${index}`, provider_model: `upstream-${index}`, priority: 1, weight: 100, quality_score: 50, cost_score: 50, status: "active", strategy: "quality" }));
    // A usable classifier: an active public model with an active non-semantic route.
    const classifier = { ...model, id: "mdl_ui_router", name: "ui-router-small" };
    const classifierRoute: ModelRoute = { ...routes[0], id: "route_ui_router", model_name: classifier.name, provider_model: "router-upstream" };
    const providers: Provider[] = [0, 1].map(index => ({ id: `provider_ui_${index}`, name: `UI Provider ${index}`, type: "mock", priority: 1, healthy: true, status: "active" }));
    api.respond("GET", "/api/admin/providers", { data: providers });
    const otherRoutes = state === "classifier" ? [classifierRoute] : [];
    api.respond("GET", "/api/admin/routing-rules", { data: [...routes, ...otherRoutes] });
    for (const path of ["provider-resources", "plugins", "provider-adapters", "plugin-ui-manifest", "plugin-actions", "plugin-background-jobs"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
    const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
    const models = state === "classifier" ? [model, classifier] : [model];
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, providers, models });
    const saved: ModelRoutePolicy[] = [];
    let candidateOrder = [0, 1];
    let confidence = 0.8;
    const criteria = ["Simple extraction and translation", "Complex analysis and code changes"];
    if (state === "save") {
      const providerModels: ProviderModel[] = routes.map(route => ({ id: `catalog-${route.id}`, provider_id: route.provider_id, upstream_model: route.provider_model, status: "active" }));
      providerModels.push({ id: "replacement-catalog", provider_id: routes[1].provider_id, upstream_model: "replacement", status: "active", metadata: { routing_description: "Replacement model tasks" } });
      api.replaceResponse("GET", "/api/admin/provider-models", { data: providerModels });
      api.define("PATCH", `/api/admin/routing-rules/${routes[1].id}`, input => {
        expect(input.body).toMatchObject({ provider_id: routes[1].provider_id, provider_model: "replacement", strategy: "semantic" });
        routes[1] = { ...routes[1], provider_model: "replacement", strategy: "semantic" };
        api.replaceResponse("GET", "/api/admin/routing-rules", { data: routes.map(route => ({ ...route, strategy: "semantic" })) });
        return { json: routes[1] };
      });
    }
    api.define("PATCH", `/api/admin/model-routing-policies/${model.name}`, input => {
      const policy = input.body as ModelRoutePolicy;
      expect(policy.routes).toEqual(routes.map(route => ({ route_id: route.id, weight: 100, quality_score: 50, cost_score: 50 })));
      if (policy.strategy === "semantic") {
        if (state === "classifier") {
          expect(policy.semantic_routing).toMatchObject({ evaluator: "model", classifier_model: classifier.name, classifier_timeout_ms: 2500 });
        } else {
          expect(policy.semantic_routing).not.toHaveProperty("evaluator");
          expect(policy.semantic_routing?.min_confidence).toBe(confidence);
        }
        expect(policy.semantic_routing?.mode).toBe("enforce");
        expect(policy.semantic_routing?.default_candidate_id).toBe("route_ui_1");
        expect(policy.semantic_routing?.candidates).toEqual(candidateOrder.map(index => ({ id: routes[index].id, provider_id: routes[index].provider_id, provider_model: routes[index].provider_model, criteria: criteria[index] })));
      } else expect(policy.semantic_routing?.mode).toBe("off");
      saved.push(policy);
      if (state === "failure") return { status: 500, json: { error: { message: "Synthetic save failure" } } };
      api.replaceResponse("GET", "/api/admin/overview", { ...overview, providers, models: [{ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(policy.semantic_routing) } }, ...models.slice(1)] });
      const updated = routes.map(route => ({ ...route, strategy: policy.strategy }));
      api.replaceResponse("GET", "/api/admin/routing-rules", { data: [...updated, ...otherRoutes] });
      return { json: { strategy: policy.strategy, data: updated, semantic_routing: policy.semantic_routing } };
    });
    await page.goto("/routes");
    // The classifier state lists a second model; keep every control on the routed one.
    const card = page.locator("article.route-model-card").filter({ hasText: model.name });
    await expect(card.getByLabel("模型选择指令")).toHaveCount(0);
    await card.getByRole("tab", { name: "智能路由", exact: true }).click();
    const panel = card.getByRole("group", { name: "智能路由设置" });
    await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
    await panel.getByLabel("upstream-0 · UI Provider 0 的适用条件").fill("Simple extraction and translation");
    await panel.getByLabel("upstream-1 · UI Provider 1 的适用条件").fill("Complex analysis and code changes");
    await panel.getByLabel("默认模型").selectOption("route_ui_1");
    if (state === "classifier") {
      await panel.getByLabel("分类器").selectOption("model");
      await expect(panel.getByLabel("最低置信度")).toHaveCount(0);
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await panel.getByLabel("分类模型").selectOption(classifier.name);
      await panel.getByLabel("分类超时（毫秒）").fill("2500");
      // The panel is taller than the viewport here: capture it as a named viewport segment.
      await panel.evaluate(element => element.scrollIntoView({ block: "start" }));
      await capture(page, testInfo, panel, "semantic-routing-classifier-model", "智能路由：TokenHub 模型分类器", "viewport");
    } else {
      await panel.getByLabel("最低置信度").fill("1.5");
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await panel.getByLabel("最低置信度").fill("0.8");
    }
    await card.getByRole("button", { name: "应用策略" }).click();
    if (state === "failure") {
      await expect(page.getByText("Synthetic save failure")).toBeVisible();
      await capture(page, testInfo, page.getByText("Synthetic save failure"), "semantic-routing-save-error", "智能路由：保存失败提示");
      await expect(panel.getByLabel("默认模型")).toHaveValue("route_ui_1");
      await expect(card.getByRole("button", { name: "应用策略" })).toBeEnabled();
    } else {
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await page.reload();
      await expect(card.getByRole("tab", { name: "智能路由", exact: true })).toHaveAttribute("aria-selected", "true");
      await expect(panel.getByLabel("默认模型")).toHaveValue("route_ui_1");
      await expect(panel.getByLabel("upstream-1 · UI Provider 1 的适用条件")).toHaveValue("Complex analysis and code changes");
    }
    if (state === "mobile") {
      await panel.getByLabel("模型选择指令").scrollIntoViewIfNeeded();
      await capture(page, testInfo, panel.getByLabel("模型选择指令"), "semantic-routing-mobile-instructions", "智能路由：指令与默认模型", "viewport");
      await panel.getByLabel("upstream-1 · UI Provider 1 的适用条件").scrollIntoViewIfNeeded();
      await capture(page, testInfo, panel.getByLabel("upstream-1 · UI Provider 1 的适用条件"), "semantic-routing-mobile-candidates", "智能路由：候选模型", "viewport");
    } else if (state === "classifier") {
      await expect(panel.getByLabel("分类器")).toHaveValue("model");
      await expect(panel.getByLabel("分类模型")).toHaveValue(classifier.name);
      await expect(panel.getByLabel("分类超时（毫秒）")).toHaveValue("2500");
    } else {
      await capture(page, testInfo, card.getByRole("tablist", { name: "模型路由策略" }), `semantic-routing-${state}-strategies`, "模型路由策略选项");
      await capture(page, testInfo, panel, `semantic-routing-${state}`, "智能路由设置：TypeSafe Jev 分类器");
    }
    if (state === "save") {
      await panel.getByRole("checkbox", { name: "upstream-0 · UI Provider 0", exact: true }).uncheck();
      await panel.getByRole("checkbox", { name: "upstream-0 · UI Provider 0", exact: true }).check();
      await panel.getByLabel("upstream-0 · UI Provider 0 的适用条件").fill("Simple extraction and translation");
      candidateOrder = [1, 0];
      await card.getByRole("button", { name: "应用策略" }).click();
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await page.reload();
      confidence = 0.75;
      await panel.getByLabel("最低置信度").fill("0.75");
      await card.getByRole("button", { name: "应用策略" }).click();
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await card.locator(".route-provider-row").filter({ hasText: "upstream-1" }).getByRole("button", { name: "编辑", exact: true }).click();
      const dialog = page.locator(".modal").filter({ has: page.getByRole("heading", { name: "路由策略", exact: true }) });
      await dialog.getByRole("combobox", { name: /^Provider 模型/ }).selectOption("replacement");
      await dialog.getByRole("combobox", { name: /^项目作用域/ }).selectOption("all");
      await dialog.getByRole("button", { name: "保存", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      await expect(panel.getByLabel("replacement · UI Provider 1 的适用条件")).toHaveValue("Replacement model tasks");
      await expect(card.getByRole("button", { name: "应用策略" })).toBeEnabled();
      criteria[1] = "Replacement model tasks";
      confidence = 0.85;
      await panel.getByLabel("最低置信度").fill("0.85");
      await card.getByRole("button", { name: "应用策略" }).click();
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await page.reload();
      await expect(panel.getByLabel("replacement · UI Provider 1 的适用条件")).toHaveValue("Replacement model tasks");
      await card.getByRole("tab", { name: "固定比例" }).click();
      await card.getByRole("button", { name: "应用策略" }).click();
      await expect(card.getByRole("button", { name: "应用策略" })).toBeDisabled();
      await page.reload();
      await expect(card.getByLabel("模型选择指令")).toHaveCount(0);
      expect(saved.map(policy => policy.strategy)).toEqual(["semantic", "semantic", "semantic", "semantic", "priority_weighted"]);
    }
  });
}
