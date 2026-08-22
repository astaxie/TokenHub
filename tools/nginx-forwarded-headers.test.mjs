import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const config = await readFile(
  new URL("../deploy/nginx.multi-instance.conf", import.meta.url),
  "utf8",
);

function locationBody(pattern) {
  const match = config.match(pattern);
  assert.ok(match, `missing nginx location matching ${pattern}`);
  return match[1];
}

test("the bundled proxy overwrites forwarded headers in every upstream location", () => {
  const locations = [
    locationBody(/location ~ \^\/[\s\S]*?\{([\s\S]*?)\n    \}/),
    locationBody(/location \/ \{([\s\S]*?)\n    \}/),
  ];

  for (const location of locations) {
    assert.match(location, /proxy_set_header X-Forwarded-For \$remote_addr;/);
    assert.match(location, /proxy_set_header X-Forwarded-Host \$host;/);
    assert.match(location, /proxy_set_header X-Forwarded-Proto \$scheme;/);
  }

  assert.doesNotMatch(config, /\$proxy_add_x_forwarded_for/);
  assert.doesNotMatch(config, /\$http_x_forwarded_/i);
});
