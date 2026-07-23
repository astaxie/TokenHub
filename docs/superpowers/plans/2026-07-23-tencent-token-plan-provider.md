# Tencent Token Plan Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add all Tencent Cloud Token Plan variants to the Provider catalog and require an explicit administrator acknowledgement before creating a personal-plan upstream.

**Architecture:** Provider catalog entries carry optional acknowledgement metadata. The backend validates acknowledgement only on provider creation, persists it in `Provider.Options`, and relies on the existing provider audit event. The creation wizard renders the catalog-supplied notice and does not submit an unacknowledged personal provider.

**Tech Stack:** Go, net/http, GORM/SQLite, Next.js, React, TypeScript, CSS, Go test.

---

### Task 1: Catalog and Server Enforcement

**Files:**
- Modify: `backend/internal/server/types.go:169-198`
- Modify: `backend/internal/server/provider_catalog.go:45-80,283-358`
- Modify: `backend/internal/server/http.go:2564-2591`
- Test: `backend/internal/server/http_test.go:2615-2754`

- [ ] **Step 1: Write tests for the four entries and creation acknowledgement**

```go
func TestTencentTokenPlanCatalogEntries(t *testing.T) {
	entries := tencentTokenPlanCatalogEntries()
	if len(entries) != 4 { t.Fatalf("expected four entries, got %d", len(entries)) }
	personal := findProviderCatalogEntry(entries, "tencent-token-plan-general-personal")
	if !personal.RequiresAcknowledgement || personal.AcknowledgementVersion == "" {
		t.Fatalf("expected personal acknowledgement metadata: %#v", personal)
	}
	if findProviderCatalogEntry(entries, "tencent-token-plan-enterprise-pro").RequiresAcknowledgement {
		t.Fatal("enterprise Token Plan must not require acknowledgement")
	}
}

func TestPersonalTokenPlanProviderRequiresAcknowledgement(t *testing.T) {
	withProviderCatalogCache(t, tencentTokenPlanCatalogEntries())
	app := newTestServer()
	missing := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id": "tencent-token-plan-general-personal", "name": "Personal", "status": "active",
	}, "")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body, "provider_terms_acknowledgement_required") {
		t.Fatalf("expected acknowledgement error, got %d: %s", missing.Code, missing.Body)
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id": "tencent-token-plan-general-personal", "name": "Personal", "status": "active", "acknowledged_catalog_terms": true,
	}, "")
	if created.Code != http.StatusCreated || !strings.Contains(created.Body, "catalog_terms_acknowledged_at") {
		t.Fatalf("expected acknowledged provider, got %d: %s", created.Code, created.Body)
	}
}
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./internal/server -run 'TestTencentTokenPlan|TestPersonalTokenPlan' -count=1`

Expected: FAIL because the Tencent catalog helper and acknowledgement request field do not exist.

- [ ] **Step 3: Add the request and catalog fields**

```go
type ProviderCatalogEntry struct {
	// Existing fields.
	RequiresAcknowledgement bool   `json:"requires_acknowledgement,omitempty"`
	AcknowledgementTitle    string `json:"acknowledgement_title,omitempty"`
	AcknowledgementMessage  string `json:"acknowledgement_message,omitempty"`
	AcknowledgementVersion  string `json:"acknowledgement_version,omitempty"`
}

type ProviderCreateRequest struct {
	// Existing fields.
	AcknowledgedCatalogTerms bool `json:"acknowledged_catalog_terms"`
}
```

- [ ] **Step 4: Make the four templates available in every catalog load mode**

