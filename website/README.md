# TokenHub Documentation Website

The documentation website for TokenHub, built with [Docusaurus 3](https://docusaurus.io)
in docs-only mode. It renders the repository documentation from `docs/` as a
browsable, searchable handbook in English, Simplified Chinese, and Japanese.

## How content works

The site does **not** keep its own copy of the documentation. `scripts/sync-docs.mjs`
generates the page trees on every `npm start` and `npm run build`:

- `website/docs/` comes from the English docs (`../docs`).
- `website/i18n/<locale>/` mirrors the same page ids from `docs/zh-CN`
  (`zh-Hans`) and `docs/ja` (`ja`); a page without a translated source falls
  back to the English content so locale builds never break links.
- The `Language: ...` switcher line (including the localized `语言：` and
  `言語：` variants) is stripped; the site has its own locale dropdown.
- Cross-document links are rewritten to the generated page locations, links
  that leave `docs/` (for example `plugin-devkit/`) and unpublished pages (for
  example locale-only pages) are pointed at GitHub, and images resolve from the
  copied `assets/` directory.
- Frontmatter (sidebar position, landing slug, English titles) is injected
  according to the section layout declared in `SECTIONS` inside the script;
  sidebar section names are translated per locale in `LOCALES`.

Because the generated trees are git-ignored, never edit files under
`website/docs/` or `website/i18n/` by hand. To publish or reorganize pages,
edit the source markdown in `../docs` and adjust `SECTIONS` in
`scripts/sync-docs.mjs`.

## Commands

```bash
npm ci            # install dependencies
npm start         # dev server with live reload at http://localhost:3000/TokenHub/
npm run build     # production build into build/
npm run serve     # preview the production build locally
npm run typecheck # type-check the site config
```

The dev server reads the generated trees; after changing a file under
`../docs`, save again or restart `npm start` to re-run the sync.

## Deployment

`docs-deploy.yml` (`.github/workflows/`) deploys the site to GitHub Pages on
every push to `main` that touches `docs/` or `website/`. Enable it once in the
repository under Settings -> Pages by setting Source to "GitHub Actions".
The site URL is `https://astaxie.github.io/TokenHub/`; override `DOCS_URL` and
`DOCS_BASE_URL` when publishing somewhere else.

"Edit this page" links are generated from `scripts/docs-source-map.json`
(written by the sync script) and point at the source markdown file in the
repository — including its language directory — not at the generated copy.

## Adding a page

1. Write the page under `../docs` (English) and its `docs/zh-CN` and `docs/ja`
   counterparts, following the repository documentation guidelines.
2. Add an entry for the English file to a section in `SECTIONS` within
   `scripts/sync-docs.mjs`; section order defines the sidebar. Mermaid
   flowcharts in the source render out of the box.
3. Run `npm start` to preview.
