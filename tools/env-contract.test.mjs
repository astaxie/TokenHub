import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

import {
  KNOWN_CONSUMERS,
  checkBackendCompleteness,
  checkComposeDefaults,
  checkDocumented,
  checkNoPhantoms,
  extractComposeRequiredVars,
  extractDocumentedVars,
  extractGoEnvVars,
  parseEnvExample,
  stripGoComments,
} from "./env-contract.mjs";

const REPOSITORY_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("Go comment stripping", () => {
  it("blanks line comments but keeps the newline", () => {
    const stripped = stripGoComments('a := 1 // os.Getenv("TOKENHUB_X")\nb := 2\n');
    assert.match(stripped, /^a := 1/);
    assert.equal(stripped.includes("TOKENHUB_X"), false);
    assert.equal(stripped.split("\n").length, 3, "line count is preserved");
  });

  it("blanks block comments while preserving their newlines", () => {
    const source = 'x\n/*\nos.Getenv("TOKENHUB_X")\n*/\ny\n';
    const stripped = stripGoComments(source);
    assert.equal(stripped.includes("TOKENHUB_X"), false);
    assert.equal(stripped.split("\n").length, source.split("\n").length);
  });

  it("keeps string literals, which is where the keys live", () => {
    const stripped = stripGoComments('os.Getenv("TOKENHUB_X")');
    assert.equal(stripped, 'os.Getenv("TOKENHUB_X")');
  });

  it("does not treat a slash inside a string as a comment", () => {
    const source = 'url := "http://example.com" // trailing\nos.Getenv("TOKENHUB_X")';
    const stripped = stripGoComments(source);
    assert.ok(stripped.includes('"http://example.com"'));
    assert.ok(stripped.includes("TOKENHUB_X"));
    assert.equal(stripped.includes("trailing"), false);
  });

  it("does not let an escaped quote close a string", () => {
    const source = 'a := "he said \\" // not a comment"\nos.Getenv("TOKENHUB_X")';
    const stripped = stripGoComments(source);
    assert.ok(stripped.includes("TOKENHUB_X"));
    assert.ok(stripped.includes("not a comment"));
  });

  it("handles rune literals containing a quote or slash", () => {
    const source = "c := '\\''\nd := '/'\n";
    assert.equal(stripGoComments(source), source);
  });

  it("leaves raw strings intact", () => {
    const source = "q := `line // not a comment`\n";
    assert.equal(stripGoComments(source), source);
  });
});

describe("Go env extraction", () => {
  it("matches the shared helpers and direct os.Getenv calls", () => {
    const source = `
      a := getenv("TOKENHUB_ENV", "dev")
      b := getenvInt("TOKENHUB_DB_MAX_OPEN_CONNS", 25)
      c := getenvBool("TOKENHUB_METRICS_ENABLED", false)
      d := getenvList("TOKENHUB_CORS_ALLOWED_ORIGINS")
      e := os.Getenv("TOKENHUB_DATABASE_URL")
    `;
    assert.deepEqual(
      [...extractGoEnvVars(source)].sort(),
      [
        "TOKENHUB_CORS_ALLOWED_ORIGINS",
        "TOKENHUB_DATABASE_URL",
        "TOKENHUB_DB_MAX_OPEN_CONNS",
        "TOKENHUB_ENV",
        "TOKENHUB_METRICS_ENABLED",
      ],
    );
  });

  it("matches a call split across lines", () => {
    const source = 'getenvInt(\n  "TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS",\n  3600,\n)';
    assert.deepEqual([...extractGoEnvVars(source)], ["TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS"]);
  });

  it("ignores a commented-out call", () => {
    assert.equal(extractGoEnvVars('// os.Getenv("TOKENHUB_REMOVED")').size, 0);
    assert.equal(extractGoEnvVars('/* getenv("TOKENHUB_REMOVED", "") */').size, 0);
  });

  it("ignores a bare mention that is not a call site", () => {
    // The regression this guards: a bare regex over file text picked up the fragment
    // TOKENHUB_DB_ from DSN concatenation and doc comments naming variables.
    const source = `
      // resolveDatabaseURL builds a DSN from TOKENHUB_DB_HOST and friends.
      prefix := "TOKENHUB_DB_"
      msg := "set TOKENHUB_SECRET_KEY before starting"
    `;
    assert.equal(extractGoEnvVars(source).size, 0);
  });

  it("accepts a backtick-quoted key", () => {
    assert.deepEqual([...extractGoEnvVars("os.Getenv(`TOKENHUB_ENV`)")], ["TOKENHUB_ENV"]);
  });

  it("tolerates whitespace around the argument", () => {
    assert.deepEqual([...extractGoEnvVars('os.Getenv (  "TOKENHUB_ENV" )')], ["TOKENHUB_ENV"]);
  });
});