```go
const (
	tencentTokenPlanEnterpriseBaseURL = "https://tokenhub.tencentmaas.com/plan/v3"
	tencentTokenPlanPersonalBaseURL   = "https://api.lkeap.cloud.tencent.com/plan/v3"
	tencentTokenPlanTermsVersion      = "2026-07-23"
)

func tencentTokenPlanCatalogEntries() []ProviderCatalogEntry {
	terms := func(title string) ProviderCatalogEntry {
		return ProviderCatalogEntry{
			RequiresAcknowledgement: true,
			AcknowledgementTitle: title,
			AcknowledgementMessage: "个人套餐由管理员自行确认适用性与风险后接入。",
			AcknowledgementVersion: tencentTokenPlanTermsVersion,
		}
	}
	professional := tencentTokenPlanCatalogEntry("tencent-token-plan-enterprise-pro", "腾讯云 Token Plan / 企业版专业套餐", tencentTokenPlanEnterpriseBaseURL, []string{"auto", "glm-5.2", "glm-5.1", "glm-5-turbo", "kimi-k2.7-code", "kimi-k2.6", "minimax-m3", "deepseek-v4-flash", "deepseek-v4-pro"})
	auto := tencentTokenPlanCatalogEntry("tencent-token-plan-enterprise-auto", "腾讯云 Token Plan / 企业版轻享套餐", tencentTokenPlanEnterpriseBaseURL, []string{"auto"})
	general := tencentTokenPlanCatalogEntry("tencent-token-plan-general-personal", "腾讯云 Token Plan / 通用 Token Plan（个人版）", tencentTokenPlanPersonalBaseURL, []string{"tc-code-latest", "glm-5.1", "kimi-k2.5", "minimax-m2.7", "deepseek-v4-flash-202605", "deepseek-v4-pro-202606"})
	hy := tencentTokenPlanCatalogEntry("tencent-token-plan-hy-personal", "腾讯云 Token Plan / Hy Token Plan（个人版）", tencentTokenPlanPersonalBaseURL, []string{"hy3", "hy3-preview"})
	personalTerms := terms("个人 Token Plan 使用确认")
	general.RequiresAcknowledgement = personalTerms.RequiresAcknowledgement
	general.AcknowledgementTitle = personalTerms.AcknowledgementTitle
	general.AcknowledgementMessage = personalTerms.AcknowledgementMessage
	general.AcknowledgementVersion = personalTerms.AcknowledgementVersion
	hy.RequiresAcknowledgement = personalTerms.RequiresAcknowledgement
	hy.AcknowledgementTitle = personalTerms.AcknowledgementTitle
	hy.AcknowledgementMessage = personalTerms.AcknowledgementMessage
	hy.AcknowledgementVersion = personalTerms.AcknowledgementVersion
	return []ProviderCatalogEntry{professional, auto, general, hy}
}

func tencentTokenPlanCatalogEntry(id string, name string, baseURL string, modelIDs []string) ProviderCatalogEntry {
	entry := builtinCatalogEntry(id, name, ProviderOpenAICompatible, baseURL, "https://cloud.tencent.com/document/product/1823/130660", modelIDs)
	entry.Source = "builtin"
	return entry
}

func mergeProviderCatalogEntries(entries []ProviderCatalogEntry, additions []ProviderCatalogEntry) []ProviderCatalogEntry {
	byID := make(map[string]int, len(entries))
	for index, entry := range entries { byID[entry.ID] = index }
	for _, entry := range additions {
		if index, ok := byID[entry.ID]; ok { entries[index] = entry; continue }
		byID[entry.ID] = len(entries)
		entries = append(entries, entry)
	}
	return entries
}
```

Merge Tencent entries with a successful remote catalog before adding the custom entry. Add the same entries in `builtinProviderCatalog` so the fallback catalog is identical.

- [ ] **Step 5: Enforce acknowledgement only on POST and persist audit metadata**

```go
func validateProviderCatalogAcknowledgement(catalog ProviderCatalogEntry, acknowledged bool) error {
	if catalog.RequiresAcknowledgement && !acknowledged {
		return NewHTTPError(http.StatusBadRequest, "provider_terms_acknowledgement_required", "Provider terms must be acknowledged before creating this provider")
	}
	return nil
}

func applyProviderCatalogAcknowledgement(provider *Provider, catalog ProviderCatalogEntry, now time.Time) {
	if !catalog.RequiresAcknowledgement { return }
	if provider.Options == nil { provider.Options = map[string]string{} }
	provider.Options["catalog_terms_acknowledged"] = "true"
	provider.Options["catalog_terms_acknowledged_at"] = now.UTC().Format(time.RFC3339)
	provider.Options["catalog_terms_version"] = catalog.AcknowledgementVersion
}
```

