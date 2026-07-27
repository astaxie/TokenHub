#!/usr/bin/env node
// Parses a systemd EnvironmentFile the way systemd itself does, and prints shell
// assignments the installer can eval.
//
// The installer must see exactly the values the service will receive. Reading the
// file with sed or a shell loop gets quoting, escapes and continuations wrong,
// and sourcing it is not an option: EnvironmentFile syntax is not shell syntax,
// so sourcing would execute whatever it contains.
//
// This mirrors the character-level state machine in systemd's
// src/basic/env-file.c. Two properties of that grammar are easy to get wrong and
// are deliberately reproduced here:
//   * a backslash removes the special meaning of the next character and is then
//     dropped; there is NO C-style escape table, so `\t` is the letter t, not a
//     tab, and `\x41` is x41;
//   * a backslash immediately before a newline continues the line.
// Getting either wrong would validate one value while the service receives
// another, which is worse than refusing the file.
//
// Usage:
//   parse-env-file.mjs FILE          print KEY='value' for every assignment
//   parse-env-file.mjs FILE KEY      print the value of KEY only (nothing if unset)
//
// Exits non-zero with a message on stderr when the file cannot be parsed, or when
// a value contains a newline: the shell transport below cannot carry those
// losslessly, so they are refused rather than silently truncated.

import { readFileSync } from "node:fs";

const [, , filePath, singleKey] = process.argv;
if (!filePath) {
  console.error("usage: parse-env-file.mjs FILE [KEY]");
  process.exit(2);
}

let contents;
try {
  contents = readFileSync(filePath, "utf8");
} catch (error) {
  console.error(`Cannot read ${filePath}: ${error.message}`);
  process.exit(1);
}

const PRE_KEY = 0;
const KEY = 1;
const PRE_VALUE = 2;
// A value is either a sequence of quoted sections, or a bare value in which
// quote characters are literal. Which one it is, is decided by the first
// character after "=". Verified against systemd: `pre"post"` keeps both quotes,
// while `"mixed"'quotes'` concatenates to mixedquotes.
const VALUE_BARE = 3;
const VALUE_QUOTED = 11;
const VALUE_ESCAPE = 4;
const SINGLE_QUOTE = 5;
const DOUBLE_QUOTE = 7;
const DOUBLE_QUOTE_ESCAPE = 8;
const COMMENT = 9;
const COMMENT_ESCAPE = 10;

const values = new Map();
let state = PRE_KEY;
let key = "";
let value = "";
// Whitespace only counts as trailing once the value ends unquoted; anything
// inside quotes is part of the value.
let trailingWhitespace = 0;
let line = 1;

function commit() {
  if (key) {
    let finalValue = value;
    if (trailingWhitespace > 0) finalValue = finalValue.slice(0, finalValue.length - trailingWhitespace);
    if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) values.set(key, finalValue);
  }
  key = "";
  value = "";
  trailingWhitespace = 0;
}

function fail(message) {
  console.error(`${filePath}: line ${line}: ${message}`);
  process.exit(1);
}

