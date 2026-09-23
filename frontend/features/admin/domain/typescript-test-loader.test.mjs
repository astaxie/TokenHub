import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

test("TypeScript test imports isolate parent-directory dependencies across invocations", async () => {
  const fixtures = await mkdtemp(join(tmpdir(), "tokenhub-loader-fixture-"));
  try {
    const entries = await Promise.all(["first", "second"].map(async name => {
      const root = join(fixtures, name);
      await mkdir(join(root, "domain"), { recursive: true });
      await mkdir(join(root, "shared"), { recursive: true });
      await writeFile(join(root, "shared", "value.ts"), `export const value = ${JSON.stringify(name)};`);
      const entry = join(root, "domain", "entry.ts");
      await writeFile(entry, 'export { value } from "../shared/value";');
      return pathToFileURL(entry);
    }));
    const results = await Promise.all(entries.map(entry => importTypeScript(entry)));
    assert.deepEqual(results.map(result => result.value), ["first", "second"]);
  } finally {
    await rm(fixtures, { recursive: true, force: true });
  }
});
