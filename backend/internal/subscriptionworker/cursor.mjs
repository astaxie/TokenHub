import { CursorAuthPlugin } from "./vendor/cursor/index.mjs";
import { failure } from "./validation.mjs";

// The vendor reads a selected team from the CLI's local config. Server
// resources instead carry an explicitly imported teamId with their credentials.
// This wrapper leaves the pinned vendor sources byte-identical.
export async function createCursorProvider(context, getAuth) {
  const hooks = await CursorAuthPlugin(context);
  hooks["network.request"] = async (request, operation) => {
    request.headers.delete("x-cursor-team-id");
    if (!["models", "usage", "fetch"].includes(operation)) return request;
    const auth = getAuth();
    if (auth?.teamId === undefined) return request;
    if (!Number.isSafeInteger(auth.teamId) || auth.teamId <= 0) throw failure("invalid_credentials", "Cursor teamId must be a positive safe integer");
    const authorization = request.headers.get("authorization");
    // OAuth tokens must belong to this resource. API-key adapters exchange
    // their key for an account token inside the same isolated resource scope.
    if (!authorization?.startsWith("Bearer ") || (auth.type === "oauth" && authorization !== `Bearer ${auth.access}`)) return request;
    const url = new URL(request.url);
    if (!url.hostname.endsWith(".cursor.sh") && url.hostname !== "cursor.sh") return request;
    request.headers.set("x-cursor-team-id", String(auth.teamId));
    if (url.pathname === "/aiserver.v1.DashboardService/GetFilteredUsageEvents" && request.method === "POST") {
      const body = await request.clone().json();
      return new Request(request, { body: JSON.stringify({ ...body, teamId: auth.teamId }), duplex: "half" });
    }
    return request;
  };
  return hooks;
}
