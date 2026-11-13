import { tx } from "../i18n/runtime";

export function requestErrorInfo(responseBody?: string) {
  try {
    const response: unknown = JSON.parse(responseBody || "");
    if (!response || typeof response !== "object" || !("error" in response)) return null;
    const error = response.error;
    if (!error || typeof error !== "object") return null;
    const message = "message" in error && typeof error.message === "string" ? error.message : "";
    const details = "details" in error ? error.details : null;
    // Routing diagnostics mark rejected requests before any upstream attempt.
    // Jev admission now sets the same flag, so the stage name is not required.
    const beforeUpstream = !!details && typeof details === "object" &&
      (details as Record<string, unknown>).upstream_attempted === false;
    return { message, beforeUpstream };
  } catch { return null; }
}

export function RequestErrorSummary({ code, responseBody }: { code: string; responseBody?: string }) {
  const error = requestErrorInfo(responseBody);
  return <div className="request-error-box" role="alert">
    <strong>{code}</strong>
    {error?.beforeUpstream ? <p>{tx("请求在路由检查阶段被拒绝，尚未发送到上游。")}</p> : null}
    {error?.message && error.message !== code ? <p>{error.message}</p> : null}
  </div>;
}
