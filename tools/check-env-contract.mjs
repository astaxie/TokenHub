#!/usr/bin/env node
//
// Enforces the environment variable contract described in tools/env-contract.mjs:
// every backend variable is documented, every offered variable does something, every
// Compose interpolation without a default has a value to find, and
// backend/.env.example stays complete.
//
//   node tools/check-env-contract.mjs

import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import {
  checkBackendCompleteness,
  checkComposeDefaults,
  checkDocumented,
  checkNoPhantoms,
  extractComposeRequiredVars,
  extractDocumentedVars,
  extractGoEnvVars,
  parseEnvExample,
} from "./env-contract.mjs";

const GO_ROOTS = ["backend/internal", "backend/cmd"];
const ENGLISH_DOCS = ["docs/deployment.md", "docs/postgresql-setup.md"];
const COMPOSE_DIR = "deploy";

function repositoryRoot() {
  return execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
}

function read(root, relative) {
  return readFileSync(join(root, relative), "utf8");
}

function collectGoFiles(directory, output = []) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      collectGoFiles(path, output);
    } else if (entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) {
      // Test files are excluded on purpose: they set throwaway credentials such as
      // TOKENHUB_LIVE_ARK_API_KEY that are not part of the operator-facing contract.
      output.push(path);
    }
  }
  return output;
}

function main() {
  const root = repositoryRoot();

  const goVars = new Set();
  for (const goRoot of GO_ROOTS) {
    for (const file of collectGoFiles(join(root, goRoot))) {
      for (const name of extractGoEnvVars(readFileSync(file, "utf8"))) {
        goVars.add(name);
      }
    }
  }

  const documentedVars = new Set();
  for (const doc of ENGLISH_DOCS) {
    for (const name of extractDocumentedVars(read(root, doc))) {
      documentedVars.add(name);
    }
  }

  const backendEnv = parseEnvExample(read(root, "backend/.env.example"));
  const deployEnv = parseEnvExample(read(root, "deploy/.env.example"));

  const composeRequired = new Set();
  for (const entry of readdirSync(join(root, COMPOSE_DIR))) {
    if (!/^docker-compose.*\.ya?ml$/.test(entry)) continue;
    for (const name of extractComposeRequiredVars(read(root, join(COMPOSE_DIR, entry)))) {
      composeRequired.add(name);
    }
  }

  const failures = [
    ...checkDocumented(goVars, documentedVars),
    ...checkNoPhantoms("backend/.env.example", backendEnv, goVars),
    ...checkNoPhantoms("deploy/.env.example", deployEnv, goVars),
    ...checkComposeDefaults(composeRequired, deployEnv),
    ...checkBackendCompleteness(goVars, backendEnv),
  ];

  if (failures.length > 0) {
    console.error("Environment variable contract check failed:\n");
    for (const failure of failures) {
      console.error(`- [${failure.rule}] ${failure.detail}`);
    }
    console.error(
      [
        "",
        "A new backend variable needs an entry in backend/.env.example and a row in the",
        "English deployment documentation. A variable owned by Compose or the frontend",
        "instead of Go belongs in KNOWN_CONSUMERS in tools/env-contract.mjs.",
      ].join("\n"),
    );
    return 1;
  }

  console.log(
    `Environment contract check passed (${goVars.size} backend variables, ` +
      `${composeRequired.size} required Compose interpolations).`,
  );
  return 0;
}

try {
  process.exitCode = main();
} catch (error) {
  // Fail closed with a diagnostic. A missing git, a missing .env.example or an
  // unreadable Compose file are all configuration problems, and a Node traceback
  // hides which one it was.
  console.error(`Environment contract check could not run: ${error.message}`);
  process.exitCode = 1;
}
