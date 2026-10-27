import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor } from "../core/types";
import { ProviderSubscriptionAuth, subscriptionCredentialValues } from "./provider-subscription-auth";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
function actions(opaque = false): PluginActionDescriptor[] {
  return ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({
    plugin_id: "test.subscription", action_id: capability, capability, subject: "test_subscription", kind: "mutate",
    metadata: { provider_resource_type: "test_account", subscription_auth: "true", ...(opaque ? { subscription_session_json: "true" } : {}) },
  }));
}
type Request = { capability: string; body: Record<string, unknown> };
type Method = { id: string; label: string; type: string; prompts?: Array<{ key: string; label: string; type: string }> };
function setup(handle: (request: Request) => unknown | Promise<unknown>, opaque = false, methodList: Method[] = [{ id: "login", label: "Synthetic login", type: "oauth" }]) {
  const requests: Request[] = [];
  vi.spyOn(window, "open").mockReturnValue(null);
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    const request = { capability: url.split("/").at(-1)!, body: JSON.parse(String(init.body)) };
    requests.push(request);
    expect(request.body.resource_type).toBe("test_account");
    const data = request.capability === "oauth.methods" ? { methods: methodList }
      : request.capability === "oauth.cancel" ? { status: "cancelled" } : await handle(request);
    return data instanceof Response ? data : new Response(JSON.stringify({ data }));
  }));
  const onCredentials = vi.fn();
  const view = render(<ProviderSubscriptionAuth api={api} actions={actions(opaque)} providerType="test_subscription" onCredentials={onCredentials} />);
  return { ...view, onCredentials, requests };
}
async function start() {
  await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "打开授权" }));
}
const session = { session_id: "synthetic-session", state: "server-state", auth_url: "https://auth.example.test/login", flow: "authorization_code" };
afterEach(() => { vi.useRealTimers(); });

