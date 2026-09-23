import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  fetchContributors,
  renderContributors,
  replaceContributorSection,
  updateReadmes,
} from "./update-contributors.mjs";

const contributors = [
  {
    login: "alice",
    avatar_url: "https://avatars.example/alice",
    html_url: "https://github.com/alice",
  },
  {
    login: "release[bot]",
    avatar_url: "https://avatars.example/bot",
    html_url: "https://github.com/apps/release",
  },
  {
    login: "LOUD[BOT]",
    avatar_url: "https://avatars.example/loud-bot",
    html_url: "https://github.com/apps/loud",
  },
  {
    login: "automation",
    avatar_url: "https://avatars.example/automation",
    html_url: "https://github.com/automation",
    type: "Bot",
  },
];

describe("contributor rendering", () => {
  it("renders GitHub users and excludes bots", () => {
    const rendered = renderContributors(contributors);

    assert.match(rendered, /href="https:\/\/github\.com\/alice"/);
    assert.match(rendered, /<b>alice<\/b>/);
    assert.doesNotMatch(rendered, /release\[bot\]/);
    assert.doesNotMatch(rendered, /LOUD\[BOT\]/);
    assert.doesNotMatch(rendered, /automation/);
  });

  it("escapes contributor-controlled values", () => {
    const rendered = renderContributors([
      {
        login: 'a<&"',
        avatar_url: 'https://avatars.example/a?x=1&y="2"',
        html_url: "https://github.com/a?x=1&y=2",
      },
    ]);

    assert.match(rendered, /a&lt;&amp;&quot;/);
    assert.match(rendered, /x=1&amp;y=&quot;2&quot;/);
  });
});

describe("README transaction", () => {
  const markerDocument = (content) =>
    [
      "before",
      "<!-- readme: contributors -start -->",
      content,
      "<!-- readme: contributors -end -->",
      "after",
    ].join("\n");

  it("validates every README before writing any of them", async () => {
    const files = new Map([
      ["README.md", markerDocument("old")],
      ["README.zh-CN.md", "missing markers"],
      ["README.ja.md", markerDocument("old")],
    ]);
    let writes = 0;

    await assert.rejects(
      () =>
        updateReadmes(contributors, {
          paths: [...files.keys()],
          readFileImpl: async (path) => files.get(path),
          writeFileImpl: async () => {
            writes += 1;
          },
          log() {},
        }),
      /must contain/,
    );

    assert.equal(writes, 0);
    assert.equal(files.get("README.md"), markerDocument("old"));
  });

  it("rolls back every touched README when a write fails", async () => {
    const original = new Map([
      ["README.md", markerDocument("english")],
      ["README.zh-CN.md", markerDocument("chinese")],
      ["README.ja.md", markerDocument("japanese")],
    ]);
    const files = new Map(original);
    let failedOnce = false;

    await assert.rejects(
      () =>
        updateReadmes(contributors, {
          paths: [...files.keys()],
          readFileImpl: async (path) => files.get(path),
          writeFileImpl: async (path, content) => {
            if (path === "README.zh-CN.md" && !failedOnce) {
              failedOnce = true;
              throw new Error("simulated write failure");
            }
            files.set(path, content);
          },
          log() {},
        }),
      /simulated write failure/,
    );

    assert.deepEqual(files, original);
  });
});

describe("README replacement", () => {
  it("replaces only the marked contributor section", () => {
    const readme = [
      "before",
      "<!-- readme: contributors -start -->",
      "old",
      "<!-- readme: contributors -end -->",
      "after",
    ].join("\n");

    assert.equal(
      replaceContributorSection(readme, "new"),
      [
        "before",
        "<!-- readme: contributors -start -->",
        "",
        "new",
        "",
        "<!-- readme: contributors -end -->",
        "after",
      ].join("\n"),
    );
  });

  it("rejects missing or duplicate markers", () => {
    assert.throws(
      () => replaceContributorSection("README", "new"),
      /must contain/,
    );
    assert.throws(
      () =>
        replaceContributorSection(
          "<!-- readme: contributors -start -->\n<!-- readme: contributors -start -->\n<!-- readme: contributors -end -->",
          "new",
        ),
      /duplicate/,
    );
  });
});

describe("GitHub contributor pagination", () => {
  it("requests pages until GitHub returns fewer than 100 entries", async () => {
    const requests = [];
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      login: `user-${index}`,
    }));
    const fetchImpl = async (url, options) => {
      requests.push({ url, options });
      return {
        ok: true,
        async json() {
          return requests.length === 1 ? firstPage : [{ login: "last-user" }];
        },
      };
    };

    const result = await fetchContributors(
      "astaxie/TokenHub",
      "test-token",
      fetchImpl,
    );

    assert.equal(result.length, 101);
    assert.match(requests[0].url, /page=1$/);
    assert.match(requests[1].url, /page=2$/);
    assert.equal(
      requests[0].options.headers.Authorization,
      "Bearer test-token",
    );
  });

  it("rejects malformed repository names", async () => {
    await assert.rejects(
      () => fetchContributors("not-a-repository", "token"),
      /Invalid/,
    );
  });
});
