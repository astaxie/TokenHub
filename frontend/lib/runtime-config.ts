import "server-only";

const defaultAPIBaseURL = "http://localhost:8080";
const runtimeAPIBaseURLEnv = "NEXT_PUBLIC_API_BASE_URL";

export function runtimeAPIBaseURL() {
  return process.env[runtimeAPIBaseURLEnv]?.trim() || defaultAPIBaseURL;
}
