#!/usr/bin/env node

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { loadDotEnv, maskKey } from "./lib/env.mjs";

const __dirname = dirname(fileURLToPath(import.meta.url));
loadDotEnv(resolve(__dirname, ".env"));

const adminBaseURL = normalizeAdminBaseURL(
  process.env.TOKENHUB_ADMIN_BASE_URL
    || process.env.TOKENHUB_BASE_URL
    || "http://localhost:8080",
);
const adminToken = process.env.TOKENHUB_ADMIN_TOKEN || "dev_admin_token";
const policyID = process.env.TOKENHUB_SECURITY_POLICY_ID || "sec_ip_allowlist";
const policyName = process.env.TOKENHUB_SECURITY_POLICY_NAME || "生产 IP 白名单策略";

if (!adminToken) {
  console.error("Missing TOKENHUB_ADMIN_TOKEN.");
  console.error("For local dev you can use:");
  console.error("  TOKENHUB_ADMIN_TOKEN=dev_admin_token npm run test:security-policy");
  process.exit(1);
}

const desiredPolicy = {
  id: policyID,
  name: policyName,
  description: "记录模型 API 的推荐 IP 白名单、Prompt 脱敏和错误透传规则",
  status: "active",
  fields: {
    mask_prompts: true,
    ip_allowlist: "127.0.0.1/32\n10.0.0.0/8",
    error_passthrough: "sanitized",
  },
};

console.log("TokenHub security policy smoke test");
console.log(`Admin URL: ${adminBaseURL}`);
console.log(`Policy ID: ${policyID}`);
console.log(`Policy:    ${policyName}`);
console.log(`Token:     ${maskKey(adminToken)}`);
console.log("");

try {
  const policies = await listSecurityPolicies();
  const existing = policies.find((item) => item?.id === policyID);
  const duplicate = policies.find(
    (item) => item?.id !== policyID
      && item?.name === policyName
      && item?.description === desiredPolicy.description,
  );
  if (!existing && duplicate?.id) {
    await deleteSecurityPolicy(duplicate.id);
    console.log(`Removed duplicate test policy: ${duplicate.id}`);
  }
  const saved = existing
    ? await updateSecurityPolicy(existing.id, desiredPolicy)
    : await createSecurityPolicy(desiredPolicy);
  const verified = await findSecurityPolicy(policyID);

  assertPolicy(verified, saved?.id);

  console.log(existing ? "Policy updated." : "Policy created.");
  console.log("Verified policy:");
  console.log(JSON.stringify({
    id: verified.id,
    name: verified.name,
    status: verified.status,
    fields: verified.fields,
  }, null, 2));
} catch (error) {
  console.error("Security policy smoke test failed.");
  console.error(formatError(error));
  process.exit(1);
}

async function listSecurityPolicies() {
  const payload = await adminJSON("/api/admin/resources/security-policies");
  return Array.isArray(payload.data) ? payload.data : [];
}

async function findSecurityPolicy(id) {
  const items = await listSecurityPolicies();
  return items.find((item) => item?.id === id);
}

async function createSecurityPolicy(policy) {
  return adminJSON("/api/admin/resources/security-policies", {
    method: "POST",
    body: JSON.stringify(policy),
  });
}

async function updateSecurityPolicy(id, policy) {
  return adminJSON(`/api/admin/resources/security-policies/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(policy),
  });
}

async function deleteSecurityPolicy(id) {
  await adminJSON(`/api/admin/resources/security-policies/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

async function adminJSON(path, options = {}) {
  const resp = await fetch(`${adminBaseURL}${path}`, {
    ...options,
    headers: {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
      ...(options.headers ?? {}),
    },
  });
  const text = await resp.text();
  const payload = text ? JSON.parse(text) : null;
  if (!resp.ok) {
    throw new Error(`${options.method || "GET"} ${path} failed: ${resp.status} ${text}`);
  }
  return payload;
}

function assertPolicy(policy, expectedID) {
  if (!policy) {
    throw new Error(`policy "${policyName}" was not found after save`);
  }
  if (expectedID && policy.id !== expectedID) {
    throw new Error(`expected policy id ${expectedID}, got ${policy.id}`);
  }
  if (policy.status !== "active") {
    throw new Error(`expected active policy, got ${policy.status}`);
  }
  const fields = policy.fields || {};
  if (fields.mask_prompts !== true) {
    throw new Error(`expected mask_prompts=true, got ${JSON.stringify(fields.mask_prompts)}`);
  }
  if (!String(fields.ip_allowlist || "").includes("127.0.0.1/32")) {
    throw new Error(`expected 127.0.0.1/32 in ip_allowlist, got ${JSON.stringify(fields.ip_allowlist)}`);
  }
  if (fields.error_passthrough !== "sanitized") {
    throw new Error(`expected error_passthrough=sanitized, got ${JSON.stringify(fields.error_passthrough)}`);
  }
}

function normalizeAdminBaseURL(value) {
  const trimmed = value.replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed.slice(0, -3) : trimmed;
}

function formatError(error) {
  if (error instanceof Error) return `${error.name}: ${error.message}`;
  return String(error);
}
