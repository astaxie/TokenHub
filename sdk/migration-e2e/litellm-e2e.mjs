import { execFileSync } from "node:child_process";
import { mkdtempSync, existsSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import fetch from "node-fetch";

const TOKENHUB_API = process.env.TOKENHUB_API || "http://localhost:8080";
const TOKENHUB_TOKEN = process.env.TOKENHUB_ADMIN_TOKEN || "admin-token";
const LITELLM_API = process.env.LITELLM_API || "http://localhost:4000";
const LITELLM_KEY = process.env.MIGRATION_E2E_LITELLM_KEY || "sk-test-key";
const MIGRATE_BIN = process.env.TOKENHUB_MIGRATE_BIN || "tokenhub-migrate";
const FIXTURE = resolve("fixtures/proxy_config.yaml");

let passed = 0;
let failed = 0;
const report = { checks: [] };

function record(ok, message, detail = "") {
  report.checks.push({ ok, message, detail });
  if (ok) {
    passed += 1;
    console.log(`  PASS: ${message}`);
  } else {
    failed += 1;
    console.error(`  FAIL: ${message}`);
    if (detail) console.error(`    ${detail}`);
  }
}

function run(bin, args, options = {}) {
  try {
    const output = execFileSync(bin, args, {
      encoding: "utf-8",
      stdio: options.silent ? ["ignore", "pipe", "pipe"] : ["ignore", "pipe", "pipe"],
      env: { ...process.env, ...options.env },
    });
    return { success: true, stdout: output, stderr: "" };
  } catch (error) {
    return {
      success: false,
      stdout: error.stdout?.toString() || "",
      stderr: error.stderr?.toString() || error.message || "",
    };
  }
}

async function requestJSON(url, token, options = {}) {
  const { headers = {}, ...requestOptions } = options;
  const response = await fetch(url, {
    ...requestOptions,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...headers,
    },
  });
  const body = await response.json().catch(() => ({}));
  return { status: response.status, body };
}

function fetchLiteLLM(path, options = {}) {
  return requestJSON(`${LITELLM_API}${path}`, LITELLM_KEY, options);
}

function fetchTokenHub(path, options = {}) {
  return requestJSON(`${TOKENHUB_API}${path}`, TOKENHUB_TOKEN, options);
}

const delay = (milliseconds) => new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));

async function waitForStatus(request, expectedStatus = 200, attempts = 90) {
  let latest = { status: 0, body: { error: "service did not respond" } };
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      latest = await request();
      if (latest.status === expectedStatus) return latest;
    } catch (error) {
      latest = { status: 0, body: { error: String(error) } };
    }
    await delay(1000);
  }
  return latest;
}

async function ensureEmailNotificationChannel() {
  const listed = await fetchTokenHub("/api/admin/resources/notification-channels");
  if (listed.status !== 200) return listed;
  const existing = (listed.body?.data || []).find(
    (channel) => channel.status === "active" && channel.fields?.type === "email",
  );
  if (existing) return { status: 200, body: existing };

  return fetchTokenHub("/api/admin/resources/notification-channels", {
    method: "POST",
    body: JSON.stringify({
      name: "Migration E2E SMTP",
      status: "active",
      fields: {
        type: "email",
        smtp_host: "mailpit",
        smtp_port: 1025,
        smtp_from: "tokenhub@example.com",
      },
    }),
  });
}

