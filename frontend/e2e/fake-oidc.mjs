import { createHash, randomBytes } from "node:crypto";

const authorizations = new Map();
const accessTokens = new Set();

function json(response, status, body) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
}

export async function handleFakeOIDC(request, response) {
  const url = new URL(request.url, "http://oidc.test");
  if (!url.pathname.startsWith("/oidc/")) return false;

  if (request.method === "GET" && url.pathname === "/oidc/authorize") {
    const params = url.searchParams;
    const redirectURI = params.get("redirect_uri");
    if (params.get("client_id") !== "e2e-oidc-client" || params.get("response_type") !== "code"
      || !params.get("state") || !redirectURI || params.get("code_challenge_method") !== "S256"
      || !params.get("code_challenge")) {
      json(response, 400, { error: "invalid_request" });
      return true;
    }
    const callback = new URL(redirectURI);
    callback.searchParams.set("state", params.get("state"));
    if (params.get("scenario") === "deny") {
      callback.searchParams.set("error", "access_denied");
    } else {
      const code = randomBytes(24).toString("hex");
      authorizations.set(code, { redirectURI, challenge: params.get("code_challenge") });
      callback.searchParams.set("code", code);
    }
    // Keycloak includes these parameters alongside the authorization code.
    callback.searchParams.set("session_state", "e2e-keycloak-session");
    response.writeHead(302, { location: callback.toString() });
    response.end();
    return true;
  }

  if (request.method === "POST" && url.pathname === "/oidc/token") {
    let body = "";
    for await (const chunk of request) body += chunk;
    const params = new URLSearchParams(body);
    const code = params.get("code");
    const authorization = authorizations.get(code);
    authorizations.delete(code);
    const challenge = createHash("sha256").update(params.get("code_verifier") ?? "").digest("base64url");
    if (params.get("grant_type") !== "authorization_code" || params.get("client_id") !== "e2e-oidc-client"
      || params.get("client_secret") !== "e2e-oidc-secret" || !authorization
      || params.get("redirect_uri") !== authorization.redirectURI || challenge !== authorization.challenge) {
      json(response, 400, { error: "invalid_grant" });
      return true;
    }
    const accessToken = randomBytes(24).toString("hex");
    accessTokens.add(accessToken);
    json(response, 200, { access_token: accessToken, token_type: "Bearer", expires_in: 300 });
    return true;
  }

  if (request.method === "GET" && url.pathname === "/oidc/userinfo") {
    if (!accessTokens.has(request.headers.authorization?.replace(/^Bearer /, ""))) {
      json(response, 401, { error: "invalid_token" });
      return true;
    }
    json(response, 200, {
      sub: "e2e-keycloak-user",
      preferred_username: "e2e.oidc.user",
      name: "E2E OIDC User",
      email: "e2e.oidc.user@example.test",
      email_verified: true,
    });
    return true;
  }

  json(response, 404, { error: "not_found" });
  return true;
}
