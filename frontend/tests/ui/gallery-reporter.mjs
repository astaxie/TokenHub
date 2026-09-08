import { execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

const escape = value => String(value).replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);

export default class GalleryReporter {
  entries = [];
  onBegin(config) {
    this.output = config.projects[0].outputDir;
    try {
      this.head = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8", cwd: config.rootDir }).trim();
      const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8", cwd: config.rootDir }).trim();
      this.dirty = Boolean(execFileSync("git", ["status", "--porcelain", "--", ".", ":!frontend/next-env.d.ts"], { encoding: "utf8", cwd: root }).trim());
    }
    catch { this.head = "unavailable"; }
  }
  onTestEnd(test, result) {
    const captures = test.annotations.filter(item => item.type === "ui-capture").map(item => JSON.parse(item.description));
    const images = result.attachments.filter(item => item.contentType === "image/png" && item.path).map(item => {
      const relative = path.relative(this.output, item.path);
      if (relative.startsWith("..") || path.isAbsolute(relative)) throw new Error("Screenshot must be inside the UI artifact directory");
      const capture = captures.find(capture => capture.id === item.name);
      return { file: relative.split(path.sep).join("/"), id: capture?.id ?? item.name, title: capture?.title ?? item.name };
    });
    this.entries.push({ id: captures[0]?.id ?? test.title, title: captures[0]?.title ?? test.title, test: test.title, status: result.status, expectedStatus: test.expectedStatus, images });
  }
  async onEnd(result) {
    await mkdir(this.output, { recursive: true });
    this.entries.sort((a, b) => a.id.localeCompare(b.id));
    const manifest = { status: result.status, sourceHead: this.head, workingTreeDirty: this.dirty ?? null, evidence: "Frontend UI with synthetic API fixtures; no backend or database verification; no pixel baseline comparison.", scenarios: this.entries };
    await writeFile(path.join(this.output, "manifest.json"), JSON.stringify(manifest, null, 2));
    const cards = this.entries.map(entry => `<article><h2>${escape(entry.title)}</h2><p>${escape(entry.id)} · ${escape(entry.status)}</p>${entry.images.map(image => {
      const url = image.file.split("/").map(encodeURIComponent).join("/");
      return `<h3>${escape(image.title)}</h3><a href="${url}" target="_blank" rel="noopener"><img loading="lazy" src="${url}" alt="${escape(image.title)}"></a>`;
    }).join("") || "<p>本项只有行为断言，没有截图。</p>"}</article>`).join("");
    await writeFile(path.join(this.output, "index.html"), `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>TokenHub UI 验收图集</title><style>
*{box-sizing:border-box}body{margin:0;background:#f5f7fa;color:#172339;font:16px/1.7 system-ui,sans-serif}main{max-width:1400px;margin:auto;padding:32px 24px}h1{margin:0 0 12px}h2{font-size:18px}h3{font-size:15px}p{color:#526078}a{color:#285bd5}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px}article{padding:18px;border:1px solid #dde3ec;border-radius:12px;background:white;min-width:0}img{width:100%;height:320px;object-fit:contain;background:#f6f7fa;border:1px solid #edf0f5}code{overflow-wrap:anywhere}@media(max-width:760px){.grid{grid-template-columns:1fr}main{padding:22px 14px}}
</style><main><h1>TokenHub UI 验收图集</h1><p>结果：<strong>${escape(result.status)}</strong> · 源代码提交：<code>${escape(this.head)}</code>${this.dirty ? "（含未提交修改）" : ""}</p><p>真实前端 + 固定 API 样本。未运行 Go 或数据库；不能据此确认后端权限、金额计算或事务正确性。本阶段不进行像素基线比较。</p><p>点击截图查看原图。每次执行会替换本目录产物；需要留档时请复制整个目录。<a href="manifest.json">查看场景清单</a></p><section class="grid">${cards}</section></main></html>`);
    process.stdout.write(`UI gallery: ${path.join(this.output, "index.html")}\n`);
  }
}
