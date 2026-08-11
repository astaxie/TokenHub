// Environment variable contract rules for TokenHub.
//
// AGENTS.md requires environment variable additions to stay synchronized across the
// .env.example files, Compose, start.sh and the deployment documentation. This module
// holds the extraction and comparison logic; file I/O lives in check-env-contract.mjs.
//
// Scope, stated honestly: these are key-presence rules. They do not compare default
// VALUES — dev and deploy defaults differ on purpose, and a default drifting from 300
// to 600 is invisible here. Nor does any rule force a new variable into start.sh or
// Compose; those surfaces are deliberately selected rather than universal.

const VAR = "TOKENHUB_[A-Z0-9_]+";

/**
 * Variables that appear in an .env.example without being read by Go, because a
 * non-Go consumer owns them. Anything not listed here and not read by Go is a
 * phantom knob: an operator can set it and nothing happens.
 *
 * An entry only ever exempts a variable an .env.example already offers, so a key that
 * no .env.example mentions exempts nothing and is dead bookkeeping. The test suite
 * enforces that, which is how the two entries that outlived their .env.example lines
 * were found.
 */
export const KNOWN_CONSUMERS = {
  "deploy/.env.example": new Map([
    ["TOKENHUB_IMAGE_TAG", "docker-compose image tag"],
    ["TOKENHUB_BACKEND_PORT", "docker-compose port publishing"],
    ["TOKENHUB_FRONTEND_PORT", "docker-compose port publishing"],
    ["TOKENHUB_BACKEND_REPLICAS", "docker-compose scale (remote-postgres)"],
    ["TOKENHUB_FRONTEND_REPLICAS", "docker-compose scale (remote-postgres)"],
    ["TOKENHUB_STOP_GRACE_PERIOD", "docker-compose stop_grace_period"],
    ["TOKENHUB_API_BASE_URL", "Next.js frontend runtime configuration"],
    ["TOKENHUB_NGINX_CLIENT_MAX_BODY_SIZE", "nginx load-balancer client_max_body_size (remote-postgres)"],
  ]),
  "backend/.env.example": new Map(),
};

/**
 * Rewrites Go comments to spaces so a commented-out call cannot register as a real
 * one, while leaving string literals intact because that is where the keys live.
 *
 * The accepted grammar, spelled out because the plan committed to writing it down:
 *
 *   code          `//` opens a line comment, `/*` opens a block comment, `"` opens an
 *                 interpreted string, a backtick opens a raw string, `'` opens a rune.
 *   interpreted   a backslash escapes the next character; `"` closes.
 *   rune          a backslash escapes the next character; `'` closes.
 *   raw           no escapes exist; the next backtick closes.
 *   line comment  closes at the newline, which is preserved.
 *   block comment closes at the first `*``/`; newlines inside are preserved so that
 *                 line numbers survive.
 *
 * Known and accepted limitation: a raw string whose contents happen to spell a real
 * call, such as a SQL fragment containing `os.Getenv("TOKENHUB_X")`, is still matched.
 * Blanking raw strings instead would break the legal `os.Getenv(`TOKENHUB_X`)` form,
 * which is the more plausible of the two.
 */
export function stripGoComments(source) {
  let out = "";
  let state = "code";
  for (let index = 0; index < source.length; index += 1) {
    const char = source[index];
    const next = source[index + 1];

    switch (state) {
      case "code":
        if (char === "/" && next === "/") {
          state = "line";
          out += "  ";
          index += 1;
        } else if (char === "/" && next === "*") {
          state = "block";
          out += "  ";
          index += 1;
        } else {
          if (char === '"') state = "string";
          else if (char === "`") state = "raw";
          else if (char === "'") state = "rune";
          out += char;
        }
        break;

      case "line":
        // Keep the newline so line numbers and statement boundaries survive.
        if (char === "\n") {
          state = "code";
          out += char;
        } else {
          out += " ";
        }
        break;

      case "block":
        if (char === "*" && next === "/") {
          state = "code";
          out += "  ";
          index += 1;
        } else {
          out += char === "\n" ? char : " ";
        }
        break;

      case "string":
      case "rune": {
        const closer = state === "string" ? '"' : "'";
        out += char;
        if (char === "\\") {
          // Copy the escaped character verbatim so an escaped quote cannot close it.
          if (next !== undefined) {
            out += next;
            index += 1;
          }
        } else if (char === closer) {
          state = "code";
        }
        break;
      }

      case "raw":
        out += char;
        if (char === "`") state = "code";
        break;
    }
  }
  return out;
}

