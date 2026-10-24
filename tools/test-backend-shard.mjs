import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

const backendDirectory = fileURLToPath(new URL("../backend/", import.meta.url));
const testName = /^(?:Test|Example|Fuzz)[\p{L}\p{N}_]*$/u;
const benchmarkName = /^Benchmark[\p{L}\p{N}_]*$/u;

export function parseTestInventory(output) {
  const names = [];
  for (const line of output.split(/\r?\n/).map((value) => value.trim()).filter(Boolean)) {
    if (testName.test(line)) names.push(line);
    // Benchmarks do not run under the ordinary `go test ./...` command.
    else if (benchmarkName.test(line) || /^ok\s+\S+\s+/.test(line)) continue;
    else throw new Error(`Unexpected Go test inventory output: ${line}`);
  }
  if (names.length === 0) throw new Error("Go test inventory is empty");
  if (new Set(names).size !== names.length) throw new Error("Go test inventory contains duplicate names");
  return names.sort();
}

export function shardInventory(names, index, count) {
  if (!Number.isSafeInteger(count) || count < 1 || !Number.isSafeInteger(index) || index < 0 || index >= count) {
    throw new Error("Shard index must be between zero and shard count minus one");
  }
  if (names.length === 0 || names.some((name) => !testName.test(name)) || new Set(names).size !== names.length) {
    throw new Error("Cannot shard an empty or invalid test inventory");
  }
  const selected = [...names].sort().filter((_, position) => position % count === index);
  if (selected.length === 0) throw new Error("Selected backend test shard is empty");
  return selected;
}

export function exactTestPattern(names) {
  if (names.length === 0) throw new Error("Cannot create an empty Go test pattern");
  const escaped = names.map((name) => name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  return `^(${escaped.join("|")})$`;
}

export function otherBackendPackages(output) {
  const packages = output.trim().split(/\r?\n/);
  if (packages.some((name) => !name || /\s/.test(name)) || new Set(packages).size !== packages.length) {
    throw new Error("Go package inventory is empty or invalid");
  }
  const server = packages.filter((name) => name.endsWith("/internal/server"));
  if (server.length !== 1) throw new Error("Go package inventory must include exactly one server package");
  const others = packages.filter((name) => name !== server[0]);
  if (others.length === 0) throw new Error("Go package inventory contains no other backend packages");
  return others.sort();
}

export function backendShardCommands(names, packages, index, count) {
  const selected = shardInventory(names, index, count);
  const others = otherBackendPackages(packages);
  const commands = [["test", "-count=1", "-timeout", "15m", "./internal/server", "-run", exactTestPattern(selected)]];
  if (index === 0) commands.unshift(["test", "-count=1", "-timeout", "15m", ...others]);
  return { selected, commands };
}

function main(args) {
  if (args.length !== 2 || args.some((value) => !/^\d+$/.test(value))) {
    throw new Error("Usage: node tools/test-backend-shard.mjs <zero-based-index> <count>");
  }
  const [index, count] = args.map(Number);
  const readGo = (command) => execFileSync("go", command, { cwd: backendDirectory, encoding: "utf8", maxBuffer: 16 << 20 });
  const names = parseTestInventory(readGo(["test", "./internal/server", "-list", "."]));
  const packages = readGo(["list", "./..."]);
  const { selected, commands } = backendShardCommands(names, packages, index, count);
  console.log(`Backend shard ${index + 1}/${count}: ${selected.length}/${names.length} top-level tests, examples, and fuzz seeds`);
  for (const command of commands) {
    const result = spawnSync("go", command, { cwd: backendDirectory, stdio: "inherit" });
    if (result.error) throw result.error;
    if (result.status !== 0) process.exit(result.status ?? 1);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(process.argv.slice(2)); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
