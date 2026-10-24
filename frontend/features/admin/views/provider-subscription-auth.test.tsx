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
function setup(handle: (request: Request) => unknown | Promise<unknown>, opaque = false) {
  const requests: Request[] = [];
  vi.spyOn(window, "open").mockReturnValue(null);
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    const request = { capability: url.split("/").at(-1)!, body: JSON.parse(String(init.body)) };
    requests.push(request);
    expect(request.body.resource_type).toBe("test_account");
    const data = request.capability === "oauth.methods" ? { methods: [{ id: "login", label: "Synthetic login", type: "oauth" }] }
      : request.capability === "oauth.cancel" ? { status: "cancelled" } : await handle(request);
    return new Response(JSON.stringify({ data }));
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

  it("rejects non-HTTPS authorization links", async () => {
    setup(() => ({ ...session, auth_url: "http://auth.example.test" }));
    await start();
    expect(await screen.findByRole("alert")).toHaveTextContent("授权地址无效");
    expect(window.open).not.toHaveBeenCalled();
  });
});

describe("Subscription authorization concurrency", () => {
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
    expect(replacement).toMatchObject({ session_json: "", access_token: "new-access", refresh_token: "", account_id: "" });
  });
});