/**
 * Extracts variables from Go call sites rather than from raw file text. A bare regex
 * over the source yields false positives: test-only credentials such as
 * TOKENHUB_LIVE_ARK_API_KEY, and the bare fragment `TOKENHUB_DB_` produced by DSN
 * string concatenation.
 *
 * Both the shared helpers in internal/server/config.go and the separate local helper
 * in cmd/tokenhub/main.go are matched, along with direct os.Getenv calls.
 *
 * Known and accepted limitations, both of which fail by missing a variable rather than
 * inventing one, and neither of which occurs in this repository:
 *
 *   - A comment between the selector tokens, `os./* x *\/Getenv("…")`, is legal Go but
 *     breaks the required adjacency of `os.Getenv`.
 *   - A non-literal key, `os.Getenv(someConst)`, cannot be resolved without evaluating
 *     the program.
 *
 * Handling either one means tokenizing Go properly, at which point the extraction
 * belongs in a go/parser helper rather than in JavaScript.
 */
export function extractGoEnvVars(source) {
  const code = stripGoComments(source);
  const pattern = new RegExp(
    String.raw`(?:os\.Getenv|getenv[A-Za-z]*)\s*\(\s*["\x60](` + VAR + String.raw`)["\x60]`,
    "g",
  );
  const found = new Set();
  for (const match of code.matchAll(pattern)) {
    found.add(match[1]);
  }
  return found;
}

/**
 * Parses an .env file into active and commented assignments. A commented entry still
 * counts as a mention for the phantom-variable rule — documenting a knob that does
 * nothing is misleading whether or not the line is commented out.
 */
export function parseEnvExample(source) {
  const active = new Set();
  const commented = new Set();
  const pattern = new RegExp(String.raw`^(\s*#\s*)?(` + VAR + String.raw`)\s*=`);
  for (const line of source.split(/\r?\n/)) {
    const match = line.match(pattern);
    if (!match) continue;
    (match[1] ? commented : active).add(match[2]);
  }
  return { active, commented };
}

/**
 * Finds Compose interpolations that resolve to an empty string when the variable is
 * unset — `${VAR}` and the unbraced `$VAR`. Those fail silently, which is the whole
 * point of this rule. Forms carrying a default (`${VAR:-x}`, `${VAR-x}`) are safe by
 * construction, and the error forms (`${VAR:?msg}`, `${VAR?msg}`) fail loudly rather
 * than silently, so neither is reported here.
 *
 * Written as a scanner rather than a regex because two Compose details defeat the
 * obvious pattern: `$$` escapes a literal dollar sign, so `$${VAR}` is not an
 * interpolation at all, and a default may itself contain a nested interpolation, as in
 * `${TOKENHUB_DB_PASSWORD:-${POSTGRES_PASSWORD:-change-me}}`, which needs brace
 * matching to skip correctly.
 */