describe("Subscription authorization", () => {
  it("uses visible text inputs for non-secret prompts", async () => {
    setup(({ capability }) => capability === "oauth.start" ? session : { access_token: "synthetic-token" }, false, [
      { id: "login", label: "Synthetic login", type: "oauth", prompts: [
        { key: "account", label: "Account name", type: "text" },
        { key: "secret", label: "Account secret", type: "password" },
      ] },
    ]);
    await start();
    expect(screen.getByLabelText("Account name")).toHaveAttribute("type", "text");
    expect(screen.getByLabelText("Account secret")).toHaveAttribute("type", "password");
  });

  it("exchanges the callback state and dynamic OAuth client ID", async () => {
    const { onCredentials, requests } = setup(({ capability }) => capability === "oauth.start" ? session : { access_token: "synthetic-token", client_id: "dynamic-client" });
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "http://localhost:1455/auth/callback?code=synthetic-code&state=callback-state&client_id=dynamic-client" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(onCredentials).toHaveBeenCalledWith({ access_token: "synthetic-token", client_id: "dynamic-client" }));
    expect(requests.at(-1)).toMatchObject({ capability: "oauth.exchange", body: { session_id: session.session_id, state: "callback-state", code: "synthetic-code", client_id: "dynamic-client" } });
  });

  it("binds an opaque worker callback to the TokenHub state", async () => {
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start" ? session : { session_json: '{"refresh":"synthetic-rotated"}' }, true);
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "http://localhost:4567/?code=worker-code&state=worker-state" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(onCredentials).toHaveBeenCalledOnce());
    expect(requests.at(-1)?.body).toMatchObject({ state: "server-state", code: "worker-code", callback_url: "http://localhost:4567/?code=worker-code&state=worker-state" });
  });

  it("restarts native authorization after a rejected single-use callback", async () => {
    let starts = 0, exchanges = 0;
    const { requests, onCredentials } = setup(({ capability }) => {
      if (capability === "oauth.start") return { ...session, session_id: `session-${++starts}`, state: `state-${starts}` };
      if (++exchanges === 1) return new Response(JSON.stringify({ error: { code: "subscription_oauth_code_rejected", message: "Synthetic rejected authorization code" } }), { status: 502 });
      return { access_token: "fresh-account-token" };
    });
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "http://localhost:1455/auth/callback?code=old-code&state=state-1" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled();
    expect(screen.getByRole("alert")).toHaveTextContent("授权码已失效、已被使用或与本次授权不匹配。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
    expect(onCredentials).not.toHaveBeenCalled();
    expect(requests.filter(request => request.capability === "oauth.cancel")).toEqual([{ capability: "oauth.cancel", body: { resource_type: "test_account", session_id: "session-1", state: "state-1" } }]);
    await start();
    expect(await screen.findByLabelText("回调结果")).toHaveValue("");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("回调结果"), { target: { value: "http://localhost:1455/auth/callback?code=fresh-code&state=state-2" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(onCredentials).toHaveBeenCalledExactlyOnceWith({ access_token: "fresh-account-token" }));
    expect(requests.filter(request => request.capability === "oauth.exchange").at(-1)?.body).toMatchObject({ session_id: "session-2", state: "state-2", code: "fresh-code" });
  });

  it("keeps an opaque worker callback session available for correction", async () => {
    const { requests } = setup(({ capability }) => capability === "oauth.start" ? session : new Response(JSON.stringify({ error: { message: "Synthetic worker callback failure" } }), { status: 400 }), true);
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "worker-code" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Synthetic worker callback failure");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeDisabled();
    expect(screen.getByLabelText("回调结果")).toHaveValue("worker-code");
    expect(screen.getByRole("button", { name: "完成授权" })).toBeEnabled();
    expect(requests.filter(request => request.capability === "oauth.cancel")).toHaveLength(0);
  });

  it("imports the rotated credentials and catalog together without browser storage", async () => {
    const result = { session_json: '{"refresh":"synthetic-new"}', catalog: { id: "synthetic", models: [{ id: "synthetic-model" }] } };
    const storage = vi.spyOn(Storage.prototype, "setItem");
    const { onCredentials, requests } = setup(() => result, true);
    fireEvent.click(screen.getByText("导入订阅会话", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText(/^订阅会话 JSON/), { target: { value: '{"refresh":"synthetic-old"}' } });
    fireEvent.click(screen.getByRole("button", { name: "导入订阅会话" }));
    await waitFor(() => expect(onCredentials).toHaveBeenCalledWith(result));
    expect(requests.at(-1)?.body).toEqual({ resource_type: "test_account", mode: "import", session_json: '{"refresh":"synthetic-old"}' });
    expect(screen.getByLabelText(/^订阅会话 JSON/)).toHaveValue("");
    expect(storage).not.toHaveBeenCalled();
  });

  it("rejects an array before making an import request", async () => {
    const { requests, onCredentials } = setup(() => ({}), true);
    fireEvent.click(screen.getByText("导入订阅会话", { selector: "summary" }));
    fireEvent.change(screen.getByLabelText(/^订阅会话 JSON/), { target: { value: "[]" } });
    fireEvent.click(screen.getByRole("button", { name: "导入订阅会话" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("订阅会话必须是 JSON 对象。");
    expect(requests.filter(r => r.capability === "oauth.exchange")).toHaveLength(0);
    expect(onCredentials).not.toHaveBeenCalled();
  });

  it("cancels the remote session and ignores a late callback result", async () => {
    let complete!: (value: unknown) => void;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start" ? session : new Promise(resolve => { complete = resolve; }));
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "synthetic-code" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(complete).toBeDefined());
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await act(async () => { complete({ access_token: "stale-token" }); });
    expect(onCredentials).not.toHaveBeenCalled();
    expect(requests.at(-1)).toMatchObject({ capability: "oauth.cancel", body: { session_id: session.session_id, state: session.state } });
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
  });

  it("releases a pending remote session on unmount", async () => {
    const { unmount, requests } = setup(() => session);
    await start();
    await screen.findByLabelText("回调结果");
    unmount();
    expect(requests.at(-1)?.capability).toBe("oauth.cancel");
  });

  it("polls device authorization with slow-down backoff", async () => {
    let polls = 0;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start"
      ? { ...session, flow: "device_code", user_code: "ABCD-EFGH", poll_interval: 2 }
      : ++polls === 1 ? { status: "slow_down", poll_interval: 7 } : { access_token: "device-token" });
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    expect(screen.getByText("ABCD-EFGH")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(requests.filter(r => r.capability === "oauth.exchange")).toHaveLength(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(onCredentials).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(onCredentials).toHaveBeenCalledWith({ access_token: "device-token" });
  });

  it("automatically fills a browser subscription session after pending authorization", async () => {
    let polls = 0;
    const credentials = { session_json: '{"type":"oauth","access":"synthetic-workbuddy"}' };
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start"
      ? { ...session, flow: "auto", callback_mode: "none", poll_interval: 2 }
      : ++polls === 1 ? { status: "pending" } : credentials, true);
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    expect(screen.getByText("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。")).toBeInTheDocument();
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(onCredentials).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(onCredentials).toHaveBeenCalledExactlyOnceWith(credentials);
    expect(requests.filter(request => request.capability === "oauth.exchange").map(request => request.body)).toEqual([
      { resource_type: "test_account", session_id: session.session_id, state: session.state, code: "" },
      { resource_type: "test_account", session_id: session.session_id, state: session.state, code: "" },
    ]);
    expect(screen.getByText("已回填账号 Token")).toBeInTheDocument();
    expect(screen.getByText("账号凭据已回填，请保存账号资源以完成接入。")).toBeInTheDocument();
  });

  it("explains a failed browser authorization without requiring a pasted code", async () => {
    setup(({ capability }) => {
      if (capability === "oauth.start") return { ...session, flow: "auto", callback_mode: "none", poll_interval: 2 };
      return new Response(JSON.stringify({ error: { code: "subscription_authorization_failed", message: "Subscription authorization failed or expired; start again" } }), { status: 400 });
    }, true);
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByRole("alert")).toHaveTextContent("未能取得账号凭据，授权可能失败或已过期。请重新打开授权并在网页完成登录；无需手动复制 Token。");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled();
  });

  for (const flow of ["auto", "device_code"]) it(`cancels the remote ${flow} session after a polling failure`, async () => {
    const { requests, onCredentials, unmount } = setup(({ capability }) => {
      if (capability === "oauth.start") return { ...session, flow, poll_interval: 2 };
      throw new TypeError("Synthetic network failure");
    });
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByRole("alert")).toHaveTextContent("Synthetic network failure");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "取消" })).not.toBeInTheDocument();
    expect(onCredentials).not.toHaveBeenCalled();
    expect(requests.at(-1)).toMatchObject({ capability: "oauth.cancel", body: { session_id: session.session_id, state: session.state } });
    unmount();
    expect(requests.filter(request => request.capability === "oauth.cancel")).toHaveLength(1);
  });

  it("rejects non-HTTPS authorization links", async () => {
    setup(() => ({ ...session, auth_url: "http://auth.example.test" }));
    await start();
    expect(await screen.findByRole("alert")).toHaveTextContent("授权地址无效");
    expect(window.open).not.toHaveBeenCalled();
  });
});

