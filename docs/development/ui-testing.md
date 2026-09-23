# Database-free UI acceptance

This suite runs the real Next.js console against synthetic API fixtures. It starts neither Go nor a database and does not use an existing development server. Use it for page interaction checks and review screenshots. Existing component tests and backend integration E2E remain separate.

## Run locally

From `frontend/`, use the Node version in `.nvmrc`, install dependencies with `npm ci`, and install Chromium once with `npx playwright install chromium`.

```bash
npm run test:ui
npm run capture:ui
# Run only the affected scenarios:
npm run test:ui -- --grep customer-complete
npm run capture:ui -- --grep billing
# Inspect the scenario list without building or starting a server:
npm run test:ui -- --list
```

Both commands build into `.next-ui` and launch the standalone frontend on `127.0.0.1:43210`. They execute the same tests; `capture:ui` additionally attaches screenshots and creates `ui-test-results/index.html` and `manifest.json`. Open the HTML locally and click an image for its original size. The manifest records the source commit, dirty-worktree state and test outcomes. Copy the whole artifact directory to preserve a run; subsequent runs replace it.

The runner restores the generated import in `next-env.d.ts`, preserves unrelated edits, and uses `.ui-test.lock` to exclude concurrent UI runs in the same checkout. Run builds and other browser suites sequentially in that checkout. A port conflict fails rather than reusing another server. After an interrupted run, check that no UI runner is active before removing a stale lock.

## Isolation and evidence

`tests/ui/config.cjs` fixes the API origin to `http://tokenhub-ui.invalid`. Playwright intercepts declared method/path pairs before they reach the network. The automatic fixture fails on missing handlers, invalid request payloads, unexpected external requests, same-origin API fallbacks, WebSockets, or uncaught page errors. Service workers are blocked. Normal frontend assets, documents and RSC requests remain local. Query strings fail by default; an endpoint with a query must provide an explicit validator to `MockAPI.define`.

Each test receives a new browser context, fixture registry, and synthetic administrator session. Stateful mock writes live only in that test and subsequent fixture reads reflect them. No production authentication bypass or mock mode is added to the application. Inherited backend connection settings are removed from the runner environment.

The current console reads business APIs in the browser. Browser interception does not intercept server-side fetches: if a future server component needs business data, provide an explicit test-only transport before adding that scenario. Keep UI runs independent of live services; do not add a real-backend fallback.

These tests establish UI behavior only. A sample response containing an amount does not prove billing arithmetic, authorization enforcement, transactionality or persistence. Shared TypeScript DTOs check fixture shapes; they do not replace backend contract tests.

## Maintain a scenario

- `fixtures/shell.ts` declares the shell's required reads. Add an endpoint only when the page actually needs it; there is no catch-all successful empty response.
- `fixtures/billing.ts` uses the existing statement DTOs. Keep IDs, amounts, dates and identities synthetic. Clone data per test and use small, named examples.
- `scenarios/billing.ts` lists the parameterized statement states. `billing.spec.ts` and `pricing.spec.ts` drive actual controls and assert visible outcomes and relevant outgoing requests.
- `harness.ts` installs isolation, fixes time/language and provides the shared capture helper. `isolation.spec.ts` verifies the guard itself rejects missing routes, wrong methods, bad payloads and network fallbacks.

For an affected page, cover its normal path and relevant empty, loading, failure, permission or long-content states. Prefer role/label selectors; scope repeated controls to their section. Assert the state before capturing it. Control loading with a releasable response instead of a timing sleep. Never recreate the production billing algorithm inside a fixture.

Initial coverage includes customer/provider/margin statements, unknown versus zero amounts, empty/loading/error states, CSV from the displayed snapshot, filter invalidation, mobile actions, and the base branch's pricing preview/shadow-publication flow. The gallery adapts the earlier hybrid billing screenshot experiment; its scenarios target the checked-out product, not unmerged pricing redesigns.

## Review screenshots

Time, timezone, language, viewport and motion are controlled. The capture helper waits for fonts and captures the relevant section. It rejects sections taller than the viewport, which must use explicitly positioned, named viewport segments to avoid internal-scroll clipping. Desktop scenarios use 1440×1000; mobile form/result segments use 390×844. Test names and diagnostics are English; the human-facing gallery captions are Chinese. Native controls and system fonts can vary between operating systems.

Review every changed screenshot for readable text, visible actions, clipping and correct state. Keep passing and failing runs distinguishable; only a passing run is acceptance evidence. Attach or share the generated gallery when useful, rather than checking runtime images into source control.

This first stage adds no CI job, image baselines, automatic screenshot comparison, Storybook or MSW dependency. Pixel comparisons can be added later with reviewed baselines and a fixed browser/font/OS environment. Existing Go/database/browser CI checks are unchanged and continue to validate their own contracts.
