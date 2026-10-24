import childProcess from "node:child_process";
import http from "node:http";
import https from "node:https";
import http2 from "node:http2";
import { syncBuiltinESMExports } from "node:module";
import { failure } from "./validation.mjs";

// Defense in depth for the fixed adapter set, not a sandbox for untrusted code.
// Credential imports cannot request subprocesses or local account discovery.
export function installIsolation(scope) {
  const createServer = http.createServer;
  const localRequest = http.request;
  const forbidden = () => { throw failure("local_credentials_not_supported", "Local commands and agent-session discovery are disabled"); };
  for (const key of ["exec", "execSync", "execFile", "execFileSync", "spawn", "spawnSync", "fork"]) childProcess[key] = forbidden;
  // Cursor already implements RunSSE/BidiAppend when HTTP/2 is unavailable.
  // Forcing that path keeps every upstream byte on Go's guarded transport.
  http2.connect = () => { throw new Error("h2 is not supported by this subscription transport"); };
  http.request = forbidden;
  http.get = forbidden;
  https.request = forbidden;
  https.get = forbidden;
  http.createServer = (...args) => {
    const context = scope.getStore();
    if (!context?.session) throw failure("invalid_session", "A pending authorization is required for a callback listener");
    const server = createServer(...args);
    context.session.servers.add(server);
    server.on("close", () => context.session.servers.delete(server));
    const listen = server.listen.bind(server);
    server.listen = (...listenArgs) => {
      if (typeof listenArgs[0] === "object") {
        const options = listenArgs[0];
        if (options.path || (options.host && !["localhost", "127.0.0.1", "::1"].includes(options.host))) forbidden();
        return listen({ ...options, host: options.host || "127.0.0.1" }, ...listenArgs.slice(1));
      }
      if (typeof listenArgs[0] !== "number") forbidden();
      if (typeof listenArgs[1] === "string") {
        if (!["localhost", "127.0.0.1", "::1"].includes(listenArgs[1])) forbidden();
      } else listenArgs.splice(1, 0, "127.0.0.1");
      return listen(...listenArgs);
    };
    return server;
  };
  syncBuiltinESMExports();

  return async function relayCallback(session, value) {
    let url;
    try { url = new URL(value); } catch { throw failure("callback_not_allowed", "The callback URL is invalid"); }
    const address = url.hostname.replace(/^\[|\]$/g, "");
    if (url.protocol !== "http:" || url.username || url.password || url.hash || !["localhost", "127.0.0.1", "::1"].includes(address) ||
      ![...session.servers].some((server) => server.address()?.port === Number(url.port))) {
      throw failure("callback_not_allowed", "The callback URL does not belong to this authorization session");
    }
    return new Promise((resolve, reject) => {
      // Only the exact listener owned by this pending session is reachable.
      // The vendor adapter still verifies its own state, path and PKCE code.
      const request = localRequest(url, { method: "GET", timeout: 30000 }, (response) => {
        response.resume();
        response.on("end", () => resolve({ status: response.statusCode }));
      });
      request.on("timeout", () => request.destroy(new Error("Callback timed out")));
      request.on("error", reject);
      request.end();
    });
  };
}
