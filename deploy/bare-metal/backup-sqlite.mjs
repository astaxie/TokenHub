#!/usr/bin/env node
// Scheduled SQLite backup for the bare-metal deployment.
//
// Backups are taken through the running backend's admin API, which uses SQLite's
// online backup interface. Copying the database file from the outside while the
// service is running would produce a torn copy.
//
// The application stores an expiry on each backup but never deletes anything on
// its own, so retention is driven from here: list the backups and delete the
// expired ones through the API. Deleting the files directly would leave the
// database rows behind, and the console would list backups that no longer exist.
//
// Configuration comes from /etc/tokenhub/backend.env, which systemd loads into
// this process's environment.

const adminToken = process.env.TOKENHUB_ADMIN_TOKEN?.trim();
if (!adminToken) {
  console.error("TOKENHUB_ADMIN_TOKEN is not set; cannot authenticate to the backup API");
  process.exit(1);
}

const httpAddr = process.env.TOKENHUB_HTTP_ADDR?.trim() || ":8080";
const port = httpAddr.slice(httpAddr.lastIndexOf(":") + 1) || "8080";
const baseURL = `http://127.0.0.1:${port}`;

const expireDays = Number.parseInt(process.env.TOKENHUB_BACKUP_EXPIRE_DAYS ?? "14", 10);
if (!Number.isFinite(expireDays) || expireDays < 0) {
  console.error(`TOKENHUB_BACKUP_EXPIRE_DAYS must be a non-negative integer, got ${process.env.TOKENHUB_BACKUP_EXPIRE_DAYS}`);
  process.exit(1);
}

// A backup of a large database holds the backend's single SQLite connection for
// its whole duration, so allow generous time but never hang the timer forever.
const requestTimeoutMs = Number.parseInt(process.env.TOKENHUB_BACKUP_TIMEOUT_MS ?? "1800000", 10);

async function api(method, path, body) {
  const response = await fetch(`${baseURL}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${adminToken}`,
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(requestTimeoutMs),
  });
  const text = await response.text();
  if (!response.ok) {
    // 403 here usually means the static admin token no longer maps to an admin:
    // it is attributed to the earliest-created admin user, so demoting that user
    // breaks this timer.
    throw new Error(`${method} ${path} failed: HTTP ${response.status} ${text.slice(0, 500)}`);
  }
  return text ? JSON.parse(text) : null;
}

function backupIsExpired(backup, now) {
  if (!backup?.expires_at) return false;
  const expiresAt = Date.parse(backup.expires_at);
  return Number.isFinite(expiresAt) && expiresAt <= now;
}

async function main() {
  const created = await api("POST", "/api/admin/sqlite/backups", { expire_days: expireDays });
  const sizeMB = created?.size_bytes ? (created.size_bytes / 1024 / 1024).toFixed(1) : "?";
  console.log(`Created backup ${created?.id ?? "?"} (${created?.file_name ?? "?"}, ${sizeMB} MB, status=${created?.status ?? "?"})`);

  // CreateSQLiteBackup sets "creating" while it runs and settles on "ready" or
  // "failed"; anything else means the API changed under us.
  if (created?.status !== "ready") {
    throw new Error(`Backup finished with status "${created?.status ?? "unknown"}": ${created?.error ?? "no detail"}`);
  }

  const listed = await api("GET", "/api/admin/sqlite/backups");
  // Treating an unexpected shape as "no backups" would report a successful
  // retention pass that never actually ran.
  if (!Array.isArray(listed?.data)) {
    throw new Error("Backup list API did not return a data array; retention was not applied");
  }
  const backups = listed.data;
  const now = Date.now();
  const expired = backups.filter((backup) => backup.id !== created?.id && backupIsExpired(backup, now));

  let deleted = 0;
  const failures = [];
  for (const backup of expired) {
    try {
      await api("DELETE", `/api/admin/sqlite/backups/${encodeURIComponent(backup.id)}`);
      deleted += 1;
    } catch (error) {
      failures.push(`${backup.id}: ${error.message}`);
    }
  }

  console.log(`Retention: ${backups.length} backups tracked, ${deleted} expired backups deleted`);
  if (failures.length > 0) {
    throw new Error(`Failed to delete ${failures.length} expired backups:\n  ${failures.join("\n  ")}`);
  }
}

main().catch((error) => {
  console.error(error.message ?? error);
  process.exit(1);
});
