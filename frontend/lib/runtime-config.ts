import "server-only";

const defaultAPIBaseURL = "http://localhost:8080";
const legacyAPIBaseURLEnv = "NEXT_PUBLIC_API_BASE_URL";

export function runtimeAPIBaseURL() {
  return (
    process.env.TOKENHUB_API_BASE_URL?.trim() ||
    process.env[legacyAPIBaseURLEnv]?.trim() ||
    defaultAPIBaseURL
  );
}
