import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor } from "../core/types";
import { ProviderSubscriptionAuth } from "./provider-subscription-auth";

type Request = { capability: string; body: Record<string, unknown> };
const session = { session_id: "synthetic-local-session", state: "synthetic-state", auth_url: "https://auth.example.test/login", flow: "auto", callback_mode: "none", poll_interval: 2 };
function setup(handle: (request: Request) => unknown, options: { baseURL?: string; providerType?: string; opaque?: boolean } = {}) {
  const providerType = options.providerType ?? "chatgpt-subscription";
  const api = { baseURL: options.baseURL ?? "http://localhost:8080", adminToken: "synthetic-admin" };
  const actions: PluginActionDescriptor[] = ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({ plugin_id: "test.local-subscription", action_id: capability, capability, subject: providerType, kind: "mutate", metadata: { subscription_auth: "true", subscription_session_json: String(Boolean(options.opaque)), provider_resource_type: "synthetic_account" } }));
  const requests: Request[] = [];
  vi.spyOn(window, "open").mockReturnValue(null);
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    const request = { capability: url.split("/").at(-1)!, body: JSON.parse(String(init.body)) };
    requests.push(request);
    const data = request.capability === "oauth.methods" ? { methods: [{ id: "oauth", label: "Synthetic OAuth", type: "oauth" }] }
      : request.capability === "oauth.cancel" ? { status: "cancelled" } : handle(request);
    return data instanceof Response ? data : new Response(JSON.stringify({ data }));
  }));
  const onCredentials = vi.fn();
  render(<ProviderSubscriptionAuth api={api} actions={actions} providerType={providerType} onCredentials={onCredentials} />);
  return { requests, onCredentials };
}

afterEach(() => vi.useRealTimers());

describe("Local ChatGPT automatic authorization", () => {
  it("opts in on HTTP loopback and polls until the browser callback is ready", async () => {
    let polls = 0;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start" ? session : ++polls === 1 ? { status: "pending" } : { access_token: "synthetic-local-token" });
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    expect(requests.find(request => request.capability === "oauth.start")?.body.automatic_callback).toBe(true);
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
    expect(screen.getByText("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。")).toBeVisible();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(onCredentials).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(onCredentials).toHaveBeenCalledExactlyOnceWith({ access_token: "synthetic-local-token" });
    expect(requests.filter(request => request.capability === "oauth.exchange").map(request => request.body)).toEqual([
      { resource_type: "synthetic_account", session_id: session.session_id, state: session.state, code: "" },
      { resource_type: "synthetic_account", session_id: session.session_id, state: session.state, code: "" },
    ]);
    expect(screen.getByText("已回填账号 Token")).toBeVisible();
  });

  it.each(["http://tokenhub.example.test", "https://localhost:8080", "http://user:password@localhost:8080"])("keeps manual authorization for API %s", async baseURL => {
    const { requests } = setup(() => ({ ...session, flow: "authorization_code", callback_mode: undefined }), { baseURL });
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "打开授权" }));
    expect(await screen.findByLabelText("回调结果")).toBeVisible();
    expect(requests.find(request => request.capability === "oauth.start")?.body).not.toHaveProperty("automatic_callback");
  });

  it("uses the server-selected manual flow even after a local opt-in", async () => {
    const { requests } = setup(() => ({ ...session, flow: "authorization_code", callback_mode: undefined }));
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "打开授权" }));
    expect(await screen.findByLabelText("回调结果")).toBeVisible();
    expect(requests.find(request => request.capability === "oauth.start")?.body.automatic_callback).toBe(true);
    expect(requests.filter(request => request.capability === "oauth.exchange")).toHaveLength(0);
  });

  it("preserves generic worker authorization without the ChatGPT opt-in", async () => {
    const { requests } = setup(() => session, { providerType: "workbuddy_subscription", opaque: true });
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "打开授权" }));
    await screen.findByRole("link", { name: "打开授权页面" });
    expect(requests.find(request => request.capability === "oauth.start")?.body).not.toHaveProperty("automatic_callback");
  });

  it("releases a denied automatic authorization and allows a new attempt", async () => {
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start" ? session : new Response(JSON.stringify({ error: { code: "subscription_oauth_authorization_denied", message: "Synthetic denied callback" } }), { status: 409 }));
    await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
    vi.useFakeTimers();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByRole("alert")).toHaveTextContent("本次授权未获同意。请重新打开授权，并在登录页同意所需权限。");
    expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled();
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
    expect(onCredentials).not.toHaveBeenCalled();
    expect(requests.filter(request => request.capability === "oauth.cancel")).toHaveLength(1);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(requests.filter(request => request.capability === "oauth.start")).toHaveLength(2);
  });
});
