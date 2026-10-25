import { failure } from "./validation.mjs";

// The pinned adapter defaults missing percentages to zero for display. That
// fallback cannot authorize spending when Standard-only is a routing policy.
export async function guardFactoryAllowance(context, request, response) {
  if (context.provider !== "factory" || !context.options?.standard_only || response.status !== 200 ||
      new URL(request.url).pathname !== "/api/billing/limits") return response;
  const text = await response.text();
  let body;
  try { body = JSON.parse(text); } catch { throw failure("invalid_upstream", "Factory Standard allowance could not be verified", 502); }
  const standard = body?.limits?.standard;
  const windows = ["fiveHour", "weekly", "monthly"].filter((key) => standard && Object.hasOwn(standard, key));
  if (!standard || typeof standard !== "object" || Array.isArray(standard) || windows.length === 0 ||
      windows.some((key) => !Number.isFinite(standard[key]?.usedPercent) || standard[key].usedPercent < 0)) {
    throw failure("invalid_upstream", "Factory Standard allowance could not be verified", 502);
  }
  return new Response(text, { status: response.status, statusText: response.statusText, headers: response.headers });
}
