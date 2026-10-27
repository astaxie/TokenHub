import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PluginActionDescriptor } from "../core/types";
import { ProviderSubscriptionAuth } from "./provider-subscription-auth";
import { ProviderSubscriptionReauthorization } from "./provider-subscription-reauthorization";

const chatgpt = "chatgpt-subscription", worker = "workbuddy_subscription";
const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
const registrationID = "synthetic-opaque-registration";
const guidance = "客户端注册已完成。请再次点击“打开授权”并完成登录，以继续连接账号。";
const session = { session_id: "synthetic-session", state: "synthetic-state", registration_id: registrationID, auth_url: "https://auth.example.test/login", flow: "auto", callback_mode: "none", poll_interval: 2 };
const retry = () => new Response(JSON.stringify({ error: { code: "subscription_oauth_registration_retry", message: "Synthetic registration retry" } }), { status: 409 });
type Request = { providerType: string; capability: string; body: Record<string, unknown> };

function setup(handle: (request: Request) => unknown, providerType = chatgpt, reauthorization = false) {
  const actions: PluginActionDescriptor[] = [chatgpt, worker].flatMap(subject => ["oauth.methods", "oauth.start", "oauth.exchange", "oauth.cancel"].map(capability => ({
    plugin_id: "test.subscription", action_id: capability, capability, subject, kind: "mutate", metadata: { subscription_auth: "true", subscription_session_json: String(subject === worker) },
  })));
  const requests: Request[] = [];
  vi.spyOn(window, "open").mockReturnValue(null);
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    const path = url.split("/");
    const request = { providerType: path.at(-2)!, capability: path.at(-1)!, body: JSON.parse(String(init.body)) };
    requests.push(request);
    const data = request.capability === "oauth.methods" ? { methods: [{ id: "oauth", label: "Synthetic OAuth", type: "oauth" }] }
      : request.capability === "oauth.cancel" ? { status: "cancelled" } : handle(request);
    return data instanceof Response ? data : new Response(JSON.stringify({ data }));
  }));
  const onCredentials = vi.fn();
  const element = (type = providerType, resourceID = "synthetic-first-account") => reauthorization
    ? <ProviderSubscriptionReauthorization key={`${type}:${resourceID}`} api={api} actions={actions} providerType={type} resource={{ id: resourceID, provider_id: "synthetic-provider", name: resourceID, resource_type: "synthetic-account", healthy: true, status: "active", priority: 1, weight: 100 }} onSaved={async () => {}} />
    : <ProviderSubscriptionAuth api={api} actions={actions} providerType={type} onCredentials={onCredentials} />;
  const view = render(element());
  return { ...view, requests, onCredentials, element };
}

async function enableClock() {
  await waitFor(() => expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled());
  vi.useFakeTimers();
}
async function start() {
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "打开授权" })); });
}
async function poll() {
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
}
afterEach(() => vi.useRealTimers());

