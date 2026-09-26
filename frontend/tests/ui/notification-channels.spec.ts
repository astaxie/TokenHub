import type { AdminResource } from "../../features/admin/core/types";
import { test, expect, capture, section } from "./harness";

for (const scenario of ["webhook-success", "email-success", "email-failure", "request-failure", "loading", "empty", "disabled"] as const) {
  test(`notification-channels ${scenario}`, async ({ page, api }, testInfo) => {
    const email = scenario.startsWith("email");
    const channel: AdminResource = {
      id: "ntf_ui", kind: "notification-channels", name: email ? "UI Email" : "UI Webhook",
      status: scenario === "disabled" ? "disabled" : "active",
      fields: email ? { type: "email", smtp_host: "smtp.example.test", smtp_from: "sender@example.test", email_to: "ops@example.test", smtp_password: "********" }
        : { type: "webhook", webhook_url: "********" },
    };
    api.respond("GET", "/api/admin/resources/notification-channels", { data: scenario === "empty" ? [] : [channel] });
    let release: (() => void) | undefined;
    const pending = new Promise<void>(resolve => { release = resolve; });
    api.define("POST", "/api/admin/resources/notification-channels/ntf_ui/test", async input => {
      expect(input.body).toBeUndefined();
      if (scenario === "loading") await pending;
      if (scenario === "request-failure") return { status: 404, json: { error: { message: "Notification channel not found" } } };
      return { json: { id: "dlv_ui", channel_id: channel.id, channel: channel.fields?.type, status: scenario === "email-failure" ? "failed" : "success", ...(scenario === "email-failure" ? { error: "SMTP authentication failed" } : {}) } };
    });
    await page.goto("/notification-channels");
    if (email) await page.getByRole("tab", { name: /^Email/ }).click();
    const panel = section(page, "渠道配置");
    const send = panel.getByRole("button", { name: "发送测试通知", exact: true });
    if (scenario === "empty") {
      await expect(send).toHaveCount(0);
      await expect(panel).toBeVisible();
      await capture(page, testInfo, panel, scenario, "尚未配置通知渠道");
      return;
    }
    await expect(send).toBeVisible();
    await capture(page, testInfo, panel, `${scenario}-channel`, "通知渠道测试入口");
    await send.click();
    if (scenario === "loading") {
      await expect.poll(() => api.calls.filter(call => call.method === "POST").length).toBe(1);
      await expect(send).toBeDisabled();
      await expect(page.getByText("测试通知发送中…", { exact: true })).toBeVisible();
      await capture(page, testInfo, page.getByText("测试通知发送中…", { exact: true }), "loading", "测试通知发送中");
      release!();
    }
    const message = scenario === "email-failure" ? "测试通知发送失败：SMTP authentication failed"
      : scenario === "request-failure" ? "Notification channel not found"
      : "测试通知已提交，请到收件箱或目标渠道确认接收。";
    await expect(page.getByText(message, { exact: true })).toBeVisible();
    await capture(page, testInfo, page.getByText(message, { exact: true }), `${scenario}-result`, "测试通知投递结果");
    expect(api.calls.filter(call => call.method === "POST")).toHaveLength(1);
  });
}
