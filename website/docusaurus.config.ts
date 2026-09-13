import type { Config } from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import { themes as prismThemes } from 'prism-react-renderer';
import { existsSync } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const repoUrl = 'https://github.com/astaxie/TokenHub';
const repoBranch = 'main';

// The sync script writes i18n locale trees for these docs locales; the map
// converts a Docusaurus locale to its repository documentation directory.
const localeSourceDirs: Record<string, string> = {
  'zh-Hans': 'zh-CN',
  ja: 'ja',
};

// website/docs is generated from the repository English documentation by
// scripts/sync-docs.mjs, which also writes a generated -> source map so
// "Edit this page" can point at the canonical markdown file.
const require = createRequire(import.meta.url);
const sourceMapPath = fileURLToPath(
  new URL('./scripts/docs-source-map.json', import.meta.url),
);
const docsSourceMap: Record<string, string> = existsSync(sourceMapPath)
  ? require(sourceMapPath)
  : {};

function docsEditUrl(
  locale: string | undefined,
  docPath: string,
): string | undefined {
  const localeMatch =
    /^i18n\/([^/]+)\/docusaurus-plugin-content-docs\/current\/(.*)$/.exec(docPath);
  const source = docsSourceMap[localeMatch ? localeMatch[2] : docPath];
  if (!source) {
    return undefined;
  }
  const localeName = localeMatch ? localeMatch[1] : locale;
  const sourceDir =
    localeName && localeName !== 'en'
      ? (localeSourceDirs[localeName] ?? localeName)
      : undefined;
  const docsPath = sourceDir ? `${sourceDir}/${source}` : source;
  return `${repoUrl}/edit/${repoBranch}/docs/${docsPath}`;
}

const config: Config = {
  title: 'TokenHub Docs',
  tagline:
    'Enterprise token governance for AI: model routing, access control, token cost optimization, and provider reconciliation.',
  favicon: 'img/tokenhub-logo.png',

  // GitHub Pages project site (https://astaxie.github.io/TokenHub/).
  // Override for other hosts: DOCS_URL=https://docs.example.com npm run build
  url: process.env.DOCS_URL ?? 'https://astaxie.github.io',
  baseUrl: process.env.DOCS_BASE_URL ?? '/TokenHub/',
  trailingSlash: true,

  onBrokenLinks: 'throw',

  organizationName: 'astaxie',
  projectName: 'TokenHub',

  // Repository documentation is plain GitHub-flavored markdown; parsing it
  // without the MDX toolchain keeps every generated page build-safe.
  markdown: {
    format: 'md',
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  // Architecture and deployment docs embed mermaid flowcharts. The search
  // plugin provides the offline search bar and page for every locale build;
  // its language list matches the i18n locales above.
  themes: [
    '@docusaurus/theme-mermaid',
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        docsRouteBasePath: '/',
        language: ['en', 'zh', 'ja'],
        highlightSearchTermsOnTargetPage: true,
      },
    ],
  ],

  // Locale trees are generated from docs/zh-CN and docs/ja by the sync script.
  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh-Hans', 'ja'],
    localeConfigs: {
      en: {
        label: 'English',
      },
      'zh-Hans': {
        label: '简体中文',
      },
      ja: {
        label: '日本語',
      },
    },
  },

  presets: [
    [
      'classic',
      {
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: ({ locale, docPath }) => docsEditUrl(locale, docPath),
          breadcrumbs: true,
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/tokenhub-logo.png',
    navbar: {
      title: 'TokenHub',
      logo: {
        alt: 'TokenHub',
        src: 'img/tokenhub-logo.png',
        href: '/',
      },
      items: [
        {
          type: 'localeDropdown',
          position: 'right',
        },
        {
          href: repoUrl,
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'light',
      links: [
        {
          title: 'Documentation',
          items: [
            { label: 'Architecture', to: '/concepts/architecture/' },
            { label: 'User Guide', to: '/guides/user-guide/' },
            { label: 'Administrator Guide', to: '/guides/administrator-guide/' },
            { label: 'Deployment', to: '/operations/deployment/' },
          ],
        },
        {
          title: 'Platform',
          items: [
            { label: 'Billing and Pricing', to: '/billing/billing-pricing/' },
            { label: 'Agent Token Cost API', to: '/billing/agent-token-cost-api/' },
            { label: 'Plugin Development', to: '/plugin-development/' },
            { label: 'Migration', to: '/migration/' },
          ],
        },
        {
          title: 'Community',
          items: [
            { label: 'GitHub', href: repoUrl },
            { label: 'Issues', href: `${repoUrl}/issues` },
            { label: 'Apache-2.0 License', href: `${repoUrl}/blob/main/LICENSE` },
          ],
        },
      ],
      copyright: `© ${new Date().getFullYear()} TokenHub contributors. Apache-2.0 licensed.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'go', 'yaml', 'sql', 'docker', 'ini', 'toml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