Call both functions in `handleAdminProviders` after `providerFromCreateRequest` and before `AddProvider`. Leave the PATCH handler unchanged.

- [ ] **Step 6: Run backend tests and commit**

Run: `go test ./internal/server -count=1`

Expected: PASS.

```bash
git add backend/internal/server/types.go backend/internal/server/provider_catalog.go backend/internal/server/http.go backend/internal/server/http_test.go
git commit -m "feat: add Tencent Token Plan provider catalog"
```

### Task 2: Provider Wizard Confirmation

**Files:**
- Modify: `frontend/app/page.tsx:123-135,13362-14331,16365-16379`
- Modify: `frontend/app/globals.css`

- [ ] **Step 1: Extend the browser catalog type and request payload**

```ts
type ProviderCatalogEntry = {
  // Existing fields.
  requires_acknowledgement?: boolean;
  acknowledgement_title?: string;
  acknowledgement_message?: string;
  acknowledgement_version?: string;
};

function providerPayload(values: Record<string, string>) {
  return {
    // Existing payload fields.
    acknowledged_catalog_terms: values.acknowledged_catalog_terms === "true",
  };
}
```

- [ ] **Step 2: Add acknowledgement state and validate it twice**

```ts
const [acknowledgedCatalogTerms, setAcknowledgedCatalogTerms] = useState(false);
const requiresCatalogAcknowledgement = mode === "create" && selectedEntry?.requires_acknowledgement === true;

function catalogTermsReady() {
  return !requiresCatalogAcknowledgement || acknowledgedCatalogTerms;
}
```

Reset it in `selectCatalog` and `selectCustomCatalog`. Reject credential step navigation and final submission when `catalogTermsReady()` is false.

- [ ] **Step 3: Render the catalog-provided warning in the credential step**

```tsx
{requiresCatalogAcknowledgement ? (
  <section className="provider-terms-notice" role="alert">
    <strong>{selectedEntry?.acknowledgement_title || tx("个人套餐使用确认")}</strong>
    <p>{selectedEntry?.acknowledgement_message}</p>
    <label>
      <input checked={acknowledgedCatalogTerms} onChange={(event) => setAcknowledgedCatalogTerms(event.target.checked)} type="checkbox" />
      <span>{tx("我已阅读并确认由本组织自行判断该套餐的适用性与风险。")}</span>
    </label>
  </section>
) : null}
```

Pass `acknowledged_catalog_terms` through `providerPayload`, disable final save until confirmation exists, and add a confirmation review row.

- [ ] **Step 4: Add compact warning styles using existing variables**

```css
.provider-terms-notice { border: 1px solid color-mix(in srgb, var(--warn) 36%, var(--border)); border-left: 3px solid var(--warn); border-radius: 6px; background: var(--warn-weak); padding: 14px; }
.provider-terms-notice label { display: flex; align-items: flex-start; gap: 8px; margin-top: 10px; }
```

Use the closest existing warning variable after inspecting `frontend/app/globals.css`; do not create a new color system.

- [ ] **Step 5: Run frontend verification and commit**

Run: `npm run lint --prefix frontend`

Expected: PASS with no TypeScript or lint errors.

```bash
git add frontend/app/page.tsx frontend/app/globals.css
git commit -m "feat: confirm personal Token Plan providers"
```

### Task 3: Full Verification

**Files:**
- Modify: `docs/superpowers/specs/2026-07-23-tencent-token-plan-provider-design.md` only if verification finds an inaccuracy.

- [ ] **Step 1: Run the complete backend suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Build the frontend production bundle**

Run: `npm run build --prefix frontend`

Expected: Next.js build completes successfully.

- [ ] **Step 3: Inspect the final diff and working tree**

Run: `git diff --check && git status --short`

Expected: no whitespace errors; only intended implementation changes and pre-existing user changes remain unstaged.
