# Hermes Tabler Redesign Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Replace the full Hermes web console presentation with locally bundled Tabler components while preserving every existing Go, htmx, accessibility, and operational behavior contract.

**Architecture:** `@tabler/core` and `@tabler/icons-webfont` are development dependencies whose CSS is bundled into the existing embedded `app.css`; Tabler JavaScript and icon fonts are copied into `internal/web/static` for an offline runtime. Go templates adopt Tabler's vertical admin shell and component classes while stable IDs, `data-*`, `hx-*`, form routes, and partial template names remain unchanged. Hermes-specific CSS remains only for brand tokens, mobile data tables, logs, dialogs, loading feedback, long values, and other behavior Tabler does not provide.

**Tech Stack:** Go 1.25, `html/template`, htmx 2, Tabler, Tabler Icons webfont, Tailwind CLI 4 as the CSS bundler, Node's built-in test runner.

---

### Task 1: Vendor Tabler Assets Locally

**Files:**
- Create: `scripts/sync-tabler-assets.mjs`
- Modify: `package.json`
- Modify: `package-lock.json`
- Modify: `Makefile`
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/web_test.go`
- Generate: `internal/web/static/app.css`
- Generate: `internal/web/static/tabler.min.js`
- Generate: `internal/web/static/fonts/*`

**Step 1: Write the failing embedded-asset test**

Replace the Tailwind-banner-specific assertions in `TestStaticAppCSSIsEmbedded`
with `TestTablerAssetsAreEmbedded`. Assert that:

- `static/app.css` contains `--tblr-`, `.navbar-vertical`, and Hermes compatibility selectors.
- `static/tabler.min.js` exists and contains the Tabler/Bootstrap collapse implementation.
- `static/fonts/tabler-icons.woff2` exists and is non-empty.
- the generated stylesheet still contains the narrow table, reduced motion, focus,
  stable busy indicator, and bounded log rules.

**Step 2: Run the test and verify RED**

```bash
go test ./internal/web -run 'TestTablerAssetsAreEmbedded' -count=1 -v
```

Expected: FAIL because Tabler assets and CSS tokens are absent.

**Step 3: Add dependencies and deterministic asset synchronization**

Install `@tabler/core` and `@tabler/icons-webfont` as dev dependencies. Add
`scripts/sync-tabler-assets.mjs` using `node:fs/promises` to copy:

- `node_modules/@tabler/core/dist/js/tabler.min.js` to
  `internal/web/static/tabler.min.js`.
- every file under `node_modules/@tabler/icons-webfont/dist/fonts` to
  `internal/web/static/fonts`.

The script must fail with a path-specific error if an expected source disappears.
Update package scripts so `css:build` and `css:watch` run `assets:vendor` first.

Use explicit CSS layers and omit Tailwind Preflight:

```css
@layer theme, tabler, base, components, utilities;
@import "tailwindcss/theme.css" layer(theme);
@import "@tabler/core/dist/css/tabler.css" layer(tabler);
@import "@tabler/icons-webfont/dist/tabler-icons.css" layer(tabler);
@import "tailwindcss/utilities.css" layer(utilities);
```

Keep the current Hermes rules temporarily so existing pages remain usable during
the staged migration. Update `make check` to rebuild and diff all generated web
assets, not only `app.css`.

**Step 4: Build assets and verify GREEN**

```bash
npm run css:build
go test ./internal/web -run 'TestTablerAssetsAreEmbedded' -count=1 -v
```

Expected: PASS, including the font embedding assertion.

**Step 5: Commit**

```bash
git add package.json package-lock.json Makefile scripts/sync-tabler-assets.mjs internal/web/assets/app.css internal/web/static internal/web/web_test.go
git commit -m "📦 build(web): bundle Tabler assets locally"
```

---

### Task 2: Build The Responsive Tabler Application Shell

**Files:**
- Modify: `internal/web/templates/layout.html`
- Modify: `internal/web/templates/login.html`
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css`
- Modify: `internal/web/web_test.go`

**Step 1: Write failing shell contract tests**

Add a class-token assertion helper so class order does not affect tests. Replace
`TestRenderLayoutLoadsAppStylesheet` with `TestLayoutUsesTablerAdminShell`, checking:

- authenticated output has `page`, `navbar`, `navbar-vertical`, `navbar-expand-lg`,
  `page-wrapper`, and `page-body`.
- the mobile toggler targets one sidebar collapse ID and exposes `aria-controls`.
- the active destination has both `active` and `aria-current="page"`.
- `/static/tabler.min.js` loads with `defer` before `/static/app.js`.
- all four routes, logout form, skip link, main target, and native confirm dialog
  keep their existing behavior hooks.

Add `TestLoginUsesTablerAuthShell`, asserting `page-center`, `container-tight`, and
one login `card`, while authenticated navigation, logout, and confirmation remain
absent. Add icon accessibility assertions: decorative icons are `aria-hidden`, and
any icon-only control has an accessible name.

**Step 2: Run focused tests and verify RED**

```bash
go test ./internal/web -run 'Test(LayoutUsesTablerAdminShell|LoginUsesTablerAuthShell|TablerIconsRemainAccessible)' -count=1 -v
```

Expected: FAIL on missing Tabler shell classes and asset reference.

**Step 3: Implement the shell**

Build the authenticated layout as a Tabler vertical sidebar with a restrained top
bar, responsive collapse, four icon-and-text navigation links, and logout action.
Use the login branch as a centered auth page with Hermes branding and a single
purpose card. Keep `#main-content`, `.ActiveNav`, `.HideNav`, dialog IDs and ARIA,
script order for htmx/metadata/feedback/application code, and all POST/loading
behavior. Add only shell, brand, skip-link, dialog, and focus overrides to CSS.

**Step 4: Build and verify GREEN**

```bash
npm run css:build
go test ./internal/web -run 'Test(LayoutUsesTablerAdminShell|LoginUsesTablerAuthShell|TablerIconsRemainAccessible|LoginLayoutHidesAuthenticatedNavigation|LayoutProvidesSkipLinkAndDialog)' -count=1 -v
npm run js:test
```

Expected: PASS with 29 JavaScript tests unchanged.

**Step 5: Commit**

```bash
git add internal/web/templates/layout.html internal/web/templates/login.html internal/web/assets/app.css internal/web/static/app.css internal/web/web_test.go
git commit -m "💄 feat(web): add responsive Tabler console shell"
```

---

### Task 3: Migrate Lists, Tables, Statuses, And Row Partials

**Files:**
- Modify: `internal/web/templates/accounts.html`
- Modify: `internal/web/templates/projects.html`
- Modify: `internal/web/templates/blueprints.html`
- Modify: `internal/web/templates/environments.html`
- Modify: `internal/web/templates/_account_rows.html`
- Modify: `internal/web/templates/_fragments.html`
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css`
- Modify: `internal/web/web_test.go`

**Step 1: Write failing Tabler list tests**

Update table assertions to test class tokens rather than exact class strings. Add
`TestListPagesUseTablerTables` and `TestStatusBadgesUseTablerTonesAndText`, covering:

- `card`, `table-responsive`, `table`, `table-vcenter`, and compact action groups.
- existing captions, `scope="col"`, `data-label`, empty-state guidance, and row
  target IDs.
- `badge` plus localized text for pending, active, ready, failed, and destroyed
  states; status must never become color-only.
- delete links retain their no-JavaScript hrefs, `hx-delete`, targets, swaps,
  confirmation messages, and loading labels.

**Step 2: Run focused tests and verify RED**

```bash
go test ./internal/web -run 'Test(ListPagesUseTablerTables|StatusBadgesUseTablerTonesAndText)' -count=1 -v
```

Expected: FAIL because lists still use custom table/button/badge classes.

**Step 3: Implement Tabler list components**

Convert the four list pages and account/project/blueprint/job row partials to
Tabler page headers, cards, tables, badges, buttons, and empty states. Use Tabler
icons for create, inspect, edit, copy, deploy, and delete actions while retaining
clear text labels. Preserve every partial name, row target ID, polling attribute,
stable `job-detail-*` ID, `long-value` hook, caption, and narrow-screen `data-label`.
Keep a Hermes mobile-table override because Tabler's horizontal table alone is too
dense on small screens.

**Step 4: Build and verify GREEN**

```bash
npm run css:build
go test ./internal/web -run 'Test(ListPagesUseTablerTables|StatusBadgesUseTablerTonesAndText|TablesHaveCaptionsAndScopedHeaders|DestructiveActionsUseSharedConfirmation|RenderPartialJobHistoryUsesStableUniqueDetailLinkIDs|LongIdentifiersUseWrappingUtilities)' -count=1 -v
npm run js:test
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/web/templates/accounts.html internal/web/templates/projects.html internal/web/templates/blueprints.html internal/web/templates/environments.html internal/web/templates/_account_rows.html internal/web/templates/_fragments.html internal/web/assets/app.css internal/web/static/app.css internal/web/web_test.go
git commit -m "💄 feat(web): migrate resource lists to Tabler"
```

---

### Task 4: Migrate Forms, Details, And Destructive Workflows

**Files:**
- Modify: `internal/web/templates/account_form.html`
- Modify: `internal/web/templates/project_form.html`
- Modify: `internal/web/templates/account_delete.html`
- Modify: `internal/web/templates/project_delete.html`
- Modify: `internal/web/templates/blueprint_detail.html`
- Modify: `internal/web/templates/blueprint_deploy.html`
- Modify: `internal/web/templates/blueprint_delete.html`
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css`
- Modify: `internal/web/web_test.go`

**Step 1: Write failing form and workflow tests**

Add `TestFormsUseTablerValidation` and `TestDestructivePagesUseTablerDangerZones`.
Assert `form-label`, `form-control`/`form-select`, `form-hint`, `is-invalid`, and
`invalid-feedback` while retaining explicit labels, error live regions,
`aria-invalid`, and `aria-describedby`. Assert delete pages have a danger alert,
target summary, POST route, loading label, and safe cancel link. Add detail/deploy
assertions for Tabler description lists, action hierarchy, and long-name wrapping.

**Step 2: Run focused tests and verify RED**

```bash
go test ./internal/web -run 'Test(FormsUseTablerValidation|DestructivePagesUseTablerDangerZones|BlueprintDetailUsesTablerSummary)' -count=1 -v
```

Expected: FAIL on missing Tabler form, summary, and danger classes.

**Step 3: Implement the simple workflows**

Convert account/project forms, three fallback delete pages, blueprint detail, and
blueprint deployment to consistent Tabler cards, form groups, validation states,
description grids, buttons, and alerts. Keep the password toggle as text so the
existing `textContent` update remains correct. Preserve all actions, methods, names,
IDs, error relationships, loading labels, and maximum-length wrapping hooks.

**Step 4: Build and verify GREEN**

```bash
npm run css:build
go test ./internal/web -run 'Test(FormsUseTablerValidation|DestructivePagesUseTablerDangerZones|BlueprintDetailUsesTablerSummary|FormsHaveExplicitLabelsAndErrorRegions|BlueprintDeleteWrapsMaximumLengthName|RenderedNativeSubmitButtonsReserveBusyIndicatorSpace)' -count=1 -v
npm run js:test
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/web/templates/account_form.html internal/web/templates/project_form.html internal/web/templates/account_delete.html internal/web/templates/project_delete.html internal/web/templates/blueprint_detail.html internal/web/templates/blueprint_deploy.html internal/web/templates/blueprint_delete.html internal/web/assets/app.css internal/web/static/app.css internal/web/web_test.go
git commit -m "💄 feat(web): migrate forms and detail workflows"
```

---

### Task 5: Migrate Blueprint Editing And Operational Surfaces

**Files:**
- Modify: `internal/web/templates/blueprint_form.html`
- Modify: `internal/web/templates/environment_detail.html`
- Modify: `internal/web/templates/job_detail.html`
- Modify: `internal/web/templates/_fragments.html`
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css`
- Modify: `internal/web/web_test.go`

**Step 1: Write failing complex-surface tests**

Add `TestBlueprintEditorUsesTablerSections` and
`TestOperationalPagesUseTablerComponents`. Assert:

- blueprint ownership, compute, VPC, RDS, and Redis sections use consistent Tabler
  form/card structure without nested cards.
- metadata selectors retain every `hx-*`, `data-metadata-source`, and hidden
  selection-hint contract.
- disclosures retain no-JavaScript fallback content, `.disclosure-toggle`, expanded
  state, and target relationships.
- environment status, output blocks, history, job diagnostics, failure alerts,
  copy action, and log panels use Tabler hierarchy while preserving streaming,
  polling, credential, and confirmation hooks.

**Step 2: Run focused tests and verify RED**

```bash
go test ./internal/web -run 'Test(BlueprintEditorUsesTablerSections|OperationalPagesUseTablerComponents)' -count=1 -v
```

Expected: FAIL on missing Tabler structure.

**Step 3: Implement the complex surfaces**

Migrate the blueprint editor as one unit, keeping the server-rendered fields visible
until JavaScript enhancement runs. Use Tabler form checks, selects, validation,
section headers, and compact responsive grids. Migrate environment/job pages and
all status/output/credential fragments to Tabler alerts, badges, cards, tables, and
buttons. Keep log panels visually distinct and bounded; keep status/copy nodes in
the parent relationships expected by `app.js`. Do not change API data or JavaScript
hook names.

**Step 4: Build and verify GREEN**

```bash
npm run css:build
go test ./internal/web -count=1
npm run js:test
```

Expected: all web and 29 JavaScript tests PASS.

**Step 5: Commit**

```bash
git add internal/web/templates/blueprint_form.html internal/web/templates/environment_detail.html internal/web/templates/job_detail.html internal/web/templates/_fragments.html internal/web/assets/app.css internal/web/static/app.css internal/web/web_test.go
git commit -m "💄 feat(web): finish Tabler workflow migration"
```

---

### Task 6: Visual Hardening And Full Verification

**Files:**
- Modify: `internal/web/assets/app.css`
- Modify: `internal/web/static/app.css`
- Modify: `internal/web/templates/*.html` only where browser findings require fixes
- Modify: `internal/web/web_test.go` only for reproduced regressions
- Modify: `DESIGN.md`

**Step 1: Run deterministic checks**

```bash
npm run css:build
npm run js:test
go test ./...
make check
git diff --check
```

Expected: PASS with no generated-asset diff.

**Step 2: Start Hermes and inspect representative states**

Run the local server with the worktree's configured environment. In the browser,
verify login plus authenticated accounts, projects, blueprints, blueprint form,
environments, environment detail, and job detail at 1440x900, 1024x768, 768x1024,
and 390x844.

Check that:

- the sidebar collapses and reopens without console errors;
- no text, action group, table row, dialog, or form overlaps or overflows;
- long IDs, errors, plans, output values, and logs remain readable;
- keyboard focus, skip link, field errors, loading state, and confirmation work;
- mobile list rows retain labels and stable dimensions;
- reduced motion does not leave content hidden;
- Tabler CSS, icons, fonts, and JavaScript load locally with no failed requests.

Use screenshots and page measurements to reproduce any defect as a focused test
before fixing it. Keep browser-discovered changes strictly within the approved UI
scope.

**Step 3: Update the design system record**

Update `DESIGN.md` to state that Tabler is the component foundation, document the
Hermes override layer and retained compatibility hooks, and remove claims that the
console uses custom Tailwind primitives only.

**Step 4: Re-run final verification and commit**

```bash
npm run css:build
npm run js:test
go test ./...
make check
git diff --check
```

Expected: all commands PASS and browser console/network checks are clean.

```bash
git add DESIGN.md internal/web
git commit -m "✅ test(web): harden Tabler console across viewports"
```