async function main() {
  console.log("TokenHub Migration E2E: LiteLLM\n");

  if (!existsSync(FIXTURE)) {
    record(false, "fixture exists", FIXTURE);
    writeFileSync("report.json", JSON.stringify(report, null, 2));
    process.exit(1);
  }

  const workDir = mkdtempSync(join(tmpdir(), "tokenhub-migration-e2e-"));
  process.once("exit", () => {
    rmSync(workDir, { recursive: true, force: true });
  });
  const bundlePath = join(workDir, "bundle.json");
  const checkpointPath = join(workDir, "checkpoint.json");
  const newKeysPath = join(workDir, "new-keys.json");
  // Every migration command below targets the remote TokenHub instance so
  // the apply/verify/re-apply/rollback cycle exercises the real Admin API.
  const remoteEnv = {
    TOKENHUB_API,
    TOKENHUB_ADMIN_TOKEN: TOKENHUB_TOKEN,
    OPENAI_API_KEY: process.env.OPENAI_API_KEY || "sk-placeholder",
  };

  console.log("1. Verify LiteLLM connectivity");
  const health = await waitForStatus(() => fetchLiteLLM("/health"));
  record(health.status === 200, "LiteLLM health check", JSON.stringify(health.body));
  const tokenHubHealth = await waitForStatus(() => fetchTokenHub("/healthz"));
  record(tokenHubHealth.status === 200, "TokenHub health check", JSON.stringify(tokenHubHealth.body));

  console.log("\n2. Send chat completion through LiteLLM");
  const chat = await fetchLiteLLM("/v1/chat/completions", {
    method: "POST",
    body: JSON.stringify({
      model: "gpt-4o-mini",
      messages: [{ role: "user", content: "Hello, migration test!" }],
    }),
  });
  record(chat.status === 200, "LiteLLM chat completion succeeds", JSON.stringify(chat.body));
  record(Boolean(chat.body?.choices?.[0]?.message?.content), "LiteLLM response has content");

  console.log("\n3. Configure TokenHub SMTP delivery for user import");
  const emailChannel = await ensureEmailNotificationChannel();
  record(
    emailChannel.status === 200 || emailChannel.status === 201,
    "TokenHub email notification channel is ready",
    JSON.stringify(emailChannel.body),
  );

  console.log("\n4. Extract bundle from LiteLLM fixture");
  const extract = run(MIGRATE_BIN, ["extract", "litellm", "--from", FIXTURE, "--out", bundlePath], { silent: true });
  record(extract.success, "Extract succeeds", extract.stderr || extract.stdout);
  record(existsSync(bundlePath), "Bundle file created", bundlePath);

  console.log("\n5. Plan the migration against TokenHub");
  const plan = run(MIGRATE_BIN, ["plan", "--bundle", bundlePath], { silent: true, env: remoteEnv });
  record(plan.success, "Plan succeeds", plan.stderr || plan.stdout);
  record(plan.stdout.includes("Created"), "Plan prints created count", plan.stdout);

  console.log("\n6. Apply the bundle to TokenHub");
  const apply = run(
    MIGRATE_BIN,
    ["apply", "--bundle", bundlePath, "--checkpoint-out", checkpointPath, "--new-keys-out", newKeysPath],
    { silent: true, env: remoteEnv },
  );
  record(apply.success, "Apply succeeds", apply.stderr || apply.stdout);
  record(apply.stdout.includes("Apply complete"), "Apply reports completion", apply.stdout);
  record(existsSync(checkpointPath), "Apply persists rollback checkpoint", checkpointPath);
  record(existsSync(newKeysPath), "Apply persists one-time API key secrets", newKeysPath);

  console.log("\n7. Verify the applied state on TokenHub");
  const verify = run(MIGRATE_BIN, ["verify", "--bundle", bundlePath], { silent: true, env: remoteEnv });
  record(verify.success, "Verify passes against applied state", verify.stderr || verify.stdout);

  console.log("\n8. Re-apply proves idempotency");
  const reapply = run(
    MIGRATE_BIN,
    ["apply", "--bundle", bundlePath, "--checkpoint-out", join(workDir, "checkpoint-reapply.json")],
    { silent: true, env: remoteEnv },
  );
  record(reapply.success, "Re-apply succeeds", reapply.stderr || reapply.stdout);
  record(/Created:\s*0/.test(reapply.stdout), "Re-apply creates nothing new", reapply.stdout);
  record(/Updated:\s*0/.test(reapply.stdout), "Re-apply updates nothing", reapply.stdout);

  console.log("\n9. Rollback from the real checkpoint");
  const rollback = run(MIGRATE_BIN, ["rollback", "--checkpoint", checkpointPath], { silent: true, env: remoteEnv });
  record(rollback.success, "Rollback command succeeds", rollback.stderr || rollback.stdout);
  record(/Rollback:\s*[1-9]/.test(rollback.stdout), "Rollback reverts applied changes", rollback.stdout);
  const usersAfterRollback = await fetchTokenHub("/api/admin/users");
  const importedUserRemains = (usersAfterRollback.body?.data || []).some(
    (user) => user.email === "alice@example.com",
  );
  record(usersAfterRollback.status === 200, "Users can be inspected after rollback", JSON.stringify(usersAfterRollback.body));
  record(!importedUserRemains, "Rollback removes the imported user", JSON.stringify(usersAfterRollback.body));

  console.log("\n10. Verify detects the rolled-back state");
  const verifyAfterRollback = run(MIGRATE_BIN, ["verify", "--bundle", bundlePath], { silent: true, env: remoteEnv });
  record(!verifyAfterRollback.success, "Verify fails after rollback as expected", verifyAfterRollback.stderr || verifyAfterRollback.stdout);

  console.log("\n---");
  console.log(`Results: ${passed} passed, ${failed} failed`);
  writeFileSync("report.json", JSON.stringify(report, null, 2));
  process.exit(failed > 0 ? 1 : 0);
}

main().catch((error) => {
  console.error("E2E harness error:", error);
  writeFileSync("report.json", JSON.stringify({ fatal: String(error) }, null, 2));
  process.exit(1);
});