describe("Subscription authorization concurrency", () => {
  it("ignores a rejected native exchange after a new session has started", async () => {
    let reject!: (error: Error) => void;
    let starts = 0;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start"
      ? { ...session, session_id: `session-${++starts}` }
      : new Promise((_resolve, rejectExchange) => { reject = rejectExchange; }));
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "old-code" } });
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    await waitFor(() => expect(reject).toBeDefined());
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await start();
    fireEvent.change(await screen.findByLabelText("回调结果"), { target: { value: "new-code" } });
    await act(async () => { reject(new Error("Stale exchange failure")); });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByLabelText("回调结果")).toHaveValue("new-code");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeDisabled();
    expect(requests.filter(request => request.capability === "oauth.cancel")).toHaveLength(1);
    expect(onCredentials).not.toHaveBeenCalled();
  });

  it("retains a completed poll while a manual callback is waiting", async () => {
    let complete!: (value: unknown) => void;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start"
      ? { ...session, flow: "auto", poll_interval: 2 }
      : new Promise(resolve => { complete = resolve; }), true);
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    fireEvent.change(screen.getByLabelText("回调结果"), { target: { value: "http://localhost:4567/?code=worker-code" } });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByRole("button", { name: "完成授权" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "完成授权" }));
    expect(requests.filter(request => request.capability === "oauth.exchange")).toHaveLength(1);
    const credentials = { session_json: '{"access":"completed-poll"}' };
    await act(async () => { complete(credentials); });
    expect(onCredentials).toHaveBeenCalledExactlyOnceWith(credentials);
  });

  it("replaces previous account secrets when a new subscription is authorized", () => {
    const original = { session_json: '{"access":"old-session"}', access_token: "old-access", refresh_token: "old-refresh", account_id: "old-account" };
    const replacement = { ...original, ...subscriptionCredentialValues({ access_token: "new-access" }, true) };
    expect(replacement).toMatchObject({ replace_credentials: "true", session_json: "", access_token: "new-access", refresh_token: "", account_id: "" });
  });
});
