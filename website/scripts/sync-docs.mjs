#!/usr/bin/env node
//
// Regenerates website/docs and website/i18n from the repository documentation
// so the documentation website renders the canonical markdown without keeping
// a second, drifting copy of it in this repository.
//
// The default locale tree (website/docs) is generated from the English docs
// in ../docs. Every configured locale (zh-Hans from ../docs/zh-CN, ja from
// ../docs/ja) gets a mirror tree under website/i18n/<locale>/... with the same
// generated page ids; a page without a translated source falls back to the
// English content so locale builds never break links.
//
// For every page the script:
//   - strips the "Language: ..." switcher line (the site has its own locale
//     dropdown; localized docs stay in docs/zh-CN and docs/ja)
//   - rewrites cross-document links to their generated locations and points
//     links that leave docs/ at GitHub
//   - injects Docusaurus frontmatter (sidebar position, slug); English pages
//     also get an explicit title, locale pages derive it from the content
//
// The site layout lives in SECTIONS below. To publish a page on the website,
// add its repository file to a section; the prestart and prebuild hooks run
// this script automatically.

import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { dirname, relative as pathRelative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const websiteDir = resolve(scriptDir, '..');
const repoRoot = resolve(websiteDir, '..');
const sourceDocsDir = resolve(repoRoot, 'docs');
const targetDocsDir = resolve(websiteDir, 'docs');
const targetI18nDir = resolve(websiteDir, 'i18n');
const sourceMapPath = resolve(scriptDir, 'docs-source-map.json');

const repoUrl = 'https://github.com/astaxie/TokenHub';
const repoBranch = 'main';

// Locale builds read from website/i18n/<locale>/docusaurus-plugin-content-docs/
// current/, mirroring the generated default-locale tree page by page.
// categoryLabels translates the sidebar section names of each locale.
const I18N_CONTENT_DIR = 'docusaurus-plugin-content-docs/current';

const LOCALES = [
  {
    locale: 'zh-Hans',
    sourceDirName: 'zh-CN',
    linkLabels: { Introduction: '介绍' },
    categoryLabels: {
      'Billing and Cost': '计费与成本',
      'Client Integrations': '客户端集成',
      Concepts: '核心概念',
      Development: '开发',
      Migration: '迁移指南',
      Operations: '运维部署',
      'Plugin Development': '插件开发',
      'Role Guides': '角色指南',
    },
  },
  {
    locale: 'ja',
    sourceDirName: 'ja',
    linkLabels: { Introduction: 'はじめに' },
    categoryLabels: {
      'Billing and Cost': '課金とコスト',
      'Client Integrations': 'クライアント統合',
      Concepts: '基本概念',
      Development: '開発',
      Migration: '移行ガイド',
      Operations: '運用とデプロイ',
      'Plugin Development': 'プラグイン開発',
      'Role Guides': 'ロールガイド',
    },
  },
];

const LANDING = {
  source: 'README.md',
  dest: 'index.md',
  title: 'TokenHub Documentation',
  description:
    'Official TokenHub documentation: architecture, role guides, billing, client integrations, operations, plugin development, and migration.',
  slug: '/',
  hideFromSidebar: true,
};

const SECTIONS = [
  {
    label: 'Concepts',
    dir: 'concepts',
    pages: [{ source: 'architecture.md', title: 'Architecture' }],
  },
  {
    label: 'Role Guides',
    dir: 'guides',
    pages: [
      { source: 'user-guide.md', title: 'User Guide' },
      { source: 'team-leader-guide.md', title: 'Team Leader Guide' },
      { source: 'administrator-guide.md', title: 'Administrator Guide' },
    ],
  },
  {
    label: 'Billing and Cost',
    dir: 'billing',
    pages: [
      { source: 'billing-pricing.md', title: 'Billing and Pricing' },
      { source: 'agent-token-cost-api.md', title: 'Agent Token Cost API' },
    ],
  },
  {
    label: 'Client Integrations',
    dir: 'integrations',
    pages: [
      {
        source: 'codex-tokenhub-profile-quick-start.md',
        title: 'Codex Profile Quick Start',
      },
      {
        source: 'codex-tokenhub-configuration.md',
        title: 'Codex Configuration Methods',
      },
      {
        source: 'gemini-cli-codex-subscription.md',
        title: 'Gemini CLI with Codex Subscription',
      },
    ],
  },
  {
    label: 'Operations',
    dir: 'operations',
    pages: [
      { source: 'deployment.md', title: 'Deployment' },
      { source: 'postgresql-setup.md', title: 'PostgreSQL Setup' },
      { source: 'database-evolution.md', title: 'Database Evolution' },
      { source: 'performance-benchmarking.md', title: 'Performance Benchmarking' },
    ],
  },
  {
    label: 'Plugin Development',
    dir: 'plugin-development',
    pages: [
      {
        source: 'plugin-development/README.md',
        dest: 'plugin-development/index.md',
        title: 'Plugin Development',
      },
      { source: 'plugin-development/getting-started.md', title: 'Getting Started' },
      { source: 'plugin-development/guide.md', title: 'Architecture Guide' },
      { source: 'plugin-development/manifest-reference.md', title: 'Manifest Reference' },
      { source: 'plugin-development/provider-plugins.md', title: 'Provider Plugins' },
      { source: 'plugin-development/gateway-hooks.md', title: 'Gateway Hooks' },
      { source: 'plugin-development/ui-templates.md', title: 'UI Templates' },
      { source: 'plugin-development/background-jobs.md', title: 'Background Jobs' },
      {
        source: 'plugin-development/packaging-and-release.md',
        title: 'Packaging and Release',
      },
    ],
  },
  {
    label: 'Migration',
    dir: 'migration',
    pages: [
      {
        source: 'migration/README.md',
        dest: 'migration/index.md',
        title: 'Migration Guide',
      },
      { source: 'migration/architecture.md', title: 'Migration Architecture' },
      { source: 'migration/bundle-schema.md', title: 'Bundle Schema' },
      { source: 'migration/cli.md', title: 'Migration CLI' },
      { source: 'migration/litellm.md', title: 'Migrating from LiteLLM' },
      { source: 'migration/e2e.md', title: 'End-to-End Validation' },
    ],
  },
  {
    label: 'Development',
    dir: 'development',
    pages: [{ source: 'development/ui-testing.md', title: 'UI Testing' }],
  },
];

const englishSwitcherPattern = /^Language: English \|/;
// Localized switcher lines are written in the document language, for example
// "语言：" in docs/zh-CN and "言語：" in docs/ja.
const localeSwitcherPattern = /^(Language: |语言[:：]|言語[:：])/;
const markdownLinkPattern = /\]\(([^)\s]+)\)/g;
const fencePattern = /^[ \t]*(```|~~~)/;

const posix = (value) => value.replaceAll('\\', '/');

function toPosixRelative(fromDir, toPath) {
  let value = posix(pathRelative(fromDir, toPath));
  if (!value.startsWith('.')) {
    value = `./${value}`;
  }
  return value;
}

// source relative path -> generated plan entry
function buildPlan() {
  const plan = new Map();
  plan.set(LANDING.source, { ...LANDING, position: 0 });

  SECTIONS.forEach((section, sectionIndex) => {
    section.pages.forEach((page, pageIndex) => {
      if (plan.has(page.source)) {
        throw new Error(`Page configured twice: ${page.source}`);
      }
      const dest =
        page.dest ?? `${section.dir}/${page.source.split('/').pop()}`;
      plan.set(page.source, {
        ...page,
        dest,
        position: pageIndex + 1,
      });
    });
  });
  return plan;
}

function splitAnchor(target) {
  const hashIndex = target.indexOf('#');
  if (hashIndex === -1) {
    return [target, ''];
  }
  return [target.slice(0, hashIndex), target.slice(hashIndex)];
}

function isInside(rootDir, absolutePath) {
  return !pathRelative(rootDir, absolutePath).startsWith('..');
}

function rewriteLink(target, context, stats) {
  if (/^(https?:|mailto:|#|\/\/)/.test(target)) {
    return target;
  }
  const [pathPart, anchor] = splitAnchor(target);
  if (pathPart === '') {
    return target;
  }

  const absolute = resolve(context.sourceAbsDir, pathPart);
  if (!isInside(repoRoot, absolute)) {
    throw new Error(
      `Link "${target}" in ${context.source} points outside the repository`,
    );
  }

  // Candidate roots, deepest first, for turning the target into a plan key or
  // an asset path. All matches resolve inside the current locale's generated
  // tree, which mirrors the default tree page by page.
  for (const keyRoot of context.keyRoots) {
    if (!isInside(keyRoot, absolute)) {
      continue;
    }
    const key = posix(pathRelative(keyRoot, absolute));
    const page = context.plan.get(key);
    if (page) {
      return `${toPosixRelative(
        context.destAbsDir,
        resolve(context.destRoot, page.dest),
      )}${anchor}`;
    }
    // Images and other non-markdown files resolve as assets; they only exist
    // in the English docs tree and the generated locale trees copy them, so
    // the relative path stays valid. Markdown links that are not published
    // pages (for example a locale-only page) stay reachable on GitHub.
    const isMarkdownLink = key.endsWith('.md') || key.endsWith('.mdx');
    const assetSource = resolve(sourceDocsDir, key);
    if (
      !isMarkdownLink &&
      existsSync(assetSource) &&
      statSync(assetSource).isFile()
    ) {
      return `${toPosixRelative(
        context.destAbsDir,
        resolve(context.destRoot, key),
      )}${anchor}`;
    }
  }

  const docsRelative = pathRelative(sourceDocsDir, absolute);
  if (isInside(sourceDocsDir, absolute)) {
    if (!docsRelative.endsWith('.md')) {
      throw new Error(
        `Link "${target}" in ${context.source} does not match a documented page or file`,
      );
    }
    // A documented but unpublished markdown page, for example a locale-only
    // page: link to GitHub so the target stays reachable.
    stats.githubDocLinks += 1;
    return `${repoUrl}/blob/${repoBranch}/${posix(
      pathRelative(repoRoot, absolute),
    )}${anchor}`;
  }

  // The link leaves the documentation tree (for example plugin-devkit);
  // point it at GitHub so it stays resolvable on the website.
  const repoRelative = posix(pathRelative(repoRoot, absolute));
  const kind = repoRelative.endsWith('.md') ? 'blob' : 'tree';
  return `${repoUrl}/${kind}/${repoBranch}/${repoRelative}${anchor}`;
}

function rewriteMarkdownLinks(content, context, stats) {
  const lines = content.split('\n');
  let insideFence = false;
  const rewritten = lines.map((line) => {
    if (fencePattern.test(line)) {
      insideFence = !insideFence;
      return line;
    }
    if (insideFence) {
      return line;
    }
    return line.replace(markdownLinkPattern, (full, target) => {
      const result = rewriteLink(target, context, stats);
      if (result !== target) {
        stats.rewrittenLinks += 1;
      }
      return `](${result})`;
    });
  });
  return rewritten.join('\n');
}

function stripSwitcherLine(content, pattern, stats) {
  const lines = content.split('\n').filter((line) => {
    if (!pattern.test(line)) {
      return true;
    }
    stats.strippedLines += 1;
    return false;
  });
  return lines.join('\n');
}

function quoteYaml(value) {
  return `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`;
}

function buildFrontmatter(page, locale) {
  const lines = ['---'];
  if (!locale && page.title) {
    lines.push(`title: ${quoteYaml(page.title)}`);
  }
  lines.push(`sidebar_position: ${page.position}`);
  if (!locale && page.description) {
    lines.push(`description: ${quoteYaml(page.description)}`);
  }
  if (page.slug) {
    lines.push(`slug: ${page.slug}`);
  }
  if (page.hideFromSidebar) {
    lines.push('sidebar_class_name: hidden');
  }
  lines.push('---', '');
  return lines.join('\n');
}

function renderPage(rawContent, page, plan, stats, localeContext) {
  const locale = localeContext?.locale;
  const sourceAbsDir = dirname(
    resolve(localeContext?.sourceRoot ?? sourceDocsDir, page.source),
  );
  const destRoot = localeContext?.destRoot ?? targetDocsDir;
  const destAbsDir = dirname(resolve(destRoot, page.dest));
  const context = {
    source: page.source,
    sourceAbsDir,
    destRoot,
    destAbsDir,
    plan,
    keyRoots: localeContext
      ? [localeContext.sourceRoot, sourceDocsDir]
      : [sourceDocsDir],
  };

  const switcherPattern = locale
    ? localeSwitcherPattern
    : englishSwitcherPattern;
  let content = stripSwitcherLine(rawContent, switcherPattern, stats);
  content = rewriteMarkdownLinks(content, context, stats);

  return `${buildFrontmatter(page, locale)}\n${content}`;
}

function renderInto(destRoot, rawContent, page, plan, stats, localeContext) {
  const outputPath = resolve(destRoot, page.dest);
  mkdirSync(dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, renderPage(rawContent, page, plan, stats, localeContext));
}

function writeSectionCategories(destRoot) {
  SECTIONS.forEach((section, sectionIndex) => {
    const indexPage = section.pages.find(
      (page) => (page.dest ?? '') === `${section.dir}/index.md`,
    );
    const category = {
      label: section.label,
      position: sectionIndex + 1,
    };
    if (indexPage) {
      category.link = {
        type: 'doc',
        id: `${section.dir}/index`,
      };
    }
    const categoryPath = resolve(destRoot, section.dir, '_category_.json');
    mkdirSync(dirname(categoryPath), { recursive: true });
    writeFileSync(categoryPath, `${JSON.stringify(category, null, 2)}\n`);
  });
}

function sidebarTranslations({ linkLabels, categoryLabels }) {
  const translations = {};
  for (const [label, message] of Object.entries(linkLabels)) {
    translations[`sidebar.docs.link.${label}`] = { message };
  }
  for (const [label, message] of Object.entries(categoryLabels)) {
    translations[`sidebar.docs.category.${label}`] = { message };
  }
  return translations;
}

function main() {
  const plan = buildPlan();
  const stats = { strippedLines: 0, rewrittenLinks: 0, githubDocLinks: 0 };
  const localeStats = new Map();

  for (const [source] of plan) {
    if (!existsSync(resolve(sourceDocsDir, source))) {
      throw new Error(`Configured page does not exist: docs/${source}`);
    }
  }

  rmSync(targetDocsDir, { recursive: true, force: true });
  rmSync(targetI18nDir, { recursive: true, force: true });
  mkdirSync(targetDocsDir, { recursive: true });

  // Default locale, generated from the English documentation.
  const sourceMap = {};
  for (const [source, page] of plan) {
    const rawContent = readFileSync(resolve(sourceDocsDir, source), 'utf8');
    renderInto(targetDocsDir, rawContent, page, plan, stats);
    sourceMap[page.dest] = source;
  }
  writeSectionCategories(targetDocsDir);
  cpSync(resolve(sourceDocsDir, 'assets'), resolve(targetDocsDir, 'assets'), {
    recursive: true,
  });

  // Localized trees under website/i18n/<locale>/...
  for (const localeConfig of LOCALES) {
    const { locale, sourceDirName } = localeConfig;
    const localeSourceRoot = resolve(sourceDocsDir, sourceDirName);
    if (!existsSync(localeSourceRoot)) {
      throw new Error(`Locale docs directory does not exist: docs/${sourceDirName}`);
    }
    const destRoot = resolve(targetI18nDir, locale, I18N_CONTENT_DIR);
    mkdirSync(destRoot, { recursive: true });

    let translated = 0;
    for (const [source, page] of plan) {
      const localizedPath = resolve(localeSourceRoot, source);
      const hasTranslation = existsSync(localizedPath);
      if (hasTranslation) {
        translated += 1;
      }
      const rawContent = readFileSync(
        hasTranslation ? localizedPath : resolve(sourceDocsDir, source),
        'utf8',
      );
      renderInto(destRoot, rawContent, page, plan, stats, {
        locale,
        sourceRoot: localeSourceRoot,
        destRoot,
      });
    }
    writeSectionCategories(destRoot);
    const translationPath = resolve(
      targetI18nDir,
      locale,
      'docusaurus-plugin-content-docs',
      'current.json',
    );
    writeFileSync(
      translationPath,
      `${JSON.stringify(sidebarTranslations(localeConfig), null, 2)}\n`,
    );
    cpSync(resolve(sourceDocsDir, 'assets'), resolve(destRoot, 'assets'), {
      recursive: true,
    });
    localeStats.set(locale, {
      sourceDirName,
      translated,
      fallback: plan.size - translated,
    });
  }

  writeFileSync(sourceMapPath, `${JSON.stringify(sourceMap, null, 2)}\n`);

  const localeSummary = [...localeStats.entries()]
    .map(
      ([locale, { translated, fallback }]) =>
        `${locale}: ${translated} translated, ${fallback} English fallback`,
    )
    .join('; ');
  console.log(
    `sync-docs: generated ${plan.size} pages per locale, rewrote ` +
      `${stats.rewrittenLinks} links (${stats.githubDocLinks} to GitHub for ` +
      `unpublished pages), stripped ${stats.strippedLines} ` +
      `language switcher lines (${localeSummary})`,
  );
}

main();