describe("env example parsing", () => {
  it("separates active from commented assignments", () => {
    const parsed = parseEnvExample(
      ["TOKENHUB_ENV=prod", "# TOKENHUB_DATABASE_URL=sqlite://x", "TOKENHUB_EMPTY="].join("\n"),
    );
    assert.deepEqual([...parsed.active].sort(), ["TOKENHUB_EMPTY", "TOKENHUB_ENV"]);
    assert.deepEqual([...parsed.commented], ["TOKENHUB_DATABASE_URL"]);
  });

  it("ignores prose mentions that are not assignments", () => {
    const parsed = parseEnvExample("# Remember to set TOKENHUB_SECRET_KEY somewhere\n");
    assert.equal(parsed.active.size, 0);
    assert.equal(parsed.commented.size, 0);
  });
});

describe("compose interpolation", () => {
  it("flags only interpolations without a default", () => {
    const source = [
      "      A: ${TOKENHUB_PUBLIC_BASE_URL}",
      "      B: ${TOKENHUB_LOG_LEVEL:-info}",
      '      - "${TOKENHUB_GATEWAY_PORT:-8080}:8080"',
      "      C: ${TOKENHUB_REQUIRED:?must be set}",
    ].join("\n");
    assert.deepEqual([...extractComposeRequiredVars(source)], ["TOKENHUB_PUBLIC_BASE_URL"]);
  });

  it("handles a nested default", () => {
    const source = "A: ${TOKENHUB_DB_PASSWORD:-${POSTGRES_PASSWORD:-change-me}}";
    assert.equal(extractComposeRequiredVars(source).size, 0);
  });

  it("flags a bare variable used as another variable's default", () => {
    // ${A:-${B}} still resolves to an empty string when both are unset, so the
    // default body is scanned rather than skipped wholesale.
    assert.deepEqual([...extractComposeRequiredVars("A: ${TOKENHUB_CORS:-${TOKENHUB_BASE}}")], [
      "TOKENHUB_BASE",
    ]);
  });

  it("ignores a $$-escaped literal dollar sign", () => {
    // $$ is Compose's escape for a literal $, so $${VAR} is not an interpolation and
    // the variable is never read. Reporting it would be a false positive.
    assert.equal(extractComposeRequiredVars("A: $${TOKENHUB_ESCAPED}").size, 0);
    assert.equal(extractComposeRequiredVars("A: $$TOKENHUB_ESCAPED").size, 0);
  });

  it("flags the unbraced form, which also has no default", () => {
    assert.deepEqual([...extractComposeRequiredVars("A: $TOKENHUB_UNBRACED")], [
      "TOKENHUB_UNBRACED",
    ]);
  });

  it("requires a token boundary after an unbraced name", () => {
    // Compose reads $TOKENHUB_PORTsuffix as one variable named TOKENHUB_PORTsuffix.
    // Matching the uppercase prefix would demand a TOKENHUB_PORT that nothing uses.
    assert.equal(extractComposeRequiredVars("VALUE=$TOKENHUB_PORTsuffix").size, 0);
    assert.deepEqual([...extractComposeRequiredVars("VALUE=$TOKENHUB_PORT/x")], [
      "TOKENHUB_PORT",
    ]);
  });

  it("does not scan an error directive's message body", () => {
    // The :? branch only builds a message; the variable named inside it can never
    // become a Compose value.
    assert.equal(
      extractComposeRequiredVars("A: ${TOKENHUB_PRIMARY:?missing $TOKENHUB_DIAGNOSTIC}").size,
      0,
    );
  });

  it("does not treat the error forms as silently-empty", () => {
    // ${VAR:?msg} and ${VAR?msg} abort the deployment with a message rather than
    // resolving to an empty string, so they are a different failure mode.
    assert.equal(extractComposeRequiredVars("A: ${TOKENHUB_X:?set it}").size, 0);
    assert.equal(extractComposeRequiredVars("A: ${TOKENHUB_X?set it}").size, 0);
  });

  it("recovers after an unterminated interpolation instead of hanging", () => {
    assert.equal(extractComposeRequiredVars("A: ${TOKENHUB_X").size, 0);
  });
});