export function extractComposeRequiredVars(source) {
  const required = new Set();
  const namePattern = new RegExp(String.raw`^(` + VAR + String.raw`)`);

  for (let index = 0; index < source.length; index += 1) {
    if (source[index] !== "$") continue;

    if (source[index + 1] === "$") {
      index += 1; // Consume the escape so the second $ is not read as a sigil.
      continue;
    }

    if (source[index + 1] === "{") {
      let depth = 1;
      let cursor = index + 2;
      while (cursor < source.length && depth > 0) {
        if (source[cursor] === "{") depth += 1;
        else if (source[cursor] === "}") {
          depth -= 1;
          if (depth === 0) break;
        }
        cursor += 1;
      }
      if (depth !== 0) {
        // Unterminated, so the file is malformed and Compose will reject it anyway.
        // Inventing a finding from input this checker cannot parse would be noise.
        break;
      }
      const body = source.slice(index + 2, cursor);
      const name = body.match(namePattern);
      if (name) {
        const rest = body.slice(name[1].length);
        if (rest === "") {
          // No default and no error directive, so an unset variable becomes empty.
          required.add(name[1]);
        } else if (/^:?-/.test(rest)) {
          // A default may itself interpolate, and `${A:-${B}}` still resolves to an
          // empty string when both are unset, so the default body is scanned too.
          for (const nested of extractComposeRequiredVars(rest)) {
            required.add(nested);
          }
        }
        // Anything else is either an error directive (`:?msg`, `?msg`), whose body
        // only builds a message and can never become a value, or a name outside the
        // TOKENHUB_[A-Z0-9_]+ convention this checker governs. Neither is reported.
      }
      index = cursor;
      continue;
    }

    const name = source.slice(index + 1).match(namePattern);
    // The name must end at a real token boundary. Compose reads `$TOKENHUB_PORTsuffix`
    // as one variable named `TOKENHUB_PORTsuffix`; matching the uppercase prefix would
    // report a variable that does not exist.
    if (name && !/[A-Za-z0-9_]/.test(source[index + 1 + name[1].length] ?? "")) {
      required.add(name[1]);
      index += name[1].length;
    }
  }
  return required;
}

/** Finds every variable mentioned in a documentation file. */
export function extractDocumentedVars(source) {
  const documented = new Set();
  for (const match of source.matchAll(new RegExp(VAR, "g"))) {
    documented.add(match[0]);
  }
  return documented;
}

function sorted(set) {
  return [...set].sort();
}

/**
 * Rule 1 — every backend variable is described in at least one English document.
 */
export function checkDocumented(goVars, documentedVars) {
  return sorted(goVars)
    .filter((name) => !documentedVars.has(name))
    .map((name) => ({
      rule: "documented",
      name,
      detail: `${name} is read by Go code but appears in no English deployment document`,
    }));
}

/**
 * Rule 2 — no phantom knobs. Every variable an .env.example offers is either read by
 * Go or owned by a declared non-Go consumer.
 */
export function checkNoPhantoms(fileName, envVars, goVars) {
  const consumers = KNOWN_CONSUMERS[fileName] ?? new Map();
  const mentioned = new Set([...envVars.active, ...envVars.commented]);
  return sorted(mentioned)
    .filter((name) => !goVars.has(name) && !consumers.has(name))
    .map((name) => ({
      rule: "phantom",
      name,
      detail:
        `${fileName} offers ${name}, but no Go code reads it and no consumer is ` +
        `declared for it. Setting it does nothing.`,
    }));
}

/**
 * Rule 3 — a Compose interpolation without a default must have a value to find.
 * Requires an active assignment: a commented-out line does not stop the variable
 * from resolving to an empty string.
 */
export function checkComposeDefaults(requiredVars, deployEnv) {
  return sorted(requiredVars)
    .filter((name) => !deployEnv.active.has(name))
    .map((name) => ({
      rule: "compose-default",
      name,
      detail:
        `${name} is interpolated in a Compose file with no :-default and is not set ` +
        `in deploy/.env.example, so it resolves to an empty string`,
    }));
}

/**
 * Rule 4 — backend/.env.example stays the complete developer reference it already is.
 */
export function checkBackendCompleteness(goVars, backendEnv) {
  const mentioned = new Set([...backendEnv.active, ...backendEnv.commented]);
  return sorted(goVars)
    .filter((name) => !mentioned.has(name))
    .map((name) => ({
      rule: "backend-completeness",
      name,
      detail: `${name} is read by Go code but is missing from backend/.env.example`,
    }));
}
