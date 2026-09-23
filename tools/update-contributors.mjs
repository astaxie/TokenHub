#!/usr/bin/env node

import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const START_MARKER = "<!-- readme: contributors -start -->";
const END_MARKER = "<!-- readme: contributors -end -->";
const README_PATHS = ["README.md", "README.zh-CN.md", "README.ja.md"];
const COLUMNS_PER_ROW = 8;

function escapeHtml(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function renderContributors(contributors) {
  const unique = new Map();
  for (const contributor of contributors) {
    if (
      typeof contributor.login !== "string" ||
      contributor.login.toLowerCase().endsWith("[bot]") ||
      contributor.type === "Bot" ||
      typeof contributor.avatar_url !== "string" ||
      typeof contributor.html_url !== "string"
    ) {
      continue;
    }
    unique.set(contributor.login.toLowerCase(), contributor);
  }

  const entries = [...unique.values()];
  if (entries.length === 0) {
    throw new Error("GitHub returned no displayable contributors");
  }

  const rows = [];
  for (let index = 0; index < entries.length; index += COLUMNS_PER_ROW) {
    const cells = entries
      .slice(index, index + COLUMNS_PER_ROW)
      .map((contributor) => {
        const login = escapeHtml(contributor.login);
        const profile = escapeHtml(contributor.html_url);
        const avatar = escapeHtml(contributor.avatar_url);
        return [
          '    <td align="center" valign="top" width="12.5%">',
          `      <a href="${profile}">`,
          `        <img src="${avatar}" width="80px" alt="${login}" />`,
          `        <br /><sub><b>${login}</b></sub>`,
          "      </a>",
          "    </td>",
        ].join("\n");
      });
    rows.push(["  <tr>", ...cells, "  </tr>"].join("\n"));
  }

  return ["<table>", ...rows, "</table>"].join("\n");
}

export function replaceContributorSection(readme, rendered) {
  const start = readme.indexOf(START_MARKER);
  const end = readme.indexOf(END_MARKER);
  if (start === -1 || end === -1 || end < start) {
    throw new Error(
      `README must contain one ordered ${START_MARKER} / ${END_MARKER} pair`,
    );
  }
  if (
    readme.indexOf(START_MARKER, start + START_MARKER.length) !== -1 ||
    readme.indexOf(END_MARKER, end + END_MARKER.length) !== -1
  ) {
    throw new Error("README contains duplicate contributor markers");
  }

  return [
    readme.slice(0, start),
    START_MARKER,
    "\n\n",
    rendered,
    "\n\n",
    END_MARKER,
    readme.slice(end + END_MARKER.length),
  ].join("");
}

export async function fetchContributors(repository, token, fetchImpl = fetch) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) {
    throw new Error(`Invalid GITHUB_REPOSITORY value: ${repository}`);
  }

  const contributors = [];
  for (let page = 1; ; page += 1) {
    const response = await fetchImpl(
      `https://api.github.com/repos/${repository}/contributors?per_page=100&page=${page}`,
      {
        headers: {
          Accept: "application/vnd.github+json",
          Authorization: `Bearer ${token}`,
          "X-GitHub-Api-Version": "2022-11-28",
        },
      },
    );
    if (!response.ok) {
      throw new Error(
        `GitHub contributors request failed: ${response.status} ${response.statusText}`,
      );
    }

    const pageEntries = await response.json();
    if (!Array.isArray(pageEntries)) {
      throw new Error("GitHub contributors response was not an array");
    }
    contributors.push(...pageEntries);
    if (pageEntries.length < 100) {
      return contributors;
    }
  }
}

export async function updateReadmes(
  contributors,
  {
    paths = README_PATHS,
    readFileImpl = readFile,
    writeFileImpl = writeFile,
    log = console.log,
  } = {},
) {
  const rendered = renderContributors(contributors);
  const prepared = await Promise.all(
    paths.map(async (path) => {
      const current = await readFileImpl(path, "utf8");
      return {
        path,
        current,
        next: replaceContributorSection(current, rendered),
      };
    }),
  );
  const changed = prepared.filter(({ current, next }) => next !== current);
  const touched = [];

  try {
    for (const entry of changed) {
      touched.push(entry);
      await writeFileImpl(entry.path, entry.next);
    }
  } catch (writeError) {
    const rollbackErrors = [];
    for (const entry of touched.reverse()) {
      try {
        await writeFileImpl(entry.path, entry.current);
      } catch (rollbackError) {
        rollbackErrors.push(rollbackError);
      }
    }
    if (rollbackErrors.length > 0) {
      throw new AggregateError(
        [writeError, ...rollbackErrors],
        "README update failed and could not be fully rolled back",
      );
    }
    throw writeError;
  }

  log(`Updated ${changed.length} of ${paths.length} contributor sections.`);
  return changed.length;
}

async function main() {
  const repository = process.env.GITHUB_REPOSITORY;
  const token = process.env.GITHUB_TOKEN;
  if (!repository || !token) {
    throw new Error("GITHUB_REPOSITORY and GITHUB_TOKEN are required");
  }
  await updateReadmes(await fetchContributors(repository, token));
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