describe("documentation extraction", () => {
  it("finds variables inside markdown table cells", () => {
    const source = "| `TOKENHUB_LOG_LEVEL` | `info` | Log level |\n";
    assert.ok(extractDocumentedVars(source).has("TOKENHUB_LOG_LEVEL"));
  });
});

describe("rules", () => {
  it("rule 1 reports undocumented backend variables", () => {
    const failures = checkDocumented(
      new Set(["TOKENHUB_ENV", "TOKENHUB_DB_HOST"]),
      new Set(["TOKENHUB_ENV"]),
    );
    assert.equal(failures.length, 1);
    assert.equal(failures[0].name, "TOKENHUB_DB_HOST");
    assert.equal(failures[0].rule, "documented");
  });

  it("rule 2 reports a phantom knob, including a commented one", () => {
    const failures = checkNoPhantoms(
      "backend/.env.example",
      { active: new Set(["TOKENHUB_GHOST"]), commented: new Set(["TOKENHUB_ALSO_GHOST"]) },
      new Set(),
    );
    assert.deepEqual(
      failures.map((failure) => failure.name).sort(),
      ["TOKENHUB_ALSO_GHOST", "TOKENHUB_GHOST"],
    );
  });

  it("rule 2 accepts a variable owned by a declared non-Go consumer", () => {
    const failures = checkNoPhantoms(
      "deploy/.env.example",
      { active: new Set(["TOKENHUB_IMAGE_TAG"]), commented: new Set() },
      new Set(),
    );
    assert.deepEqual(failures, []);
  });

  it("rule 3 requires an active assignment, not a commented one", () => {
    const required = new Set(["TOKENHUB_PUBLIC_BASE_URL"]);
    assert.deepEqual(
      checkComposeDefaults(required, {
        active: new Set(["TOKENHUB_PUBLIC_BASE_URL"]),
        commented: new Set(),
      }),
      [],
    );

    const failures = checkComposeDefaults(required, {
      active: new Set(),
      commented: new Set(["TOKENHUB_PUBLIC_BASE_URL"]),
    });
    assert.equal(failures.length, 1);
    assert.match(failures[0].detail, /empty string/);
  });

  it("rule 4 reports a backend variable missing from the developer reference", () => {
    const failures = checkBackendCompleteness(new Set(["TOKENHUB_METRICS_TOKEN"]), {
      active: new Set(),
      commented: new Set(),
    });
    assert.equal(failures.length, 1);
    assert.equal(failures[0].rule, "backend-completeness");
  });

  it("rule 4 accepts a variable that is present but commented out", () => {
    const failures = checkBackendCompleteness(new Set(["TOKENHUB_DATABASE_URL"]), {
      active: new Set(),
      commented: new Set(["TOKENHUB_DATABASE_URL"]),
    });
    assert.deepEqual(failures, []);
  });
});

describe("KNOWN_CONSUMERS", () => {
  // An exemption is only reachable through the file it is keyed under, so a key that
  // file no longer mentions can never fire. Without this check those entries accumulate
  // silently and read like a record of variables the deployment still offers.
  for (const [fileName, consumers] of Object.entries(KNOWN_CONSUMERS)) {
    it(`only exempts variables ${fileName} actually offers`, () => {
      const parsed = parseEnvExample(readFileSync(join(REPOSITORY_ROOT, fileName), "utf8"));
      const mentioned = new Set([...parsed.active, ...parsed.commented]);
      const dead = [...consumers.keys()].filter((name) => !mentioned.has(name)).sort();
      assert.deepEqual(
        dead,
        [],
        `${fileName} no longer offers ${dead.join(", ")}, so the entries exempt nothing`,
      );
    });
  }
});