describe("ChatGPT registration retry", () => {
  it("keeps the opaque registration through pending and retry, then fills fresh credentials", async () => {
    let starts = 0, polls = 0;
    const credentials = { access_token: "synthetic-access", refresh_token: "synthetic-refresh", client_id: "synthetic-issued-client" };
    const storage = vi.spyOn(Storage.prototype, "setItem");
    const { requests, onCredentials } = setup(({ capability }) => {
      if (capability === "oauth.start") return { ...session, session_id: `session-${++starts}`, state: `state-${starts}` };
      if (starts === 1) return ++polls === 1 ? { status: "pending" } : retry();
      return credentials;
    });
    await enableClock();
    await start();
    expect(requests.find(request => request.capability === "oauth.start")?.body).not.toHaveProperty("registration_id");
    expect(requests.find(request => request.capability === "oauth.start")?.body).not.toHaveProperty("resource_id");
    await poll();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await poll();
    expect(screen.getByRole("alert")).toHaveTextContent(guidance);
    expect(screen.getByRole("button", { name: "打开授权" })).toBeEnabled();
    expect(screen.queryByLabelText("回调结果")).not.toBeInTheDocument();
    expect(onCredentials).not.toHaveBeenCalled();
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body.registration_id).toBe(registrationID);
    await poll();
    expect(requests.filter(request => request.capability === "oauth.exchange").at(-1)?.body).toEqual({ session_id: "session-2", state: "state-2", code: "" });
    expect(onCredentials).toHaveBeenCalledExactlyOnceWith(credentials);
    expect(screen.getByText("已回填账号 Token")).toBeVisible();
    expect(screen.getByText("账号凭据已回填，请保存账号资源以完成接入。")).toBeVisible();
    expect(storage).not.toHaveBeenCalled();
  });

  it("retains registration after a manual exchange failure and accepts a callback without client_id", async () => {
    let starts = 0;
    const { requests, onCredentials } = setup(({ capability }) => capability === "oauth.start"
      ? { ...session, flow: "authorization_code", callback_mode: "manual", session_id: `session-${++starts}` }
      : starts === 1 ? retry() : { access_token: "synthetic-manual-access" });
    await enableClock();
    await start();
    for (const code of ["first", "fresh"]) {
      fireEvent.change(screen.getByLabelText("回调结果"), { target: { value: `http://localhost:1455/auth/callback?code=${code}&state=synthetic-state` } });
      await act(async () => { fireEvent.click(screen.getByRole("button", { name: "完成授权" })); });
      if (code === "first") {
        expect(screen.getByRole("alert")).toHaveTextContent(guidance);
        await start();
      }
    }
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body.registration_id).toBe(registrationID);
    expect(requests.filter(request => request.capability === "oauth.exchange").at(-1)?.body.client_id).toBeFalsy();
    expect(onCredentials).toHaveBeenCalledExactlyOnceWith({ access_token: "synthetic-manual-access" });
  });

  it("clears a retained registration when the user explicitly cancels the next attempt", async () => {
    const { requests } = setup(({ capability }) => capability === "oauth.start" ? session : retry());
    await enableClock();
    await start();
    await poll();
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body.registration_id).toBe(registrationID);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body).not.toHaveProperty("registration_id");
  });

  it("clears registration when the provider changes and never sends it to a worker", async () => {
    const { requests, rerender, element } = setup(({ capability }) => capability === "oauth.start" ? session : retry());
    await enableClock();
    await start();
    await poll();
    await act(async () => { rerender(element(worker)); });
    await start();
    const workerStart = requests.filter(request => request.capability === "oauth.start").at(-1);
    expect(workerStart?.providerType).toBe(worker);
    expect(workerStart?.body).not.toHaveProperty("registration_id");
    await poll();
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body).not.toHaveProperty("registration_id");
    await poll();
    await act(async () => { rerender(element(chatgpt)); });
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body).not.toHaveProperty("registration_id");
  });

  it("does not retain a registration across dialogs", async () => {
    const { requests, unmount, element } = setup(({ capability }) => capability === "oauth.start" ? session : retry());
    await enableClock();
    await start();
    await poll();
    unmount();
    await act(async () => { render(element()); });
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body).not.toHaveProperty("registration_id");
  });

  it("binds saved ChatGPT reauthorization to the selected account and resets its retry registration on selection change", async () => {
    const { requests, rerender, element } = setup(({ capability }) => capability === "oauth.start" ? session : retry(), chatgpt, true);
    fireEvent.click(screen.getByRole("button", { name: "重新授权账号" }));
    await enableClock();
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body.resource_id).toBe("synthetic-first-account");
    await poll();
    await start();
    expect(requests.filter(request => request.capability === "oauth.start").at(-1)?.body).toMatchObject({ resource_id: "synthetic-first-account", registration_id: registrationID });
    await act(async () => { rerender(element(chatgpt, "synthetic-second-account")); });
    expect(screen.queryByRole("button", { name: "打开授权" })).not.toBeInTheDocument();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "重新授权账号" })); });
    await start();
    const nextStart = requests.filter(request => request.capability === "oauth.start").at(-1)?.body;
    expect(nextStart?.resource_id).toBe("synthetic-second-account");
    expect(nextStart).not.toHaveProperty("registration_id");
  });

  it("does not add ChatGPT resource binding to a saved worker account", async () => {
    const { requests } = setup(() => session, worker, true);
    fireEvent.click(screen.getByRole("button", { name: "重新授权账号" }));
    await enableClock();
    await start();
    const request = requests.find(request => request.capability === "oauth.start")?.body;
    expect(request).not.toHaveProperty("resource_id");
    expect(request).not.toHaveProperty("registration_id");
  });
});
