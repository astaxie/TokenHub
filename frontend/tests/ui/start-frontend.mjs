import { cp, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import configuration from "./config.cjs";

if (process.env.TOKENHUB_UI_RUN !== "1") throw new Error("Start the isolated UI server through npm run test:ui.");
const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const output = path.join(frontend, configuration.distDirectory);
// Next may infer a parent tracing root when a workstation has other lockfiles.
const manifest = JSON.parse(await readFile(path.join(output, "required-server-files.json"), "utf8"));
const relativeApp = path.relative(manifest.config.outputFileTracingRoot || frontend, frontend);
if (relativeApp.startsWith("..") || path.isAbsolute(relativeApp)) throw new Error("Unexpected standalone tracing root");
const standalone = path.join(output, "standalone", relativeApp);
await cp(path.join(output, "static"), path.join(standalone, configuration.distDirectory, "static"), { recursive: true });
await cp(path.join(frontend, "public"), path.join(standalone, "public"), { recursive: true });
process.env.HOSTNAME = "127.0.0.1";
process.env.PORT = String(configuration.frontendPort);
process.env.TOKENHUB_API_BASE_URL = configuration.apiOrigin;
await import(pathToFileURL(path.join(standalone, "server.js")).href);
