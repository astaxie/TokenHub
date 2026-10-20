import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const example = readFileSync(join(root, "deploy/.env.example"), "utf8");
const composeAvailable = spawnSync("docker", ["compose", "version"], {
  encoding: "utf8",
}).status === 0;

function renderCompose(file, contents, overrides = {}) {
  const directory = mkdtempSync(join(tmpdir(), "tokenhub-compose-memory-"));
  try {
    const envFile = join(directory, ".env");
    writeFileSync(envFile, contents);
    // Keep shell settings from overriding the committed defaults under test.
    const env = { ...process.env };
    delete env.TOKENHUB_CONTAINER_MEMORY_LIMIT;
    delete env.GOMEMLIMIT;
    Object.assign(env, overrides);
    const result = spawnSync("docker", [
      "compose", "--env-file", envFile, "-f", join(root, "deploy", file),
      "config", "--format", "json",
    ], { encoding: "utf8", env, cwd: root });
    assert.equal(result.status, 0, `Compose could not render ${file}: ${result.stderr}`);
    return JSON.parse(result.stdout).services["tokenhub-backend"];
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function goMemoryBytes(value) {
  const match = value.match(/^(\d+)(KiB|MiB|GiB)$/);
  assert.ok(match, "the Go memory default must be a finite binary memory size");
  return Number(match[1]) * 1024 ** ({ KiB: 1, MiB: 2, GiB: 3 }[match[2]]);
}

for (const file of ["docker-compose.yml", "docker-compose.postgres.yml"]) {
  test(`${file} bounds bundled application memory with room outside the Go runtime`, {
    skip: composeAvailable ? false : "Docker Compose is unavailable",
  }, () => {
    const fallback = renderCompose(file, "");
    const configured = renderCompose(file, example);
    assert.ok(configured.mem_limit > 0, "the application container must have a hard memory limit");
    assert.equal(configured.memswap_limit, configured.mem_limit, "swap must be disabled by default");
    assert.ok(goMemoryBytes(configured.environment.GOMEMLIMIT) < configured.mem_limit,
      "leave room for SQLite/cgo, Next.js and container page cache");
    assert.equal(fallback.mem_limit, configured.mem_limit);
    assert.equal(fallback.memswap_limit, configured.memswap_limit);
    assert.equal(fallback.environment.GOMEMLIMIT, configured.environment.GOMEMLIMIT);

    const start = readFileSync(join(root, "start.sh"), "utf8");
    assert.ok(start.includes(`"GOMEMLIMIT=${configured.environment.GOMEMLIMIT}"`),
      "the source launcher and bundled container must use the same Go soft target");

    const custom = renderCompose(file, example, {
      TOKENHUB_CONTAINER_MEMORY_LIMIT: "3g",
      GOMEMLIMIT: "1536MiB",
    });
    assert.equal(Number(custom.mem_limit), 3 * 1024 ** 3);
    assert.equal(custom.memswap_limit, custom.mem_limit);
    assert.equal(custom.environment.GOMEMLIMIT, "1536MiB");
  });
}