for (let i = 0; i < contents.length; i += 1) {
  const c = contents[i];
  if (c === "\n") line += 1;

  switch (state) {
    case PRE_KEY:
      if (c === "#" || c === ";") state = COMMENT;
      else if (c === "\n" || c === " " || c === "\t" || c === "\r") break;
      else {
        state = KEY;
        key = c;
      }
      break;

    case KEY:
      if (c === "\n") {
        // An assignment without "=" is discarded, as systemd does.
        key = "";
        state = PRE_KEY;
      } else if (c === "=") {
        state = PRE_VALUE;
      } else {
        key += c;
      }
      break;

    case PRE_VALUE:
      if (c === "\n") {
        commit();
        state = PRE_KEY;
      } else if (c === " " || c === "\t" || c === "\r") {
        // Leading whitespace is not part of the value.
      } else if (c === "'") {
        state = SINGLE_QUOTE;
      } else if (c === '"') {
        state = DOUBLE_QUOTE;
      } else if (c === "\\") {
        state = VALUE_ESCAPE;
      } else {
        value += c;
        state = VALUE_BARE;
      }
      break;

    case VALUE_BARE:
      if (c === "\n") {
        commit();
        state = PRE_KEY;
      } else if (c === "\\") {
        state = VALUE_ESCAPE;
      } else {
        // Quote characters are literal here: this value did not start with one.
        if (c === " " || c === "\t" || c === "\r") trailingWhitespace += 1;
        else trailingWhitespace = 0;
        value += c;
      }
      break;

    case VALUE_QUOTED:
      if (c === "\n") {
        commit();
        state = PRE_KEY;
      } else if (c === "'") {
        state = SINGLE_QUOTE;
      } else if (c === '"') {
        state = DOUBLE_QUOTE;
      } else if (c === "\\") {
        state = VALUE_ESCAPE;
      } else {
        // An ordinary character after a quoted section ends the quoted sequence:
        // from here on quotes are literal. Verified against systemd:
        // `"a"b"c"` yields ab"c".
        if (c === " " || c === "\t" || c === "\r") trailingWhitespace += 1;
        else trailingWhitespace = 0;
        value += c;
        state = VALUE_BARE;
      }
      break;

    case VALUE_ESCAPE:
      state = VALUE_BARE;
      // A backslash-newline pair continues the line and contributes nothing.
      if (c !== "\n") {
        value += c;
        trailingWhitespace = 0;
      }
      break;

    case SINGLE_QUOTE:
      // Nothing is escaped inside single quotes: a backslash is an ordinary
      // character. Verified against systemd: 'a\x42c' yields a\x42c.
      if (c === "'") {
        state = VALUE_QUOTED;
        trailingWhitespace = 0;
      } else {
        value += c;
        trailingWhitespace = 0;
      }
      break;

    case DOUBLE_QUOTE:
      if (c === '"') {
        state = VALUE_QUOTED;
        trailingWhitespace = 0;
      } else if (c === "\\") {
        state = DOUBLE_QUOTE_ESCAPE;
      } else {
        value += c;
        trailingWhitespace = 0;
      }
      break;

    case DOUBLE_QUOTE_ESCAPE:
      state = DOUBLE_QUOTE;
      // Only these characters lose their backslash; anything else keeps it.
      // Verified against systemd: "a\zb" yields a\zb, while "a\$b" yields a$b.
      if (c === "\n") {
        // Line continuation inside a quoted value.
      } else if (c === '"' || c === "\\" || c === "`" || c === "$") {
        value += c;
        trailingWhitespace = 0;
      } else {
        value += "\\" + c;
        trailingWhitespace = 0;
      }
      break;

    case COMMENT:
      if (c === "\\") state = COMMENT_ESCAPE;
      else if (c === "\n") state = PRE_KEY;
      break;

    case COMMENT_ESCAPE:
      state = COMMENT;
      break;

    default:
      fail("internal parser error");
  }
}

switch (state) {
  case SINGLE_QUOTE:
  case DOUBLE_QUOTE:
  case DOUBLE_QUOTE_ESCAPE:
    fail("unterminated quote at end of file");
    break;
  case VALUE_BARE:
  case VALUE_QUOTED:
  case VALUE_ESCAPE:
  case PRE_VALUE:
    commit();
    break;
  default:
    break;
}

for (const [name, parsed] of values) {
  if (parsed.includes("\n")) {
    console.error(`${filePath}: ${name} contains a newline, which this installer cannot transport safely.`);
    console.error("Rewrite the value on a single line.");
    process.exit(1);
  }
}

const shellQuote = (text) => `'${String(text).replace(/'/g, "'\\''")}'`;

if (singleKey) {
  // Raw, so the caller can capture it with $(...) without another unquoting step.
  if (values.has(singleKey)) process.stdout.write(values.get(singleKey));
} else {
  for (const [name, parsed] of values) {
    console.log(`${name}=${shellQuote(parsed)}`);
  }
}
