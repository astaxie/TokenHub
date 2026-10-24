import { readFile, writeFile } from "node:fs/promises";
import { magpieCatalogEntry, magpieProviderPresets } from "./magpie-provider-presets.mjs";

const file = new URL("../data/provider-catalog.json", import.meta.url);
const catalog = JSON.parse(await readFile(file, "utf8"));
for (const preset of magpieProviderPresets) catalog.providers[preset.id] = magpieCatalogEntry(preset);
catalog.updated_at = "2026-10-24T00:00:00Z";
await writeFile(file, JSON.stringify(catalog) + "\n");
process.stdout.write(`Synchronized ${magpieProviderPresets.length} pinned Magpie API-key presets.\n`);
