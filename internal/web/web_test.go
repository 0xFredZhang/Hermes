package web

import (
	"bytes"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestNewRendererParsesAllPages(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	for _, name := range []string{"login", "accounts", "account_form", "account_delete", "projects", "project_form", "project_delete", "blueprints", "blueprint_form", "blueprint_detail", "blueprint_deploy", "blueprint_delete", "environments", "environment_detail", "job_detail"} {
		if r.pages[name] == nil {
			t.Fatalf("page %q not parsed", name)
		}
	}
}

func TestRendererDoesNotWritePartialSuccessOnTemplateError(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	w := httptest.NewRecorder()
	r.Render(w, "blueprint_form", map[string]any{"PageTitle": "broken"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<!doctype html>") {
		t.Fatalf("renderer wrote partial success before error: %s", w.Body.String())
	}
}

func TestTablerAssetsAreEmbedded(t *testing.T) {
	css, err := StaticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("static app stylesheet must be embedded: %v", err)
	}
	for _, want := range []string{
		"--tblr-", ".navbar-vertical", ".app-shell", ".skip-link", ".form-surface", ".password-control",
		"dialog::backdrop", `content:attr(data-label)`, `.resource-list-card,.job-history-card{border-color:`, `:focus-visible`,
		`@media (max-width:39.999rem)`, `.responsive-table-wrap{border:0`,
		`@media (prefers-reduced-motion:reduce)`, `button[data-loading-label][aria-busy=true]:after`,
		`.blueprint-form-page`, `.summary-grid`, `.disclosure-toggle`,
		`.job-history-wrap`, `.badge.status-active`, `.diagnostic-grid`, `.log-panel{max-height:420px`, `.log-panel-full{max-height:70vh`, `.copy-log-status`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("static app stylesheet missing %q", want)
		}
	}
	mobileStart := strings.Index(string(css), `@media (max-width:39.999rem)`)
	mobileEnd := -1
	if mobileStart != -1 {
		if offset := strings.Index(string(css)[mobileStart:], `@media (min-width:40rem)`); offset != -1 {
			mobileEnd = mobileStart + offset
		}
	}
	if mobileStart == -1 || mobileEnd <= mobileStart {
		t.Fatal("generated stylesheet is missing the bounded mobile workflow rules")
	}
	mobileCSS := string(css)[mobileStart:mobileEnd]
	for _, selector := range []string{`.workflow-card-header`, `.resource-list-card .card-header`} {
		rule := regexp.MustCompile(regexp.QuoteMeta(selector) + `\{([^}]*)\}`).FindStringSubmatch(mobileCSS)
		if len(rule) != 2 || !strings.Contains(rule[1], `flex-direction:column`) || !strings.Contains(rule[1], `align-items:stretch`) {
			t.Errorf("mobile rule %q must stack and stretch its header: %s", selector, mobileCSS)
		}
	}

	js, err := StaticFS.ReadFile("static/tabler.min.js")
	if err != nil {
		t.Fatalf("static Tabler script must be embedded: %v", err)
	}
	for _, want := range []string{"Collapse", "data-bs-toggle"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("static Tabler script missing collapse implementation marker %q", want)
		}
	}

	font, err := StaticFS.ReadFile("static/fonts/tabler-icons.woff2")
	if err != nil {
		t.Fatalf("static Tabler icon font must be embedded: %v", err)
	}
	if len(font) == 0 {
		t.Fatal("static Tabler icon font must not be empty")
	}
}

func TestTablerSidebarCollapseVisibilityIsSafe(t *testing.T) {
	css := readStaticSource(t, "app.css")
	if !strings.Contains(css, `.collapse:not(.show){display:none}`) {
		t.Fatal("generated stylesheet is missing Tabler's collapse display rule")
	}
	if strings.Contains(css, `.collapse{visibility:collapse}`) {
		t.Fatal("Tailwind visibility utility overrides Tabler's sidebar collapse visibility")
	}
}

func TestTailwindBuildPreservesTablerUtilityClasses(t *testing.T) {
	sourceBytes, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read source stylesheet: %v", err)
	}
	source := string(sourceBytes)
	if strings.Contains(source, `tailwindcss/utilities.css`) {
		t.Error("source stylesheet imports Tailwind's utility layer after Tabler")
	}
	if regexp.MustCompile(`(?m)^@source\b`).MatchString(source) {
		t.Error("source stylesheet scans templates even though Hermes utilities are expanded through @apply")
	}

	generated := readStaticSource(t, "app.css")
	for name, expected := range map[string]string{
		"width":        `.w-100{width:100%!important}`,
		"table":        `.table,.markdown>table{`,
		"border":       `.border{border:var(--tblr-border-width) var(--tblr-border-style) var(--tblr-border-color-translucent)!important}`,
		"radius":       `.rounded{border-radius:var(--tblr-border-radius)!important}`,
		"start margin": `.ms-auto{margin-left:auto!important}`,
		"spacing":      `.mb-4{margin-bottom:1.5rem!important}`,
	} {
		if !strings.Contains(generated, expected) {
			t.Errorf("generated stylesheet is missing Tabler's %s utility: %s", name, expected)
		}
	}
	for name, unwanted := range map[string]string{
		"width":        `.w-100{width:calc(var(--spacing) * 100)}`,
		"table":        `.table{display:table}`,
		"border":       `.border{border-style:var(--tw-border-style);border-width:1px}`,
		"radius":       `.rounded{border-radius:.25rem}`,
		"start margin": `.ms-auto{margin-inline-start:auto}`,
		"spacing":      `.mb-4{margin-bottom:calc(var(--spacing) * 4)}`,
	} {
		if strings.Contains(generated, unwanted) {
			t.Errorf("generated Tailwind %s utility redefines Tabler: %s", name, unwanted)
		}
	}

	tightStart := strings.Index(generated, `.container-tight{`)
	if tightStart == -1 {
		t.Fatal("generated stylesheet is missing Tabler's container-tight rule")
	}
	if regexp.MustCompile(`\.container\{max-width:`).MatchString(generated[tightStart:]) {
		t.Error("a later Tailwind container rule overrides the 30rem Tabler login container")
	}
}

func TestPagePanelsPreserveTablerCardBackground(t *testing.T) {
	sourceBytes, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read source stylesheet: %v", err)
	}
	source := string(sourceBytes)
	if regexp.MustCompile(`(?s)\.page-panel\s*\{[^}]*bg-transparent`).MatchString(source) {
		t.Error("legacy page-panel styling overrides Tabler card backgrounds with transparency")
	}

	generated := readStaticSource(t, "app.css")
	if !regexp.MustCompile(`\.card\{[^}]*background-color:var\(--tblr-card-bg\)[^}]*\}`).MatchString(generated) {
		t.Fatal("generated Tabler card rule does not use --tblr-card-bg")
	}
	if regexp.MustCompile(`\.page-panel\{[^}]*(?:background|background-color):#0000`).MatchString(generated) {
		t.Error("generated page-panel rule overrides --tblr-card-bg with transparency")
	}
}

func TestHermesLinkOverridesPreserveTablerButtonSemantics(t *testing.T) {
	sourceBytes, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read source stylesheet: %v", err)
	}
	source := string(sourceBytes)
	baseStart := strings.Index(source, "@layer base {")
	componentsStart := strings.Index(source, "@layer components {")
	if baseStart == -1 || componentsStart <= baseStart {
		t.Fatal("source stylesheet does not contain ordered base and components layers")
	}
	baseLayer := source[baseStart:componentsStart]
	linkSelector := `a:not(.btn):not(.nav-link):not(.navbar-brand):not(.auth-brand):not(.button):not(.button-primary):not(.button-danger):not(.button-muted)`
	for _, selector := range []string{linkSelector + " {", linkSelector + ":hover {", linkSelector + ":active {"} {
		if !strings.Contains(baseLayer, selector) {
			t.Errorf("Hermes base link rule must exclude Tabler navigation and button-like links; missing %q", selector)
		}
	}
	if regexp.MustCompile(`(?m)^\s+a(?::(?:hover|active))?\s*\{`).MatchString(baseLayer) {
		t.Error("Hermes base layer still contains an unscoped anchor color rule")
	}

	generated := readStaticSource(t, "app.css")
	minifiedSelector := strings.ReplaceAll(linkSelector, " ", "")
	for _, want := range []string{
		minifiedSelector + `{color:var(--color-console-primary)`,
		minifiedSelector + `:hover{color:var(--color-console-primary-strong);text-underline-offset:3px;text-decoration:underline`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated stylesheet is missing scoped Hermes link rule %q", want)
		}
	}

	for name, pattern := range map[string]*regexp.Regexp{
		"primary":        regexp.MustCompile(`\.btn-primary\{[^}]*--tblr-btn-color:var\(--tblr-primary-fg,#fff\)[^}]*--tblr-btn-bg:var\(--tblr-primary\)[^}]*--tblr-btn-hover-color:var\(--tblr-primary-fg\)[^}]*--tblr-btn-hover-bg:var\(--tblr-primary-darken\)`),
		"outline danger": regexp.MustCompile(`\.btn-outline-danger,[^{]*\{[^}]*--tblr-btn-color:var\(--tblr-danger\)[^}]*--tblr-btn-bg:transparent[^}]*--tblr-btn-hover-color:var\(--tblr-danger-fg\)[^}]*--tblr-btn-hover-bg:var\(--tblr-danger\)`),
	} {
		if !pattern.MatchString(generated) {
			t.Errorf("generated Tabler %s button rule does not preserve contrasting foreground and hover colors", name)
		}
	}
}

func TestLegacyButtonCompatibilityStylesExcludeTablerButtons(t *testing.T) {
	sourceBytes, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatalf("read source stylesheet: %v", err)
	}
	source := string(sourceBytes)
	componentsStart := strings.Index(source, "@layer components {")
	if componentsStart == -1 {
		t.Fatal("source stylesheet does not contain a components layer")
	}
	components := source[componentsStart:]
	for _, selector := range []string{
		`.button,
  button:not(.btn) {`,
		`.button:not([aria-disabled="true"]):not([aria-busy="true"]):hover,
  button:not(.btn):not(:disabled):hover {`,
		`.button:not([aria-disabled="true"]):not([aria-busy="true"]):active,
  button:not(.btn):not(:disabled):active {`,
	} {
		if !strings.Contains(components, selector) {
			t.Errorf("legacy native button appearance must exclude Tabler .btn controls; missing %q", selector)
		}
	}
	for _, shared := range []string{`button:disabled,`, `button[aria-busy="true"],`, `button[data-loading-label]::after,`, `button[data-loading-label][aria-busy="true"]::after,`} {
		if !strings.Contains(components, shared) {
			t.Errorf("shared button behavior must continue to cover Tabler button elements; missing %q", shared)
		}
	}

	generated := readStaticSource(t, "app.css")
	if !strings.Contains(generated, `.button,button:not(.btn){min-height:`) {
		t.Error("generated legacy button rule does not exclude Tabler .btn controls")
	}
}

func TestWorkflowSubmitButtonsPreserveSemanticContrast(t *testing.T) {
	css := readStaticSource(t, "app.css")
	tests := []struct {
		name       string
		selector   string
		background string
	}{
		{name: "primary", selector: `.workflow-card .btn-primary`, background: `var(--color-console-primary)`},
		{name: "primary hover", selector: `.workflow-card .btn-primary:not(:disabled):not([aria-busy=true]):hover`, background: `var(--color-console-primary-strong)`},
		{name: "danger", selector: `.workflow-card .btn-danger`, background: `var(--color-console-danger)`},
		{name: "danger hover", selector: `.workflow-card .btn-danger:not(:disabled):not([aria-busy=true]):hover`, background: `var(--color-console-danger)`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := regexp.MustCompile(regexp.QuoteMeta(tc.selector) + `\{([^}]*)\}`).FindStringSubmatch(css)
			if len(rule) != 2 {
				t.Fatalf("generated CSS is missing scoped workflow button rule %q", tc.selector)
			}
			for _, want := range []string{`background-color:` + tc.background, `color:#fff`} {
				if !strings.Contains(rule[1], want) {
					t.Errorf("workflow %s button rule missing %q: %s", tc.name, want, rule[0])
				}
			}
		})
	}
}

func TestTablerFontReferencesMatchEmbeddedAssets(t *testing.T) {
	css, err := StaticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("static app stylesheet must be embedded: %v", err)
	}

	fontURLPattern := regexp.MustCompile(`url\((?:"|')?(\./fonts/[^?"')]+)`)
	referenced := make(map[string]struct{})
	for _, match := range fontURLPattern.FindAllStringSubmatch(string(css), -1) {
		referenced[match[1]] = struct{}{}
	}
	if len(referenced) == 0 {
		t.Fatal("static app stylesheet must reference at least one local icon font")
	}

	for fontURL := range referenced {
		fontPath := "static/" + strings.TrimPrefix(fontURL, "./")
		font, err := StaticFS.ReadFile(fontPath)
		if err != nil {
			t.Fatalf("referenced icon font %q must be embedded: %v", fontURL, err)
		}
		if len(font) == 0 {
			t.Fatalf("referenced icon font %q must not be empty", fontURL)
		}
	}

	entries, err := StaticFS.ReadDir("static/fonts")
	if err != nil {
		t.Fatalf("static icon font directory must be embedded: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("static icon font directory contains unexpected subdirectory %q", entry.Name())
		}
		fontURL := "./fonts/" + entry.Name()
		if _, ok := referenced[fontURL]; !ok {
			t.Fatalf("embedded icon font %q is not referenced by static app stylesheet", fontURL)
		}
	}
	if len(entries) != len(referenced) {
		t.Fatalf("embedded icon font count = %d, referenced font count = %d", len(entries), len(referenced))
	}
}

func TestMakeCheckDetectsUntrackedGeneratedAssets(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	for _, want := range []string{
		`status="$$(git status --porcelain --untracked-files=all -- internal/web/static/app.css internal/web/static/tabler.min.js internal/web/static/fonts)"`,
		`printf '%s\n' "$$status"`,
	} {
		if !strings.Contains(string(makefile), want) {
			t.Fatalf("Makefile generated asset check missing %q", want)
		}
	}
	if strings.Contains(string(makefile), "git diff --exit-code -- internal/web/static/app.css") {
		t.Fatal("Makefile generated asset check must not ignore untracked files")
	}
}

func TestStaticAppJSIsEmbeddedWithProgressiveEnhancements(t *testing.T) {
	js, err := StaticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("static app script must be embedded: %v", err)
	}
	for _, want := range []string{"data-password-toggle", "htmx:confirm", "showModal", "aria-pressed", "data-disclosure", "aria-expanded", "data-redis-enabled", "data-redis-auth", "HermesBlueprintMetadata", "data-job-stream-url", "EventSource", "data-copy-log", "navigator.clipboard", "#job-history"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("static app script missing %q", want)
		}
	}
	helper, err := StaticFS.ReadFile("static/blueprint_metadata.js")
	if err != nil {
		t.Fatalf("metadata helper must be embedded: %v", err)
	}
	for _, want := range []string{"applySelectionChange", "syncHint", "syncRedisAuth", "disabled"} {
		if !strings.Contains(string(helper), want) {
			t.Fatalf("metadata helper missing %q", want)
		}
	}
}

func TestLayoutUsesTablerAdminShell(t *testing.T) {
	body := renderPageBody(t, "accounts", map[string]any{
		"PageTitle": "AWS 云账号",
		"ActiveNav": "accounts",
	})

	for _, want := range []struct {
		tag     string
		classes []string
	}{
		{"body", []string{"page"}},
		{"aside", []string{"navbar", "navbar-vertical", "navbar-expand-lg"}},
		{"div", []string{"page-wrapper"}},
		{"div", []string{"page-body"}},
	} {
		requireTagWithClassTokens(t, body, want.tag, want.classes...)
	}

	toggler := requireTagWithClassTokens(t, body, "button", "navbar-toggler")
	if got := htmlAttribute(t, toggler, "data-bs-toggle"); got != "collapse" {
		t.Errorf("mobile navigation toggle data-bs-toggle = %q, want collapse", got)
	}
	controls := htmlAttribute(t, toggler, "aria-controls")
	target := strings.TrimPrefix(htmlAttribute(t, toggler, "data-bs-target"), "#")
	if controls == "" || controls != target {
		t.Errorf("mobile navigation toggle targets %q but aria-controls = %q", target, controls)
	}
	if count := strings.Count(body, `id="`+target+`"`); count != 1 {
		t.Errorf("mobile navigation collapse id %q count = %d, want 1", target, count)
	}
	requireTagWithClassTokensAndAttribute(t, body, "nav", "id", target, "collapse", "navbar-collapse")

	activeLink := requireTagWithClassTokensAndAttribute(t, body, "a", "href", "/accounts", "nav-link")
	requireClassTokens(t, activeLink, "nav-link", "active")
	if got := htmlAttribute(t, activeLink, "aria-current"); got != "page" {
		t.Errorf("active navigation aria-current = %q, want page", got)
	}

	for _, route := range []string{"/accounts", "/projects", "/blueprints", "/environments"} {
		if !strings.Contains(body, `href="`+route+`"`) {
			t.Errorf("authenticated shell missing route %q", route)
		}
	}
	for _, want := range []string{
		`href="/static/app.css"`, `action="/logout"`, `method="post"`, `data-loading-label="退出中…"`,
		`href="#main-content"`, `id="main-content"`, `<dialog id="confirm-dialog"`, `method="dialog"`,
		`aria-labelledby="confirm-title"`, `aria-describedby="confirm-message"`,
		`id="confirm-cancel" type="button"`, `id="confirm-submit" type="button"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("authenticated shell missing behavior hook %q", want)
		}
	}

	scriptPaths := []string{
		"/static/htmx.min.js",
		"/static/blueprint_metadata.js",
		"/static/ui_feedback.js",
		"/static/tabler.min.js",
		"/static/app.js",
	}
	previous := -1
	for _, path := range scriptPaths {
		tag := requireTagWithAttribute(t, body, "script", "src", path)
		if _, ok := htmlAttributeValue(tag, "defer"); !ok {
			t.Errorf("script %q does not load with defer", path)
		}
		position := strings.Index(body, tag)
		if position <= previous {
			t.Errorf("script %q loads out of order", path)
		}
		previous = position
	}
}

func TestLayoutPrimaryLinksBelongToNavigationLandmark(t *testing.T) {
	body := renderPageBody(t, "accounts", map[string]any{"ActiveNav": "accounts"})
	navTag := requireTagWithAttribute(t, body, "nav", "aria-label", "主导航")
	navStart := strings.Index(body, navTag)
	navEnd := strings.Index(body[navStart:], "</nav>")
	if navEnd == -1 {
		t.Fatal("primary navigation landmark has no closing tag")
	}
	navigation := body[navStart : navStart+navEnd]
	for _, route := range []string{"/accounts", "/projects", "/blueprints", "/environments"} {
		if !strings.Contains(navigation, `href="`+route+`"`) {
			t.Errorf("primary navigation landmark does not contain route %q", route)
		}
	}
}

func TestLayoutDocumentsUseCoherentHeadingHierarchy(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body string
	}{
		{"authenticated", renderPageBody(t, "accounts", map[string]any{"ActiveNav": "accounts"})},
		{"login", renderPageBody(t, "login", map[string]any{"HideNav": true})},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			h1Tags := htmlStartTags(fixture.body, "h1")
			if len(h1Tags) != 1 {
				t.Fatalf("document h1 count = %d, want 1", len(h1Tags))
			}
			h1Start := strings.Index(fixture.body, h1Tags[0])
			h1End := strings.Index(fixture.body[h1Start:], "</h1>")
			if h1End == -1 || !strings.Contains(fixture.body[h1Start:h1Start+h1End], "Hermes") {
				t.Fatal("document h1 does not identify Hermes")
			}

			h2Tags := htmlStartTags(fixture.body, "h2")
			if len(h2Tags) == 0 {
				t.Fatal("document has no page h2 below the Hermes h1")
			}
			if h2Start := strings.Index(fixture.body, h2Tags[0]); h2Start <= h1Start {
				t.Error("page h2 must follow the Hermes h1 in document order")
			}
		})
	}
}

func TestLoginUsesTablerAuthShell(t *testing.T) {
	body := renderPageBody(t, "login", map[string]any{
		"PageTitle": "登录",
		"HideNav":   true,
		"Error":     "口令错误",
	})

	requireTagWithClassTokens(t, body, "body", "page", "page-center")
	requireTagWithClassTokens(t, body, "main", "container-tight")
	if cards := tagsWithClassTokens(body, "*", "card"); len(cards) != 1 {
		t.Fatalf("login card count = %d, want exactly 1", len(cards))
	}
	for _, forbidden := range []string{
		`aria-label="主导航"`, `action="/logout"`, `id="confirm-dialog"`,
		"navbar-vertical", "navbar-toggler",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("login shell unexpectedly contains authenticated UI %q", forbidden)
		}
	}
}

func TestTablerIconsRemainAccessible(t *testing.T) {
	authenticated := renderPageBody(t, "accounts", map[string]any{"ActiveNav": "accounts"})
	login := renderPageBody(t, "login", map[string]any{"HideNav": true})
	body := authenticated + login

	icons := tagsWithClassPrefix(body, "i", "ti-")
	if len(icons) < 6 {
		t.Fatalf("rendered Tabler icon count = %d, want at least 6 shell icons", len(icons))
	}
	for _, icon := range icons {
		if got := htmlAttribute(t, icon, "aria-hidden"); got != "true" {
			t.Errorf("decorative Tabler icon must be hidden from assistive technology: %s", icon)
		}
	}

	iconOnlyControls := iconOnlyHTMLControls(authenticated)
	if len(iconOnlyControls) == 0 {
		t.Fatal("authenticated shell contains no icon-only control fixture")
	}
	for _, control := range iconOnlyControls {
		if !hasAccessibleName(control) {
			t.Errorf("icon-only control has no accessible name: %s", control)
		}
	}
}

func TestLoginLayoutHidesAuthenticatedNavigation(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	w := httptest.NewRecorder()
	r.Render(w, "login", map[string]any{
		"PageTitle": "登录",
		"HideNav":   true,
		"Error":     "口令错误",
	})
	body := w.Body.String()
	for _, forbidden := range []string{`aria-label="主导航"`, `action="/logout"`, `id="confirm-dialog"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("login shell unexpectedly contains authenticated UI %q", forbidden)
		}
	}
	if !strings.Contains(body, `<title>登录 · Hermes</title>`) {
		t.Fatal("login layout should render the supplied page title")
	}
	for _, want := range []string{`autocomplete="current-password"`, `role="alert"`, `required`} {
		if !strings.Contains(body, want) {
			t.Fatalf("login form missing %q", want)
		}
	}
}

func TestLayoutProvidesSkipLinkAndDialog(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	w := httptest.NewRecorder()
	r.Render(w, "accounts", map[string]any{
		"PageTitle": "AWS 云账号",
		"ActiveNav": "accounts",
	})
	body := w.Body.String()
	for _, want := range []string{
		`href="#main-content"`, `id="main-content"`, `<dialog id="confirm-dialog"`,
		`aria-labelledby="confirm-title"`, `id="confirm-cancel" type="button"`, `id="confirm-submit" type="button"`,
		`src="/static/app.js"`, `defer`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("layout missing %q", want)
		}
	}
}

func TestRenderPartialEnvStatusUp(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env":       map[string]any{"ID": int64(1), "Status": "up", "Outputs": map[string]any{"public_ips": []any{"1.2.3.4"}}},
		"PublicIPs": "1.2.3.4",
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "销毁") || !strings.Contains(out, "1.2.3.4") {
		t.Fatalf("env_status(up) missing destroy/ip: %s", out)
	}
}

func TestRenderPartialEnvStatusRichOutputs(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env":            map[string]any{"ID": int64(1), "Status": "up"},
		"PublicIPs":      "52.1.2.3",
		"PublicDNS":      "ec2-52-1-2-3.compute.amazonaws.com",
		"VPCID":          "vpc-123",
		"SubnetIDs":      "subnet-1, subnet-2",
		"RDSEndpoint":    "db.example:3306",
		"RDSAddress":     "db.example",
		"RDSPort":        "3306",
		"RDSUsername":    "admin",
		"HasRDSSecret":   true,
		"RedisEndpoint":  "redis.example",
		"RedisReader":    "redis-ro.example",
		"RedisPort":      "6379",
		"HasRedisSecret": true,
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	for _, want := range []string{"EC2", "网络", "数据库", "Redis", "52.1.2.3", "vpc-123", "db.example:3306", "admin", "redis.example", "显示凭据", "/environments/1/rds-credentials", "/environments/1/redis-credentials"} {
		if !strings.Contains(out, want) {
			t.Fatalf("env_status output missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "password") || strings.Contains(out, "密码") {
		t.Fatalf("env_status must not render DB password: %s", out)
	}
}

func TestRenderPartialRDSCredentials(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Username": "admin",
		"Password": "stored-rds-secret",
		"Host":     "db.example",
		"Port":     "3306",
	}
	if err := r.RenderPartial(&b, "rds_credentials", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	for _, want := range []string{"admin", "stored-rds-secret", "db.example", "3306"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rds_credentials missing %q: %s", want, out)
		}
	}
}

func TestRenderPartialRedisCredentials(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Username": "default",
		"Token":    "stored-redis-token",
		"Host":     "redis.example",
		"Port":     "6379",
	}
	if err := r.RenderPartial(&b, "redis_credentials", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	for _, want := range []string{"default", "stored-redis-token", "redis.example", "6379"} {
		if !strings.Contains(out, want) {
			t.Fatalf("redis_credentials missing %q: %s", want, out)
		}
	}
}

func TestRenderPartialEnvStatusDestroyPreviewGate(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env": map[string]any{"ID": int64(1), "Status": "up"},
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "/destroy-preview") || !strings.Contains(out, "预演销毁") {
		t.Fatalf("env_status(up) missing destroy preview action: %s", out)
	}
	if strings.Contains(out, `action="/environments/1/destroy"`) {
		t.Fatalf("env_status(up) must not expose direct destroy: %s", out)
	}

	b.Reset()
	data = map[string]any{
		"Env":         map[string]any{"ID": int64(1), "Status": "destroy_preview_ready"},
		"DestroyPlan": "2 个待删除",
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out = b.String()
	for _, want := range []string{"销毁预演", "2 个待删除", "确认销毁", "保留资源", `action="/environments/1/destroy"`, `action="/environments/1/cancel-destroy"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("env_status(destroy_preview_ready) missing %q: %s", want, out)
		}
	}
}

func TestRenderPartialEnvStatusRefreshActionAndSummary(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	var b bytes.Buffer
	data := map[string]any{
		"Env":         map[string]any{"ID": int64(1), "Status": "up"},
		"RefreshPlan": "0 创建 / 2 更新 / 1 删除 / 4 不变",
	}
	if err := r.RenderPartial(&b, "env_status", data); err != nil {
		t.Fatalf("RenderPartial: %v", err)
	}
	out := b.String()
	for _, want := range []string{"检测漂移", "最近漂移检测", "0 创建 / 2 更新 / 1 删除 / 4 不变", `action="/environments/1/refresh"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("env_status(up) missing refresh item %q: %s", want, out)
		}
	}
}

func TestTablesHaveCaptionsAndScopedHeaders(t *testing.T) {
	headerPattern := regexp.MustCompile(`<th\b[^>]*>`)
	cellPattern := regexp.MustCompile(`<td\b[^>]*>`)
	for _, name := range []string{"accounts.html", "projects.html", "blueprints.html", "environments.html", "_fragments.html"} {
		t.Run(name, func(t *testing.T) {
			source := readTemplateSource(t, name)
			tableCount := strings.Count(source, "<table")
			if tableCount == 0 {
				t.Fatal("test fixture contains no table")
			}
			if got := strings.Count(source, "<caption"); got != tableCount {
				t.Fatalf("captions = %d, want one for each of %d tables", got, tableCount)
			}
			if got := strings.Count(source, "responsive-table"); got == 0 {
				t.Fatal("table is not wired to the responsive row treatment")
			}
			for _, header := range headerPattern.FindAllString(source, -1) {
				if !strings.Contains(header, `scope="col"`) {
					t.Errorf("column header lacks scope: %s", header)
				}
			}
			for _, cell := range cellPattern.FindAllString(source, -1) {
				if !strings.Contains(cell, `data-label=`) && !strings.Contains(cell, `class="empty-row"`) {
					t.Errorf("responsive data cell lacks a narrow-screen label: %s", cell)
				}
			}
		})
	}

	body := renderPageBody(t, "environments", map[string]any{
		"PageTitle": "环境",
		"ActiveNav": "environments",
		"Environments": []map[string]any{{
			"ID": int64(7), "Name": "staging", "PulumiStack": "staging-7",
			"Region": "ap-southeast-1", "Status": "up",
		}},
	})
	for _, want := range []string{
		`<caption class="sr-only">Pulumi 环境列表</caption>`,
		`<th scope="col">Stack</th>`,
		`<td data-label="Stack" class="long-value">staging-7</td>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered environment table missing %q", want)
		}
	}
	runningBadge := requireTagWithClassTokens(t, body, "span", "badge", "status-success")
	if !strings.Contains(body[strings.Index(body, runningBadge):], ">运行中</span>") {
		t.Error("rendered environment table status badge is missing visible text 运行中")
	}

	for _, tc := range []struct {
		page string
		data map[string]any
		want []string
	}{
		{
			page: "accounts",
			data: map[string]any{"Accounts": []map[string]any{{"ID": int64(1), "Name": "ops", "DefaultRegion": "ap-southeast-1", "AWSAccountID": "123456789012", "ARN": "arn:aws:iam::123456789012:user/ops"}}},
			want: []string{`<caption class="sr-only">AWS 云账号列表</caption>`, `<th scope="col">默认区域</th>`, `<td data-label="默认区域" class="long-value">ap-southeast-1</td>`},
		},
		{
			page: "projects",
			data: map[string]any{"Projects": []map[string]any{{"ID": int64(2), "Name": "console", "Description": "operations UI"}}},
			want: []string{`<caption class="sr-only">项目列表</caption>`, `<th scope="col">描述</th>`, `<td data-label="描述" class="long-value">operations UI</td>`},
		},
	} {
		t.Run("rendered "+tc.page, func(t *testing.T) {
			body := renderPageBody(t, tc.page, tc.data)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("rendered %s table missing %q: %s", tc.page, want, body)
				}
			}
		})
	}
}

func TestListPagesUseTablerTables(t *testing.T) {
	type expectedAction struct {
		href  string
		label string
		icon  string
	}
	fixtures := []struct {
		name    string
		body    string
		actions []expectedAction
	}{
		{
			name: "accounts",
			body: renderPageBody(t, "accounts", map[string]any{
				"Accounts": []map[string]any{{
					"ID": int64(1), "Name": "ops", "DefaultRegion": "ap-southeast-1",
					"AWSAccountID": "123456789012", "ARN": "arn:aws:iam::123456789012:user/ops",
				}},
			}),
			actions: []expectedAction{
				{href: "/accounts/new", label: "添加账号", icon: "ti-plus"},
				{href: "/accounts/1/delete", label: "删除", icon: "ti-trash"},
			},
		},
		{
			name: "projects",
			body: renderPageBody(t, "projects", map[string]any{
				"Projects": []map[string]any{{"ID": int64(2), "Name": "console", "Description": "operations UI"}},
			}),
			actions: []expectedAction{
				{href: "/projects/new", label: "新建项目", icon: "ti-plus"},
				{href: "/projects/2/delete", label: "删除", icon: "ti-trash"},
			},
		},
		{
			name: "blueprints",
			body: renderPageBody(t, "blueprints", map[string]any{
				"Blueprints": []map[string]any{{
					"ID": int64(3), "Name": "web", "Params": map[string]any{
						"Region": "ap-southeast-1", "EC2": map[string]any{"InstanceType": "t3.micro", "Count": 1},
					},
				}},
			}),
			actions: []expectedAction{
				{href: "/blueprints/new", label: "新建蓝图", icon: "ti-plus"},
				{href: "/blueprints/3/edit", label: "编辑", icon: "ti-pencil"},
				{href: "/blueprints/3/duplicate", label: "复制", icon: "ti-copy"},
				{href: "/blueprints/3/deploy", label: "部署", icon: "ti-rocket"},
				{href: "/blueprints/3/delete", label: "删除", icon: "ti-trash"},
			},
		},
		{
			name: "environments",
			body: renderPageBody(t, "environments", map[string]any{
				"Environments": []map[string]any{{
					"ID": int64(4), "Name": "staging", "PulumiStack": "staging-4",
					"Region": "ap-southeast-1", "Status": "up",
				}},
			}),
			actions: []expectedAction{{href: "/environments/4", label: "查看详情", icon: "ti-eye"}},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			requireTagWithClassTokens(t, fixture.body, "section", "card")
			requireTagWithClassTokens(t, fixture.body, "div", "table-responsive")
			requireTagWithClassTokens(t, fixture.body, "table", "table", "table-vcenter")
			for _, action := range fixture.actions {
				requireTablerActionLink(t, fixture.body, action.href, action.label, action.icon)
			}
		})
	}
	requireTagWithClassTokens(t, fixtures[2].body, "div", "btn-list")
}

func TestStatusBadgesUseTablerTonesAndText(t *testing.T) {
	for _, tc := range []struct {
		status string
		label  string
		tone   string
	}{
		{status: "pending", label: "待预演", tone: "status-neutral"},
		{status: "previewing", label: "预演中", tone: "status-active"},
		{status: "preview_ready", label: "预演就绪", tone: "status-warning"},
		{status: "up", label: "运行中", tone: "status-success"},
		{status: "failed", label: "失败", tone: "status-danger"},
		{status: "destroyed", label: "已销毁", tone: "status-neutral"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			var body bytes.Buffer
			if err := newTestRenderer(t).RenderPartial(&body, "env_status", map[string]any{
				"Env": map[string]any{"ID": int64(9), "Name": "demo", "Status": tc.status},
			}); err != nil {
				t.Fatalf("RenderPartial env_status: %v", err)
			}
			badge := requireTagWithClassTokens(t, body.String(), "span", "badge", tc.tone)
			badgeStart := strings.Index(body.String(), badge)
			badgeEnd := strings.Index(body.String()[badgeStart:], "</span>")
			if badgeEnd == -1 || !strings.Contains(body.String()[badgeStart:badgeStart+badgeEnd], tc.label) {
				t.Errorf("status %q badge does not include visible label %q: %s", tc.status, tc.label, body.String())
			}
		})
	}
	fragments := readTemplateSource(t, "_fragments.html")
	if !strings.Contains(fragments, `class="badge status-{{.StatusTone}}"`) {
		t.Error("job rows must map the existing semantic tone directly onto the shared badge system")
	}
	if regexp.MustCompile(`class="badge[^"]*(?:\bbg-(?:blue|green|red|yellow|secondary)-lt\b|\btext-(?:blue|green|red|yellow|secondary)\b)[^"]*"`).MatchString(fragments) {
		t.Error("status markup bypasses the AA-safe Hermes semantic badge palette")
	}

	var history bytes.Buffer
	if err := newTestRenderer(t).RenderPartial(&history, "job_history", map[string]any{
		"Env": map[string]any{"ID": int64(7), "Name": "staging"},
		"Jobs": []map[string]any{
			{"ID": int64(41), "ActionLabel": "创建资源", "StatusLabel": "执行中", "StatusTone": "active"},
			{"ID": int64(42), "ActionLabel": "预演创建", "StatusLabel": "成功", "StatusTone": "success"},
		},
	}); err != nil {
		t.Fatalf("RenderPartial job_history: %v", err)
	}
	requireTagWithClassTokens(t, history.String(), "span", "badge", "status-active")
	requireTagWithClassTokens(t, history.String(), "span", "badge", "status-success")
	for _, label := range []string{"执行中", "成功"} {
		if !strings.Contains(history.String(), ">"+label+"</span>") {
			t.Errorf("job status badge is missing visible text %q", label)
		}
	}
}

func TestStatusBadgePaletteMeetsWCAGAA(t *testing.T) {
	css := readStaticSource(t, "app.css")
	for _, tc := range []struct {
		tone       string
		foreground string
		background string
	}{
		{tone: "active", foreground: "console-primary-strong", background: "console-primary-soft"},
		{tone: "success", foreground: "console-success", background: "console-success-soft"},
		{tone: "danger", foreground: "console-danger", background: "console-danger-soft"},
		{tone: "neutral", foreground: "console-muted", background: "console-subtle"},
		{tone: "warning", foreground: "console-warning", background: "console-warning-soft"},
	} {
		rulePattern := regexp.MustCompile(`\.badge\.status-` + regexp.QuoteMeta(tc.tone) + `\{([^}]*)\}`)
		ruleMatch := rulePattern.FindStringSubmatch(css)
		if len(ruleMatch) != 2 {
			t.Fatalf("generated stylesheet is missing the unified badge selector for %s", tc.tone)
		}
		for _, want := range []string{
			`color:var(--color-` + tc.foreground + `)`,
			`background-color:var(--color-` + tc.background + `)`,
		} {
			if !strings.Contains(ruleMatch[1], want) {
				t.Errorf("%s badge rule does not reference %q: %s", tc.tone, want, ruleMatch[0])
			}
		}
		foreground := cssHexToken(t, css, tc.foreground)
		background := cssHexToken(t, css, tc.background)
		if ratio := contrastRatio(foreground, background); ratio < 4.5 {
			t.Errorf("%s badge contrast = %.3f:1, want >= 4.5:1", tc.tone, ratio)
		}
	}
}

func TestFormsHaveExplicitLabelsAndErrorRegions(t *testing.T) {
	controlPattern := regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*>`)
	idPattern := regexp.MustCompile(`\bid="([^"]+)"`)
	errorPattern := regexp.MustCompile(`<(?:p|span)\b[^>]*class="field-error"[^>]*>`)
	for _, name := range []string{"login.html", "account_form.html", "project_form.html", "blueprint_form.html", "blueprint_deploy.html"} {
		t.Run(name, func(t *testing.T) {
			source := readTemplateSource(t, name)
			for _, control := range controlPattern.FindAllString(source, -1) {
				if strings.Contains(control, `type="hidden"`) {
					continue
				}
				match := idPattern.FindStringSubmatch(control)
				if len(match) != 2 {
					t.Errorf("visible form control lacks an id: %s", control)
					continue
				}
				if !strings.Contains(source, `for="`+match[1]+`"`) {
					t.Errorf("control %q lacks an explicit label", match[1])
				}
			}
			for _, fieldError := range errorPattern.FindAllString(source, -1) {
				if !strings.Contains(fieldError, `role="alert"`) {
					t.Errorf("field error is not announced: %s", fieldError)
				}
			}
		})
	}

	blueprintForm := readTemplateSource(t, "blueprint_form.html")
	for _, want := range []string{`aria-expanded="true"`, `data-enhanced-expanded=`, `data-disclosure-fallback`, `aria-controls="network-fields"`, `aria-controls="rds-fields"`, `aria-controls="redis-fields"`} {
		if !strings.Contains(blueprintForm, want) {
			t.Errorf("blueprint disclosure is not valid without JavaScript; missing %q", want)
		}
	}

	body := renderPageBody(t, "blueprint_form", blueprintFormRenderData())
	for _, want := range []string{
		`<label class="form-label" for="blueprint-name">名称 *</label>`,
		`<input class="form-control is-invalid" id="blueprint-name" name="name" value="demo" required aria-invalid="true" aria-describedby="error-name">`,
		`<div class="invalid-feedback" id="error-name" role="alert">名称不能为空</div>`,
		`class="btn disclosure-toggle" hidden aria-expanded="true" data-enhanced-expanded="false" aria-controls="network-fields"`,
		`class="disclosure-label" data-disclosure-fallback>VPC</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered blueprint form missing %q", want)
		}
	}
	if strings.Contains(body, `id="network-fields" hidden`) {
		t.Fatal("server-rendered disclosure is hidden without JavaScript")
	}

	for _, tc := range []struct {
		page string
		data map[string]any
		want []string
	}{
		{
			page: "account_form",
			data: map[string]any{"Error": "请检查标出的字段。", "Name": "ops", "DefaultRegion": "bad", "AccessKeyID": "AKIA", "FieldErrors": map[string]string{"default_region": "区域无效"}},
			want: []string{`id="default-region"`, `class="form-control is-invalid"`, `aria-invalid="true" aria-describedby="default-region-hint default-region-error"`, `class="invalid-feedback" id="default-region-error" role="alert">区域无效`},
		},
		{
			page: "project_form",
			data: map[string]any{"Error": "请检查标出的字段。", "Name": "ops", "Description": "too long", "FieldErrors": map[string]string{"description": "描述过长"}},
			want: []string{`id="project-description"`, `class="form-control is-invalid"`, `aria-invalid="true" aria-describedby="project-description-hint project-description-error"`, `class="invalid-feedback" id="project-description-error" role="alert">描述过长`},
		},
	} {
		t.Run("rendered "+tc.page, func(t *testing.T) {
			body := renderPageBody(t, tc.page, tc.data)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("rendered %s form missing %q: %s", tc.page, want, body)
				}
			}
		})
	}
}

func TestBlueprintEditorUsesTablerSections(t *testing.T) {
	body := renderPageBody(t, "blueprint_form", blueprintFormRenderData())

	t.Run("section hierarchy", func(t *testing.T) {
		page := requireTagWithClassTokens(t, body, "section", "card", "page-panel", "workflow-card", "blueprint-editor-card")
		if got := htmlAttribute(t, page, "aria-labelledby"); got != "blueprint-form-title" {
			t.Errorf("blueprint editor aria-labelledby = %q, want blueprint-form-title", got)
		}
		requireTagWithClassTokens(t, body, "div", "card-header", "workflow-card-header")
		requireTagWithClassTokens(t, body, "div", "card-body")
		requireTagWithClassTokens(t, body, "form", "workflow-form", "blueprint-editor-form")

		cards := tagsWithClassTokens(body, "*", "card")
		if len(cards) != 1 {
			t.Errorf("blueprint editor renders %d card surfaces, want one outer card and no nested cards: %v", len(cards), cards)
		}

		for _, section := range []string{"ownership", "compute", "network", "rds", "redis"} {
			t.Run(section, func(t *testing.T) {
				fieldset := requireTagWithAttribute(t, body, "fieldset", "data-blueprint-section", section)
				requireClassTokens(t, fieldset, "blueprint-section")
			})
		}
		if legends := tagsWithClassTokens(body, "legend", "blueprint-section-title"); len(legends) != 5 {
			t.Errorf("Tabler blueprint section legends = %d, want 5", len(legends))
		}
	})

	t.Run("controls and validation", func(t *testing.T) {
		type controlContract struct {
			tag, id, name, class, labelClass string
		}
		controls := []controlContract{
			{tag: "input", id: "blueprint-name", name: "name", class: "form-control", labelClass: "form-label"},
			{tag: "select", id: "blueprint-project", name: "project_id", class: "form-select", labelClass: "form-label"},
			{tag: "select", id: "blueprint-account", name: "cloud_account_id", class: "form-select", labelClass: "form-label"},
			{tag: "input", id: "region-search", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "instance-type-search", class: "form-control", labelClass: "form-label"},
			{tag: "select", id: "region-select", name: "region", class: "form-select", labelClass: "form-label"},
			{tag: "select", id: "instance-type-select", name: "instance_type", class: "form-select", labelClass: "form-label"},
			{tag: "select", id: "ami-select", name: "ami", class: "form-select", labelClass: "form-label"},
			{tag: "input", id: "instance-count", name: "count", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "root-volume-size", name: "root_volume_gb", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "key-pair-name", name: "key_name", class: "form-control", labelClass: "form-label"},
			{tag: "select", id: "ingress-mode", name: "ingress_mode", class: "form-select", labelClass: "form-label"},
			{tag: "input", id: "ingress-port", name: "ingress_port", class: "form-control", labelClass: "form-label"},
			{tag: "select", id: "ingress-protocol", name: "ingress_protocol", class: "form-select", labelClass: "form-label"},
			{tag: "input", id: "ingress-cidr", name: "ingress_cidr", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "network-enabled", name: "network_enabled", class: "form-check-input", labelClass: "form-check-label"},
			{tag: "input", id: "network-vpc-cidr", name: "network_vpc_cidr", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "network-public-subnets", name: "network_public_subnet_cidrs", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "rds-enabled", name: "rds_enabled", class: "form-check-input", labelClass: "form-check-label"},
			{tag: "input", id: "rds-engine-version", name: "rds_engine_version", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "rds-instance-class", name: "rds_instance_class", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "rds-storage", name: "rds_allocated_storage_gb", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "rds-db-name", name: "rds_db_name", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "rds-username", name: "rds_username", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "redis-enabled", name: "redis_enabled", class: "form-check-input", labelClass: "form-check-label"},
			{tag: "input", id: "redis-auth-enabled", name: "redis_auth_enabled", class: "form-check-input", labelClass: "form-check-label"},
			{tag: "input", id: "redis-engine-version", name: "redis_engine_version", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "redis-node-type", name: "redis_node_type", class: "form-control", labelClass: "form-label"},
			{tag: "input", id: "redis-node-count", name: "redis_node_count", class: "form-control", labelClass: "form-label"},
		}
		for _, contract := range controls {
			control := requireTagWithAttribute(t, body, contract.tag, "id", contract.id)
			requireClassTokens(t, control, contract.class)
			if contract.name != "" {
				if got := htmlAttribute(t, control, "name"); got != contract.name {
					t.Errorf("control %q name = %q, want %q", contract.id, got, contract.name)
				}
			}
			requireTagWithClassTokensAndAttribute(t, body, "label", "for", contract.id, contract.labelClass)
		}
		if checks := tagsWithClassTokens(body, "div", "form-check"); len(checks) != 4 {
			t.Errorf("Tabler form-check wrappers = %d, want 4", len(checks))
		}

		name := requireTagWithAttribute(t, body, "input", "id", "blueprint-name")
		requireClassTokens(t, name, "form-control", "is-invalid")
		if got := htmlAttribute(t, name, "aria-describedby"); got != "error-name" {
			t.Errorf("invalid blueprint name aria-describedby = %q, want error-name", got)
		}
		errorTag := requireTagWithClassTokensAndAttribute(t, body, "div", "id", "error-name", "invalid-feedback")
		if got := htmlAttribute(t, errorTag, "role"); got != "alert" {
			t.Errorf("invalid feedback role = %q, want alert", got)
		}
	})

	t.Run("metadata and hidden selection contracts", func(t *testing.T) {
		for _, hint := range []struct {
			key, name, value string
		}{
			{key: "region", name: "selected_region", value: "ap-southeast-1"},
			{key: "instanceType", name: "selected_instance_type", value: "t3.micro"},
			{key: "ami", name: "selected_ami", value: ""},
		} {
			input := requireTagWithAttribute(t, body, "input", "data-selection-hint", hint.key)
			for attribute, want := range map[string]string{"type": "hidden", "name": hint.name, "value": hint.value} {
				if got := htmlAttribute(t, input, attribute); got != want {
					t.Errorf("selection hint %q %s = %q, want %q", hint.key, attribute, got, want)
				}
			}
		}

		for _, contract := range []struct {
			id, source, get, trigger, target, include string
		}{
			{id: "blueprint-account", source: "account", get: "/blueprints/regions", trigger: "change, load", target: "#region-select", include: "[name='selected_region']"},
			{id: "region-select", source: "region", get: "/blueprints/instance-types", trigger: "change", target: "#instance-type-select", include: "[name='cloud_account_id'],[name='region'],[name='selected_instance_type']"},
			{id: "instance-type-select", source: "instanceType", get: "/blueprints/amis", trigger: "change", target: "#ami-select", include: "[name='cloud_account_id'],[name='region'],[name='instance_type'],[name='selected_ami']"},
		} {
			selectTag := requireTagWithAttribute(t, body, "select", "id", contract.id)
			for attribute, want := range map[string]string{
				"data-metadata-source": contract.source,
				"hx-get":               contract.get,
				"hx-trigger":           contract.trigger,
				"hx-target":            contract.target,
				"hx-swap":              "innerHTML",
				"hx-include":           contract.include,
			} {
				if got := htmlAttribute(t, selectTag, attribute); got != want {
					t.Errorf("metadata selector %q %s = %q, want %q", contract.id, attribute, got, want)
				}
			}
		}

		duplicateData := blueprintFormRenderData()
		duplicateData["Mode"] = "duplicate"
		duplicateData["SourceBlueprintID"] = int64(77)
		duplicate := renderPageBody(t, "blueprint_form", duplicateData)
		for _, want := range []string{
			`<input type="hidden" name="blueprint_mode" value="duplicate">`,
			`<input type="hidden" name="source_blueprint_id" value="77">`,
		} {
			if !strings.Contains(duplicate, want) {
				t.Errorf("duplicate blueprint hidden contract missing %q", want)
			}
		}
	})

	t.Run("progressive disclosures and redis hooks", func(t *testing.T) {
		for _, disclosure := range []struct {
			label, target string
		}{
			{label: "VPC", target: "network-fields"},
			{label: "MySQL", target: "rds-fields"},
			{label: "Redis", target: "redis-fields"},
		} {
			toggle := requireTagWithClassTokensAndAttribute(t, body, "button", "aria-controls", disclosure.target, "btn", "disclosure-toggle")
			for attribute, want := range map[string]string{"type": "button", "aria-expanded": "true", "data-enhanced-expanded": "false"} {
				if got := htmlAttribute(t, toggle, attribute); got != want {
					t.Errorf("%s disclosure %s = %q, want %q", disclosure.label, attribute, got, want)
				}
			}
			if _, ok := htmlAttributeValue(toggle, "hidden"); !ok {
				t.Errorf("%s enhanced disclosure toggle must start hidden", disclosure.label)
			}
			requireTagWithClassTokensAndAttribute(t, body, "span", "data-disclosure-fallback", "", "disclosure-label")
			target := requireTagWithAttribute(t, body, "div", "id", disclosure.target)
			if _, hidden := htmlAttributeValue(target, "hidden"); hidden {
				t.Errorf("%s disclosure target is hidden before JavaScript enhancement", disclosure.label)
			}
		}
		for _, hook := range []struct{ id, attribute string }{
			{id: "redis-enabled", attribute: "data-redis-enabled"},
			{id: "redis-auth-enabled", attribute: "data-redis-auth"},
		} {
			input := requireTagWithAttribute(t, body, "input", "id", hook.id)
			if _, ok := htmlAttributeValue(input, hook.attribute); !ok {
				t.Errorf("Redis control %q lost %s", hook.id, hook.attribute)
			}
		}
	})
}

func TestBlueprintParamErrorsUseAdjacentFeedback(t *testing.T) {
	const message = "参数配置无效"
	tests := []struct {
		field, tag, controlID string
	}{
		{field: "region", tag: "select", controlID: "region-select"},
		{field: "instance_type", tag: "select", controlID: "instance-type-select"},
		{field: "count", tag: "input", controlID: "instance-count"},
		{field: "root_volume_gb", tag: "input", controlID: "root-volume-size"},
		{field: "ingress_port", tag: "input", controlID: "ingress-port"},
		{field: "ingress_protocol", tag: "select", controlID: "ingress-protocol"},
		{field: "ingress_cidr", tag: "input", controlID: "ingress-cidr"},
		{field: "network_vpc_cidr", tag: "input", controlID: "network-vpc-cidr"},
		{field: "network_public_subnet_cidrs", tag: "input", controlID: "network-public-subnets"},
		{field: "rds_engine_version", tag: "input", controlID: "rds-engine-version"},
		{field: "rds_instance_class", tag: "input", controlID: "rds-instance-class"},
		{field: "rds_allocated_storage_gb", tag: "input", controlID: "rds-storage"},
		{field: "rds_db_name", tag: "input", controlID: "rds-db-name"},
		{field: "rds_username", tag: "input", controlID: "rds-username"},
		{field: "redis_engine_version", tag: "input", controlID: "redis-engine-version"},
		{field: "redis_node_type", tag: "input", controlID: "redis-node-type"},
		{field: "redis_node_count", tag: "input", controlID: "redis-node-count"},
	}

	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			data := blueprintFormRenderData()
			data["FieldErrors"] = map[string]string{"params": message}
			data["ParamErrorField"] = tc.field
			body := renderPageBody(t, "blueprint_form", data)
			doc := parseHTMLDocument(t, body)
			errorID := "error-" + tc.field

			control := requireHTMLNodeWithAttribute(t, doc, tc.tag, "id", tc.controlID)
			requireHTMLNodeClassTokens(t, control, "is-invalid")
			if got := htmlNodeAttribute(t, control, "aria-invalid"); got != "true" {
				t.Errorf("%s aria-invalid = %q, want true", tc.controlID, got)
			}
			if got := htmlNodeAttribute(t, control, "aria-describedby"); got != errorID {
				t.Errorf("%s aria-describedby = %q, want %q", tc.controlID, got, errorID)
			}

			feedbackNodes := htmlElementNodes(doc, "div")
			var feedbacks []*xhtml.Node
			for _, node := range feedbackNodes {
				if id, ok := htmlNodeAttributeValue(node, "id"); ok && id == errorID {
					feedbacks = append(feedbacks, node)
				}
			}
			if len(feedbacks) != 1 {
				t.Fatalf("feedback %q count = %d, want 1", errorID, len(feedbacks))
			}
			feedback := feedbacks[0]
			requireHTMLNodeClassTokens(t, feedback, "invalid-feedback")
			if got := htmlNodeAttribute(t, feedback, "role"); got != "alert" {
				t.Errorf("%s role = %q, want alert", errorID, got)
			}
			if got := strings.TrimSpace(htmlNodeText(feedback)); got != message {
				t.Errorf("%s text = %q, want %q", errorID, got, message)
			}
			if feedback.Parent != control.Parent || !htmlNodeHasClassTokens(feedback.Parent, "blueprint-field") {
				t.Errorf("%s feedback and %s control are not in the same blueprint field", errorID, tc.controlID)
			}
			if previousHTMLElementSibling(feedback) != control {
				t.Errorf("%s feedback is not the immediate element sibling after %s", errorID, tc.controlID)
			}

			visibleFeedback := 0
			for _, node := range feedbackNodes {
				if htmlNodeHasClassTokens(node, "invalid-feedback") {
					visibleFeedback++
				}
			}
			if visibleFeedback != 1 {
				t.Errorf("rendered parameter error has %d feedback nodes, want exactly 1", visibleFeedback)
			}
			requireHTMLNodeWithAttribute(t, doc, "a", "href", "#"+errorID)
			requireUniqueHTMLIDs(t, doc)
		})
	}

	t.Run("generic fallback", func(t *testing.T) {
		data := blueprintFormRenderData()
		data["FieldErrors"] = map[string]string{"params": message}
		data["ParamErrorField"] = ""
		doc := parseHTMLDocument(t, renderPageBody(t, "blueprint_form", data))

		fieldset := requireHTMLNodeWithAttribute(t, doc, "fieldset", "id", "field-params")
		if got := htmlNodeAttribute(t, fieldset, "aria-describedby"); got != "error-params" {
			t.Errorf("generic parameter fieldset aria-describedby = %q, want error-params", got)
		}
		feedback := requireHTMLNodeWithAttribute(t, doc, "div", "id", "error-params")
		requireHTMLNodeClassTokens(t, feedback, "invalid-feedback", "d-block")
		requireHTMLNodeWithAttribute(t, doc, "a", "href", "#field-params")
		requireUniqueHTMLIDs(t, doc)
	})
}

func TestEnvironmentStatusRendersCredentialOnlyOutputs(t *testing.T) {
	tests := []struct {
		name, titleID, port, post, target string
		data                              map[string]any
	}{
		{
			name: "RDS port and secret", titleID: "rds-output-title-9", port: "3306",
			post: "/environments/9/rds-credentials", target: "rds-credentials-9",
			data: map[string]any{"RDSPort": "3306", "HasRDSSecret": true},
		},
		{
			name: "Redis port and secret", titleID: "redis-output-title-9", port: "6379",
			post: "/environments/9/redis-credentials", target: "redis-credentials-9",
			data: map[string]any{"RedisPort": "6379", "HasRedisSecret": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{
				"Env": map[string]any{"ID": int64(9), "Name": "staging", "Status": "up"},
			}
			for key, value := range tc.data {
				data[key] = value
			}
			var rendered bytes.Buffer
			if err := newTestRenderer(t).RenderPartial(&rendered, "env_status", data); err != nil {
				t.Fatalf("RenderPartial env_status: %v", err)
			}
			doc := parseHTMLDocument(t, rendered.String())
			section := requireHTMLNodeWithAttribute(t, doc, "section", "aria-labelledby", tc.titleID)
			if !strings.Contains(htmlNodeText(section), tc.port) {
				t.Errorf("%s output does not include port %q", tc.name, tc.port)
			}
			button := requireHTMLNodeWithAttribute(t, section, "button", "hx-post", tc.post)
			if got := htmlNodeAttribute(t, button, "hx-target"); got != "#"+tc.target {
				t.Errorf("%s credential target = %q, want #%s", tc.name, got, tc.target)
			}
			requireHTMLNodeWithAttribute(t, section, "div", "id", tc.target)
		})
	}
}

func TestOperationalPagesUseTablerComponents(t *testing.T) {
	environmentData := map[string]any{
		"PageTitle": "环境详情", "ActiveNav": "environments",
		"Env":              map[string]any{"ID": int64(9), "Name": "staging", "PulumiStack": "staging-stack", "Status": "up"},
		"CurrentJobActive": true, "StatusPolling": true, "HasActiveJobs": true,
		"CurrentJobStreamURL": "/jobs/41/logs/stream",
		"CurrentJob":          map[string]any{"ID": int64(41), "ActionLabel": "创建资源", "StatusLabel": "执行中"},
		"Jobs": []map[string]any{{
			"ID": int64(41), "ActionLabel": "创建资源", "StatusLabel": "执行中", "StatusTone": "active",
			"QueuedAt": "10:00", "StartedAt": "10:01", "FinishedAt": "-", "Duration": "1m", "Summary": "1 个待创建",
		}},
		"PublicIPs": "203.0.113.10", "PublicDNS": "ec2.example.test",
		"VPCID": "vpc-123", "SubnetIDs": "subnet-1, subnet-2",
		"RDSEndpoint": "db.example.test:3306", "RDSAddress": "db.example.test", "RDSPort": "3306", "RDSUsername": "admin", "HasRDSSecret": true,
		"RedisEndpoint": "redis.example.test", "RedisReader": "redis-ro.example.test", "RedisPort": "6379", "HasRedisSecret": true,
		"RefreshPlan": "无漂移",
	}
	environment := renderPageBody(t, "environment_detail", environmentData)

	t.Run("environment status outputs history and live log", func(t *testing.T) {
		page := requireTagWithClassTokens(t, environment, "section", "operational-page", "environment-detail-page")
		if got := htmlAttribute(t, page, "aria-labelledby"); got != "environment-detail-title" {
			t.Errorf("environment page aria-labelledby = %q, want environment-detail-title", got)
		}
		requireTagWithClassTokens(t, environment, "header", "page-header", "operational-page-header")
		requireTagWithClassTokensAndAttribute(t, environment, "h2", "id", "environment-detail-title", "page-title")

		status := requireTagWithAttribute(t, environment, "div", "id", "status")
		requireClassTokens(t, status, "card", "operational-status-card")
		for attribute, want := range map[string]string{
			"hx-get": "/environments/9/status", "hx-trigger": "every 2s", "hx-swap": "outerHTML",
			"role": "region", "aria-label": "环境状态", "aria-live": "polite",
		} {
			if got := htmlAttribute(t, status, attribute); got != want {
				t.Errorf("#status %s = %q, want %q", attribute, got, want)
			}
		}
		requireTagWithClassTokens(t, environment, "div", "card-header", "operational-status-header")
		requireTagWithClassTokens(t, environment, "div", "output-grid")
		if outputs := tagsWithClassTokens(environment, "section", "output-section"); len(outputs) != 4 {
			t.Errorf("operational output sections = %d, want 4", len(outputs))
		}
		if datagrids := tagsWithClassTokens(environment, "dl", "datagrid", "output-datagrid"); len(datagrids) != 4 {
			t.Errorf("Tabler output datagrids = %d, want 4", len(datagrids))
		}

		for _, credential := range []struct {
			kind, post, target string
		}{
			{kind: "rds", post: "/environments/9/rds-credentials", target: "#rds-credentials-9"},
			{kind: "redis", post: "/environments/9/redis-credentials", target: "#redis-credentials-9"},
		} {
			button := requireTagWithAttribute(t, environment, "button", "hx-post", credential.post)
			requireClassTokens(t, button, "btn", "btn-outline-secondary")
			for attribute, want := range map[string]string{"type": "button", "hx-target": credential.target, "hx-swap": "innerHTML", "data-loading-label": "读取中…"} {
				if got := htmlAttribute(t, button, attribute); got != want {
					t.Errorf("%s credential button %s = %q, want %q", credential.kind, attribute, got, want)
				}
			}
			requireTagWithAttribute(t, environment, "div", "id", strings.TrimPrefix(credential.target, "#"))
		}

		liveCard := requireTagWithClassTokens(t, environment, "section", "card", "operational-card", "live-job-card")
		if got := htmlAttribute(t, liveCard, "aria-labelledby"); got != "live-job-title" {
			t.Errorf("live job card aria-labelledby = %q, want live-job-title", got)
		}
		log := requireTagWithAttribute(t, environment, "pre", "id", "live-job-log")
		requireClassTokens(t, log, "log-panel")
		for attribute, want := range map[string]string{
			"tabindex": "0", "data-job-stream-url": "/jobs/41/logs/stream",
			"data-job-status-url": "/environments/9/status", "data-job-history-url": "/environments/9/jobs",
		} {
			if got := htmlAttribute(t, log, attribute); got != want {
				t.Errorf("live log %s = %q, want %q", attribute, got, want)
			}
		}
		environmentDoc := parseHTMLDocument(t, environment)
		liveLog := requireHTMLNodeWithAttribute(t, environmentDoc, "pre", "id", "live-job-log")
		streamStatus := requireHTMLNodeWithAttribute(t, environmentDoc, "p", "data-job-stream-status", "")
		if liveLog.Parent != streamStatus.Parent || !htmlNodeHasClassTokens(liveLog.Parent, "live-job-body") {
			t.Error("live log and stream status must share the live-job-body parent")
		}
		if got := htmlNodeAttribute(t, streamStatus, "role"); got != "status" {
			t.Errorf("stream status role = %q, want status", got)
		}
		if got := htmlNodeAttribute(t, streamStatus, "aria-live"); got != "polite" {
			t.Errorf("stream status aria-live = %q, want polite", got)
		}

		history := requireTagWithAttribute(t, environment, "div", "id", "job-history")
		requireClassTokens(t, history, "card", "job-history-card")
		for attribute, want := range map[string]string{"hx-get": "/environments/9/jobs", "hx-trigger": "every 2s", "hx-swap": "outerHTML"} {
			if got := htmlAttribute(t, history, attribute); got != want {
				t.Errorf("#job-history %s = %q, want %q", attribute, got, want)
			}
		}
		requireTagWithClassTokens(t, environment, "table", "table", "table-vcenter", "card-table", "job-history-table")
		detail := requireTagWithAttribute(t, environment, "a", "id", "job-detail-41")
		requireClassTokens(t, detail, "btn", "btn-outline-primary")
		if got := htmlAttribute(t, detail, "href"); got != "/jobs/41" {
			t.Errorf("#job-detail-41 href = %q, want /jobs/41", got)
		}
	})

	t.Run("job diagnostics failure copy and bounded log", func(t *testing.T) {
		job := renderPageBody(t, "job_detail", map[string]any{
			"PageTitle": "Job 详情", "ActiveNav": "environments",
			"Environment": map[string]any{"ID": int64(9), "Name": "staging"},
			"Job": map[string]any{
				"ID": int64(41), "Action": "up", "ActionLabel": "创建资源", "Status": "failed", "StatusLabel": "失败", "StatusTone": "danger",
				"QueuedAt": "10:00", "StartedAt": "10:01", "FinishedAt": "10:02", "Duration": "1m", "Summary": "创建失败", "Error": "权限不足", "Active": false,
			},
			"Logs": "preview\nfailed",
		})
		page := requireTagWithClassTokens(t, job, "section", "operational-page", "job-detail-page")
		if got := htmlAttribute(t, page, "aria-labelledby"); got != "job-detail-title" {
			t.Errorf("job page aria-labelledby = %q, want job-detail-title", got)
		}
		requireTagWithClassTokens(t, job, "header", "page-header", "operational-page-header")
		requireTagWithClassTokensAndAttribute(t, job, "h2", "id", "job-detail-title", "page-title")
		requireTagWithClassTokens(t, job, "section", "card", "operational-card", "job-diagnostics-card")
		requireTagWithClassTokens(t, job, "dl", "datagrid", "description-grid", "diagnostic-grid")
		failure := requireTagWithClassTokens(t, job, "section", "alert", "alert-danger", "job-failure")
		if got := htmlAttribute(t, failure, "role"); got != "alert" {
			t.Errorf("job failure role = %q, want alert", got)
		}
		requireTagWithClassTokens(t, job, "section", "card", "operational-card", "job-log-card")

		jobDoc := parseHTMLDocument(t, job)
		copyButton := requireHTMLNodeWithAttribute(t, jobDoc, "button", "data-copy-log", "")
		requireHTMLNodeClassTokens(t, copyButton, "btn", "btn-outline-secondary")
		for attribute, want := range map[string]string{
			"type": "button", "data-copy-target": "job-log", "data-loading-label": "复制中…", "aria-controls": "job-log",
		} {
			if got := htmlNodeAttribute(t, copyButton, attribute); got != want {
				t.Errorf("copy button %s = %q, want %q", attribute, got, want)
			}
		}
		if _, ok := htmlNodeAttributeValue(copyButton, "hidden"); !ok {
			t.Error("copy button must remain hidden until JavaScript enhancement")
		}
		copyStatus := requireHTMLNodeWithAttribute(t, jobDoc, "span", "data-copy-status", "")
		if copyButton.Parent != copyStatus.Parent || !htmlNodeHasClassTokens(copyButton.Parent, "copy-log-actions") {
			t.Error("copy button and copy status must share the copy-log-actions parent")
		}
		if got := htmlNodeAttribute(t, copyStatus, "role"); got != "status" {
			t.Errorf("copy status role = %q, want status", got)
		}
		if got := htmlNodeAttribute(t, copyStatus, "aria-live"); got != "polite" {
			t.Errorf("copy status aria-live = %q, want polite", got)
		}
		jobLog := requireTagWithAttribute(t, job, "pre", "id", "job-log")
		requireClassTokens(t, jobLog, "log-panel", "log-panel-full")
		if got := htmlAttribute(t, jobLog, "tabindex"); got != "0" {
			t.Errorf("job log tabindex = %q, want 0", got)
		}
	})

	t.Run("destructive and credential fragments", func(t *testing.T) {
		var destroy bytes.Buffer
		if err := newTestRenderer(t).RenderPartial(&destroy, "env_status", map[string]any{
			"Env":         map[string]any{"ID": int64(9), "Name": "staging", "Status": "destroy_preview_ready"},
			"DestroyPlan": "2 个待删除",
		}); err != nil {
			t.Fatalf("RenderPartial destroy status: %v", err)
		}
		form := requireTagWithAttribute(t, destroy.String(), "form", "action", "/environments/9/destroy")
		if got := htmlAttribute(t, form, "data-confirm"); got != "确认销毁环境“staging”及其 AWS 资源？" {
			t.Errorf("destroy confirmation = %q", got)
		}
		destroyButton := requireTagWithClassTokensAndAttribute(t, destroy.String(), "button", "data-loading-label", "销毁中…", "btn", "btn-danger")
		if got := htmlAttribute(t, destroyButton, "type"); got != "submit" {
			t.Errorf("destroy button type = %q, want submit", got)
		}

		for _, credential := range []struct {
			partial string
			data    map[string]any
		}{
			{partial: "rds_credentials", data: map[string]any{"Host": "db.example.test", "Port": "3306", "Username": "admin", "Password": "secret"}},
			{partial: "redis_credentials", data: map[string]any{"Host": "redis.example.test", "Port": "6379", "Username": "default", "Token": "secret"}},
		} {
			var rendered bytes.Buffer
			if err := newTestRenderer(t).RenderPartial(&rendered, credential.partial, credential.data); err != nil {
				t.Fatalf("RenderPartial %s: %v", credential.partial, err)
			}
			alert := requireTagWithClassTokens(t, rendered.String(), "div", "alert", "alert-success", "credential-alert")
			if got := htmlAttribute(t, alert, "role"); got != "status" {
				t.Errorf("%s role = %q, want status", credential.partial, got)
			}
			requireTagWithClassTokens(t, rendered.String(), "dl", "datagrid", "credential-datagrid")
		}
	})
}

func TestFormsUseTablerValidation(t *testing.T) {
	blueprint := map[string]any{
		"ID":   int64(31),
		"Name": "payments",
		"Params": map[string]any{
			"Region": "ap-southeast-1",
			"EC2":    map[string]any{"InstanceType": "t3.micro", "Count": 2},
		},
	}
	tests := []struct {
		name        string
		page        string
		invalidData map[string]any
		validData   map[string]any
		controlID   string
		hintID      string
		errorID     string
		describedBy string
		errorText   string
	}{
		{
			name: "account form",
			page: "account_form",
			invalidData: map[string]any{
				"Error": "请检查标出的字段。", "Name": "ops", "DefaultRegion": "bad", "AccessKeyID": "AKIA",
				"FieldErrors": map[string]string{"default_region": "区域无效"},
			},
			validData: map[string]any{
				"Name": "ops", "DefaultRegion": "ap-southeast-1", "AccessKeyID": "AKIA",
				"FieldErrors": map[string]string{},
			},
			controlID: "default-region", hintID: "default-region-hint", errorID: "default-region-error",
			describedBy: "default-region-hint default-region-error", errorText: "区域无效",
		},
		{
			name: "project form",
			page: "project_form",
			invalidData: map[string]any{
				"Error": "请检查标出的字段。", "Name": "ops", "Description": "too long",
				"FieldErrors": map[string]string{"description": "描述过长"},
			},
			validData: map[string]any{
				"Name": "ops", "Description": "operations", "FieldErrors": map[string]string{},
			},
			controlID: "project-description", hintID: "project-description-hint", errorID: "project-description-error",
			describedBy: "project-description-hint project-description-error", errorText: "描述过长",
		},
		{
			name: "blueprint deploy form",
			page: "blueprint_deploy",
			invalidData: map[string]any{
				"Blueprint": blueprint, "EnvironmentName": "", "EnvironmentNameError": "请输入环境名。",
			},
			validData: map[string]any{
				"Blueprint": blueprint, "EnvironmentName": "staging", "EnvironmentNameError": "",
			},
			controlID: "environment-name", hintID: "env-name-hint", errorID: "env-name-error",
			describedBy: "env-name-hint env-name-error", errorText: "请输入环境名。",
		},
	}

	controlPattern := regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*>`)
	idPattern := regexp.MustCompile(`\bid="([^"]+)"`)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invalid := renderPageBody(t, tc.page, tc.invalidData)
			for _, control := range controlPattern.FindAllString(invalid, -1) {
				if strings.Contains(control, `type="hidden"`) {
					continue
				}
				wantClass := "form-control"
				if strings.HasPrefix(control, "<select") {
					wantClass = "form-select"
				}
				if !strings.Contains(control, `class="`+wantClass) {
					t.Errorf("visible control does not use Tabler %s: %s", wantClass, control)
				}
				id := idPattern.FindStringSubmatch(control)
				if len(id) != 2 || !strings.Contains(invalid, `class="form-label" for="`+id[1]+`"`) {
					t.Errorf("visible control lacks an explicit Tabler label: %s", control)
				}
			}

			invalidControl := regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*\bid="` + regexp.QuoteMeta(tc.controlID) + `"[^>]*>`).FindString(invalid)
			for _, want := range []string{`class="form-control is-invalid"`, `aria-invalid="true"`, `aria-describedby="` + tc.describedBy + `"`} {
				if !strings.Contains(invalidControl, want) {
					t.Errorf("invalid control %q missing %q: %s", tc.controlID, want, invalidControl)
				}
			}
			for _, want := range []string{
				`class="form-hint" id="` + tc.hintID + `"`,
				`class="invalid-feedback" id="` + tc.errorID + `" role="alert">` + tc.errorText,
			} {
				if !strings.Contains(invalid, want) {
					t.Errorf("rendered invalid form missing %q", want)
				}
			}

			valid := renderPageBody(t, tc.page, tc.validData)
			validControl := regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*\bid="` + regexp.QuoteMeta(tc.controlID) + `"[^>]*>`).FindString(valid)
			if !strings.Contains(validControl, `class="form-control"`) {
				t.Errorf("valid control %q does not use the base Tabler class: %s", tc.controlID, validControl)
			}
			for _, unwanted := range []string{`is-invalid`, `aria-invalid="true"`, `id="` + tc.errorID + `"`} {
				if strings.Contains(validControl, unwanted) || (strings.HasPrefix(unwanted, `id=`) && strings.Contains(valid, unwanted)) {
					t.Errorf("valid form retained invalid-state token %q: %s", unwanted, valid)
				}
			}
		})
	}

	accountSource := readTemplateSource(t, "account_form.html")
	toggle := `class="btn btn-outline-secondary password-toggle" type="button" data-password-toggle aria-controls="secret-access-key" aria-pressed="false" hidden>显示</button>`
	if !strings.Contains(accountSource, toggle) {
		t.Error("password toggle must start hidden and retain text-only content for progressive enhancement")
	}
}

func TestDestructivePagesUseTablerDangerZones(t *testing.T) {
	tests := []struct {
		name        string
		page        string
		data        map[string]any
		action      string
		cancel      string
		targetLabel string
		targetValue string
	}{
		{
			name: "account", page: "account_delete", action: "/accounts/21/delete", cancel: "/accounts",
			targetLabel: "待删除账号摘要", targetValue: "prod-main",
			data: map[string]any{"Account": map[string]any{"ID": int64(21), "Name": "prod-main", "DefaultRegion": "ap-southeast-1", "AWSAccountID": "123456789012"}},
		},
		{
			name: "project", page: "project_delete", action: "/projects/22/delete", cancel: "/projects",
			targetLabel: "待删除项目摘要", targetValue: "console",
			data: map[string]any{"Project": map[string]any{"ID": int64(22), "Name": "console", "Description": "operations"}},
		},
		{
			name: "blueprint", page: "blueprint_delete", action: "/blueprints/23/delete", cancel: "/blueprints",
			targetLabel: "待删除蓝图摘要", targetValue: "baseline",
			data: map[string]any{"Blueprint": map[string]any{
				"ID": int64(23), "Name": "baseline",
				"Params": map[string]any{"Region": "ap-southeast-1", "EC2": map[string]any{"InstanceType": "t3.micro", "Count": 1}},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := renderPageBody(t, tc.page, tc.data)
			for _, want := range []string{
				`class="card page-panel workflow-card danger-zone"`,
				`class="alert alert-danger danger-alert" role="alert"`,
				`class="datagrid description-grid danger-target-summary" aria-label="` + tc.targetLabel + `"`,
				`class="datagrid-content long-value">` + tc.targetValue,
				`method="post" action="` + tc.action + `"`,
				`class="btn btn-danger" type="submit" data-loading-label="删除中…"`,
				`class="btn btn-outline-secondary" href="` + tc.cancel + `">取消</a>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("rendered danger zone missing %q: %s", want, body)
				}
			}
		})
	}
}

func TestBlueprintDetailUsesTablerSummary(t *testing.T) {
	name := strings.Repeat("B", 128)
	region := strings.Repeat("region", 16)
	instanceType := strings.Repeat("instance", 16)
	blueprint := map[string]any{
		"ID":   int64(41),
		"Name": name,
		"Params": map[string]any{
			"Region": region,
			"EC2": map[string]any{
				"InstanceType": instanceType, "Count": 3, "RootVolumeGB": 64,
			},
			"Network": map[string]any{"Enabled": true},
			"RDS":     map[string]any{"Enabled": false},
			"Redis":   map[string]any{"Enabled": true},
		},
	}

	detail := renderPageBody(t, "blueprint_detail", map[string]any{"Blueprint": blueprint})
	for _, want := range []string{
		`class="card page-panel workflow-card blueprint-detail-card" aria-labelledby="blueprint-detail-title"`,
		`class="card-title page-title long-value" id="blueprint-detail-title">` + name,
		`class="datagrid description-grid blueprint-summary" aria-label="蓝图资源摘要"`,
		`class="datagrid-content long-value">` + region,
		`class="datagrid-content long-value">` + instanceType + ` × 3`,
		`class="btn btn-outline-secondary" href="/blueprints/41/edit"`,
		`class="btn btn-outline-secondary" href="/blueprints/41/duplicate"`,
		`class="btn btn-primary" href="/blueprints/41/deploy"`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("rendered blueprint detail missing %q: %s", want, detail)
		}
	}

	deploy := renderPageBody(t, "blueprint_deploy", map[string]any{
		"Blueprint": blueprint, "EnvironmentName": "staging", "EnvironmentNameError": "",
	})
	for _, want := range []string{
		`class="card page-panel workflow-card blueprint-deploy-card" aria-labelledby="blueprint-deploy-title"`,
		`class="card-title page-title long-value" id="blueprint-deploy-title">部署 ` + name,
		`class="datagrid description-grid blueprint-deploy-summary" aria-label="部署资源摘要"`,
		`class="datagrid-content long-value">` + region,
		`class="datagrid-content long-value">` + instanceType + ` × 3`,
		`method="post" action="/blueprints/41/deploy"`,
		`class="btn btn-primary" type="submit" data-loading-label="启动中…"`,
		`class="btn btn-outline-secondary" href="/blueprints/41">取消</a>`,
	} {
		if !strings.Contains(deploy, want) {
			t.Errorf("rendered blueprint deploy page missing %q: %s", want, deploy)
		}
	}
}

func TestStatusBadgesIncludeText(t *testing.T) {
	fragments := readTemplateSource(t, "_fragments.html")
	for _, want := range []string{
		`define "environment_status_badge"`,
		`>待预演</span>`,
		`>预演中</span>`,
		`>运行中</span>`,
		`>失败</span>`,
		`>已销毁</span>`,
	} {
		if !strings.Contains(fragments, want) {
			t.Errorf("status badge lacks semantic text; missing %q", want)
		}
	}

	environments := readTemplateSource(t, "environments.html")
	if !strings.Contains(environments, `template "environment_status_badge" .Status`) {
		t.Fatal("environment list does not render the shared text status badge")
	}
	if strings.Contains(environments, `class="status-value"`) {
		t.Fatal("environment list still renders an unclassified raw status value")
	}

	for _, tc := range []struct {
		status string
		label  string
		tone   string
	}{
		{"pending", "待预演", "status-neutral"},
		{"previewing", "预演中", "status-active"},
		{"up", "运行中", "status-success"},
		{"failed", "失败", "status-danger"},
		{"destroyed", "已销毁", "status-neutral"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			var body bytes.Buffer
			r := newTestRenderer(t)
			if err := r.RenderPartial(&body, "env_status", map[string]any{
				"Env": map[string]any{"ID": int64(9), "Name": "demo", "Status": tc.status},
			}); err != nil {
				t.Fatalf("RenderPartial env_status: %v", err)
			}
			output := body.String()
			badge := requireTagWithClassTokens(t, output, "span", "badge", tc.tone)
			badgeStart := strings.Index(output, badge)
			badgeEnd := strings.Index(output[badgeStart:], "</span>")
			if badgeEnd == -1 || !strings.Contains(output[badgeStart:badgeStart+badgeEnd], tc.label) {
				t.Errorf("rendered status %q missing labelled badge %q: %s", tc.status, tc.label, output)
			}
		})
	}
}

func TestRenderPartialJobHistoryUsesStableUniqueDetailLinkIDs(t *testing.T) {
	r := newTestRenderer(t)
	render := func(t *testing.T, jobs []map[string]any) string {
		t.Helper()
		var body bytes.Buffer
		if err := r.RenderPartial(&body, "job_history", map[string]any{
			"Env":           map[string]any{"ID": int64(7), "Name": "staging"},
			"HasActiveJobs": true,
			"Jobs":          jobs,
		}); err != nil {
			t.Fatalf("RenderPartial job_history: %v", err)
		}
		return body.String()
	}

	before := render(t, []map[string]any{
		{"ID": int64(41), "ActionLabel": "创建资源", "StatusLabel": "执行中", "StatusTone": "active"},
		{"ID": int64(42), "ActionLabel": "预演创建", "StatusLabel": "成功", "StatusTone": "success"},
	})
	after := render(t, []map[string]any{
		{"ID": int64(41), "ActionLabel": "创建资源", "StatusLabel": "成功", "StatusTone": "success"},
		{"ID": int64(42), "ActionLabel": "预演创建", "StatusLabel": "成功", "StatusTone": "success"},
	})

	detailLinkPattern := regexp.MustCompile(`<a\b[^>]*href="/jobs/[0-9]+"[^>]*>`)
	for renderName, output := range map[string]string{"before poll": before, "after poll": after} {
		t.Run(renderName, func(t *testing.T) {
			links := detailLinkPattern.FindAllString(output, -1)
			if len(links) != 2 {
				t.Fatalf("rendered detail links = %d, want 2: %s", len(links), output)
			}
			for _, jobID := range []int64{41, 42} {
				stableID := `id="job-detail-` + strconv.FormatInt(jobID, 10) + `"`
				if count := strings.Count(output, stableID); count != 1 {
					t.Errorf("stable detail-link id %q count = %d, want 1: %s", stableID, count, output)
				}
				var matchingLink string
				for _, link := range links {
					if strings.Contains(link, `href="/jobs/`+strconv.FormatInt(jobID, 10)+`"`) {
						matchingLink = link
						break
					}
				}
				if !strings.Contains(matchingLink, stableID) {
					t.Errorf("Job %d detail link lacks its stable id: %s", jobID, matchingLink)
				}
				if strings.Contains(matchingLink, "hx-preserve") {
					t.Errorf("Job %d detail link relies on the incompatible hx-preserve fallback: %s", jobID, matchingLink)
				}
			}
		})
	}
}

func TestDestructiveActionsUseSharedConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"_account_rows.html", []string{`href="/accounts/{{.ID}}/delete"`, `hx-delete="/accounts/`, `hx-confirm="删除账号`}},
		{"_fragments.html", []string{`href="/projects/{{.ID}}/delete"`, `hx-delete="/projects/`, `hx-confirm="删除项目`, `href="/blueprints/{{.ID}}/delete"`, `hx-confirm="删除蓝图`, `action="/environments/{{.Env.ID}}/destroy"`, `data-confirm="确认销毁环境`}},
		{"account_delete.html", []string{`role="alert"`, `action="/accounts/{{.Account.ID}}/delete"`, `type="submit"`}},
		{"project_delete.html", []string{`role="alert"`, `action="/projects/{{.Project.ID}}/delete"`, `type="submit"`}},
		{"blueprint_delete.html", []string{`role="alert"`, `action="/blueprints/{{.Blueprint.ID}}/delete"`, `type="submit"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := readTemplateSource(t, tc.name)
			for _, want := range tc.want {
				if !strings.Contains(source, want) {
					t.Errorf("destructive action bypasses the shared confirmation or no-JS path; missing %q", want)
				}
			}
		})
	}

	js := readStaticSource(t, "app.js")
	for _, want := range []string{`htmx:confirm`, `form.dataset.confirm`, `showModal`, `addEventListener("cancel"`} {
		if !strings.Contains(js, want) {
			t.Errorf("shared confirmation script missing %q", want)
		}
	}

	var body bytes.Buffer
	if err := newTestRenderer(t).RenderPartial(&body, "env_status", map[string]any{
		"Env":         map[string]any{"ID": int64(12), "Name": "staging", "Status": "destroy_preview_ready"},
		"DestroyPlan": "2 个待删除",
	}); err != nil {
		t.Fatalf("RenderPartial destroy confirmation: %v", err)
	}
	for _, want := range []string{`action="/environments/12/destroy"`, `data-confirm="确认销毁环境“staging”及其 AWS 资源？"`, `确认销毁`} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("rendered destructive action missing %q: %s", want, body.String())
		}
	}

	for _, tc := range []struct {
		partial string
		data    any
		want    []string
	}{
		{partial: "rows", data: []map[string]any{{"ID": int64(21), "Name": "prod", "DefaultRegion": "ap-southeast-1"}}, want: []string{`href="/accounts/21/delete"`, `hx-delete="/accounts/21"`, `hx-confirm="删除账号“prod”？"`}},
		{partial: "project_rows", data: []map[string]any{{"ID": int64(22), "Name": "console", "Description": "ops"}}, want: []string{`href="/projects/22/delete"`, `hx-delete="/projects/22"`, `hx-confirm="删除项目“console”？"`}},
	} {
		var rendered bytes.Buffer
		if err := newTestRenderer(t).RenderPartial(&rendered, tc.partial, tc.data); err != nil {
			t.Fatalf("RenderPartial %s: %v", tc.partial, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(rendered.String(), want) {
				t.Errorf("rendered %s delete action missing %q: %s", tc.partial, want, rendered.String())
			}
		}
	}

	for _, tc := range []struct {
		page string
		data map[string]any
		want string
	}{
		{page: "account_delete", data: map[string]any{"Account": map[string]any{"ID": int64(21), "Name": "prod"}}, want: `action="/accounts/21/delete"`},
		{page: "project_delete", data: map[string]any{"Project": map[string]any{"ID": int64(22), "Name": "console"}}, want: `action="/projects/22/delete"`},
	} {
		if rendered := renderPageBody(t, tc.page, tc.data); !strings.Contains(rendered, tc.want) {
			t.Errorf("rendered %s confirmation missing %q: %s", tc.page, tc.want, rendered)
		}
	}
}

func TestRenderedNativeSubmitButtonsReserveBusyIndicatorSpace(t *testing.T) {
	accounts := renderPageBody(t, "accounts", map[string]any{
		"PageTitle": "AWS 云账号",
		"ActiveNav": "accounts",
	})

	var environmentStatus bytes.Buffer
	if err := newTestRenderer(t).RenderPartial(&environmentStatus, "env_status", map[string]any{
		"Env":         map[string]any{"ID": int64(12), "Name": "staging", "Status": "destroy_preview_ready"},
		"DestroyPlan": "2 个待删除",
	}); err != nil {
		t.Fatalf("RenderPartial destroy confirmation: %v", err)
	}

	postFormPattern := regexp.MustCompile(`(?s)<form\b[^>]*method="post"[^>]*>.*?</form>`)
	buttonPattern := regexp.MustCompile(`<button\b[^>]*>`)
	for name, output := range map[string]string{
		"authenticated layout": accounts,
		"environment actions":  environmentStatus.String(),
	} {
		t.Run(name, func(t *testing.T) {
			forms := postFormPattern.FindAllString(output, -1)
			if len(forms) == 0 {
				t.Fatal("rendered fixture contains no native POST form")
			}
			for _, form := range forms {
				for _, button := range buttonPattern.FindAllString(form, -1) {
					if strings.Contains(button, `type="button"`) {
						continue
					}
					if !strings.Contains(button, `data-loading-label=`) {
						t.Errorf("native submit button adds busy geometry too late: %s", button)
					}
				}
			}
		})
	}
}

func TestLongIdentifiersUseWrappingUtilities(t *testing.T) {
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"_account_rows.html", []string{`data-label="别名" class="long-value"`, `data-label="默认区域" class="long-value"`, `data-label="Account ID" class="long-value"`, `data-label="ARN" class="long-value"`}},
		{"_fragments.html", []string{`data-label="名称" class="long-value"`, `class="job-summary long-value"`, `class="job-error long-value"`}},
		{"layout.html", []string{`id="confirm-message" class="long-value"`}},
		{"environments.html", []string{`data-label="名称" class="long-value"`, `data-label="Stack" class="long-value"`}},
		{"environment_detail.html", []string{`class="stack-label long-value"`}},
		{"job_detail.html", []string{`class="raw-value long-value"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := readTemplateSource(t, tc.name)
			for _, want := range tc.want {
				if !strings.Contains(source, want) {
					t.Errorf("long identifier lacks safe wrapping; missing %q", want)
				}
			}
		})
	}

	stack := "staging-" + strings.Repeat("very-long-identifier-", 8)
	body := renderPageBody(t, "environments", map[string]any{
		"PageTitle": "环境",
		"ActiveNav": "environments",
		"Environments": []map[string]any{{
			"ID": int64(3), "Name": strings.Repeat("environment", 8), "PulumiStack": stack,
			"Region": "ap-southeast-1", "Status": "up",
		}},
	})
	if !strings.Contains(body, `data-label="Stack" class="long-value">`+stack+`</td>`) {
		t.Fatal("rendered long stack identifier is not wrapped")
	}

	longAccount := strings.Repeat("account-without-breaks", 10)
	longRegion := strings.Repeat("region", 20)
	var accountRows bytes.Buffer
	if err := newTestRenderer(t).RenderPartial(&accountRows, "rows", []map[string]any{{"ID": int64(4), "Name": longAccount, "DefaultRegion": longRegion}}); err != nil {
		t.Fatalf("RenderPartial account rows: %v", err)
	}
	for _, want := range []string{`data-label="别名" class="long-value">` + longAccount, `data-label="默认区域" class="long-value">` + longRegion} {
		if !strings.Contains(accountRows.String(), want) {
			t.Errorf("rendered account row lacks wrapping hook %q: %s", want, accountRows.String())
		}
	}

	longProject := strings.Repeat("project-without-breaks", 10)
	var projectRows bytes.Buffer
	if err := newTestRenderer(t).RenderPartial(&projectRows, "project_rows", []map[string]any{{"ID": int64(5), "Name": longProject, "Description": longProject}}); err != nil {
		t.Fatalf("RenderPartial project rows: %v", err)
	}
	if !strings.Contains(projectRows.String(), `data-label="名称" class="long-value">`+longProject) {
		t.Errorf("rendered project row lacks wrapping hook: %s", projectRows.String())
	}

	layout := renderPageBody(t, "accounts", map[string]any{})
	if !strings.Contains(layout, `id="confirm-message" class="long-value"`) {
		t.Fatal("rendered shared confirmation message lacks safe wrapping")
	}
}

func TestBlueprintDeleteWrapsMaximumLengthName(t *testing.T) {
	name := strings.Repeat("A", 128)
	body := renderPageBody(t, "blueprint_delete", map[string]any{
		"PageTitle": "删除蓝图",
		"ActiveNav": "blueprints",
		"Blueprint": map[string]any{
			"ID":   int64(13),
			"Name": name,
			"Params": map[string]any{
				"Region": "ap-southeast-1",
				"EC2":    map[string]any{"InstanceType": "t3.micro", "Count": 1},
			},
		},
	})

	for _, want := range []string{
		`<p class="long-value">即将永久删除蓝图“` + name + `”。已有环境引用时，Hermes 会拒绝删除。</p>`,
		`<dd class="datagrid-content long-value">` + name + `</dd>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered delete confirmation lacks safe wrapping hook %q: %s", want, body)
		}
	}
}

func TestGeneratedAssetsIncludeResponsiveAndReducedMotionRules(t *testing.T) {
	css := readStaticSource(t, "app.css")
	for _, want := range []string{
		`@media (max-width:39.999rem)`,
		`content:attr(data-label)`,
		`@media (prefers-reduced-motion:reduce)`,
		`touch-action:manipulation`,
		`[aria-busy=true]`,
		`min-height:44px`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("generated stylesheet missing %q", want)
		}
	}
	strongBorder := cssHexToken(t, css, "console-border-strong")
	for _, adjacent := range []string{"console-surface", "console-canvas", "console-subtle"} {
		background := cssHexToken(t, css, adjacent)
		if ratio := contrastRatio(strongBorder, background); ratio < 3 {
			t.Errorf("strong border contrast against %s = %.3f:1, want >= 3:1", adjacent, ratio)
		}
	}
	for name, pattern := range map[string]*regexp.Regexp{
		"skip link":                   regexp.MustCompile(`\.skip-link\{[^}]*min-height:44px`),
		"empty-state links":           regexp.MustCompile(`\.empty-row a[^}]*\{[^}]*min-height:44px`),
		"focus-visible indicator":     regexp.MustCompile(`:focus-visible\{[^}]*outline:3px solid var\(--color-console-primary\)`),
		"16px form controls":          regexp.MustCompile(`input:not\(\[type=checkbox\]\),select,textarea\{[^}]*font-size:var\(--text-base\)`),
		"disclosure minimum height":   regexp.MustCompile(`\.disclosure-toggle\{[^}]*min-height:44px`),
		"disclosure minimum width":    regexp.MustCompile(`\.disclosure-toggle\{[^}]*min-width:44px`),
		"stable busy indicator":       regexp.MustCompile(`button\[data-loading-label\]:after[^}]*\{[^}]*width:1rem`),
		"visible metadata busy state": regexp.MustCompile(`\[data-metadata-source\]\[aria-busy=true\]\{[^}]*background-color:var\(--color-console-primary-soft\)`),
		"fixed live log viewport":     regexp.MustCompile(`\.log-panel\{[^}]*height:clamp\(12rem,50vh,26\.25rem\)`),
		"fixed detail log viewport":   regexp.MustCompile(`\.log-panel-full\{[^}]*height:min\(70vh,40rem\)`),
	} {
		if !pattern.MatchString(css) {
			t.Errorf("%s does not preserve a 44px interaction target in generated CSS", name)
		}
	}
	if regexp.MustCompile(`@media \(min-width:40rem\)\{\.field>input:not\(\[type=checkbox\]\)[^}]*font-size:var\(--text-sm\)`).MatchString(css) {
		t.Error("form controls regress below 16px at the 640px breakpoint")
	}
	if regexp.MustCompile(`\.field>input:not\(\[type=checkbox\]\):focus[^}]*\{[^}]*outline-style:none`).MatchString(css) {
		t.Error("form focus styling suppresses the high-contrast focus-visible outline")
	}
	if !regexp.MustCompile(`\.skip-link\{[^}]*z-index:var\(--tblr-toast-zindex,1090\)`).MatchString(css) {
		t.Error("skip link must render above Tabler's fixed navigation")
	}
	if strings.Contains(css, `content:attr(data-loading-label)`) || regexp.MustCompile(`button\[aria-busy=true\]:after[^}]*\{[^}]*position:absolute`).MatchString(css) {
		t.Error("busy feedback overlays a replacement label instead of preserving stable button geometry")
	}

	js := readStaticSource(t, "app.js")
	for _, want := range []string{`aria-busy`, `data-loading-label`, `enhancedExpanded`} {
		if !strings.Contains(js, want) {
			t.Errorf("progressive state feedback missing %q", want)
		}
	}
}

func requireTagWithClassTokens(t *testing.T, body, tagName string, required ...string) string {
	t.Helper()
	tags := tagsWithClassTokens(body, tagName, required...)
	if len(tags) == 0 {
		t.Fatalf("rendered HTML has no <%s> with class tokens %q", tagName, required)
	}
	return tags[0]
}

func requireTablerActionLink(t *testing.T, body, href, visibleLabel, iconClass string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?is)<a\b[^>]*\bhref="` + regexp.QuoteMeta(href) + `"[^>]*>.*?</a>`)
	action := pattern.FindString(body)
	if action == "" {
		t.Fatalf("rendered HTML has no action link with href %q", href)
	}
	startTags := htmlStartTags(action, "a")
	if len(startTags) != 1 {
		t.Fatalf("action link %q has %d start tags, want 1: %s", href, len(startTags), action)
	}
	requireClassTokens(t, startTags[0], "btn")
	icon := requireTagWithClassTokens(t, action, "i", "ti", iconClass)
	if got := htmlAttribute(t, icon, "aria-hidden"); got != "true" {
		t.Errorf("action link %q decorative icon aria-hidden = %q, want true", href, got)
	}
	visibleText := strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(action, ""))
	if !strings.Contains(visibleText, visibleLabel) {
		t.Errorf("action link %q visible text = %q, want label %q", href, visibleText, visibleLabel)
	}
	return action
}

func tagsWithClassTokens(body, tagName string, required ...string) []string {
	var matches []string
	for _, tag := range htmlStartTags(body, tagName) {
		classValue, ok := htmlAttributeValue(tag, "class")
		if !ok {
			continue
		}
		classes := make(map[string]struct{}, len(strings.Fields(classValue)))
		for _, className := range strings.Fields(classValue) {
			classes[className] = struct{}{}
		}
		containsAll := true
		for _, requiredClass := range required {
			if _, ok := classes[requiredClass]; !ok {
				containsAll = false
				break
			}
		}
		if containsAll {
			matches = append(matches, tag)
		}
	}
	return matches
}

func tagsWithClassPrefix(body, tagName, prefix string) []string {
	var matches []string
	for _, tag := range htmlStartTags(body, tagName) {
		classValue, ok := htmlAttributeValue(tag, "class")
		if !ok {
			continue
		}
		for _, className := range strings.Fields(classValue) {
			if strings.HasPrefix(className, prefix) {
				matches = append(matches, tag)
				break
			}
		}
	}
	return matches
}

func requireTagWithAttribute(t *testing.T, body, tagName, attribute, value string) string {
	t.Helper()
	for _, tag := range htmlStartTags(body, tagName) {
		if got, ok := htmlAttributeValue(tag, attribute); ok && got == value {
			return tag
		}
	}
	t.Fatalf("rendered HTML has no <%s> with %s=%q", tagName, attribute, value)
	return ""
}

func requireTagWithClassTokensAndAttribute(t *testing.T, body, tagName, attribute, value string, required ...string) string {
	t.Helper()
	for _, tag := range tagsWithClassTokens(body, tagName, required...) {
		if got, ok := htmlAttributeValue(tag, attribute); ok && got == value {
			return tag
		}
	}
	t.Fatalf("rendered HTML has no <%s> with class tokens %q and %s=%q", tagName, required, attribute, value)
	return ""
}

func requireClassTokens(t *testing.T, tag string, required ...string) {
	t.Helper()
	classValue, ok := htmlAttributeValue(tag, "class")
	if !ok {
		t.Fatalf("tag has no class attribute: %s", tag)
	}
	classes := make(map[string]struct{}, len(strings.Fields(classValue)))
	for _, className := range strings.Fields(classValue) {
		classes[className] = struct{}{}
	}
	for _, requiredClass := range required {
		if _, ok := classes[requiredClass]; !ok {
			t.Errorf("tag missing class token %q: %s", requiredClass, tag)
		}
	}
}

func htmlStartTags(body, tagName string) []string {
	tagPattern := regexp.QuoteMeta(tagName)
	if tagName == "*" {
		tagPattern = `[a-z][a-z0-9:-]*`
	}
	pattern := regexp.MustCompile(`(?i)<` + tagPattern + `\b[^>]*>`)
	return pattern.FindAllString(body, -1)
}

func htmlAttribute(t *testing.T, tag, name string) string {
	t.Helper()
	value, ok := htmlAttributeValue(tag, name)
	if !ok {
		t.Fatalf("tag has no %s attribute: %s", name, tag)
	}
	return value
}

func htmlAttributeValue(tag, name string) (string, bool) {
	pattern := regexp.MustCompile(`(?i)\s` + regexp.QuoteMeta(name) + `(?:="([^"]*)")?(?:\s|/?>)`)
	match := pattern.FindStringSubmatch(tag)
	if len(match) == 0 {
		return "", false
	}
	return match[1], true
}

func parseHTMLDocument(t *testing.T, body string) *xhtml.Node {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	return doc
}

func htmlElementNodes(root *xhtml.Node, tagName string) []*xhtml.Node {
	var nodes []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && (tagName == "*" || node.Data == tagName) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func requireHTMLNodeWithAttribute(t *testing.T, root *xhtml.Node, tagName, attribute, value string) *xhtml.Node {
	t.Helper()
	for _, node := range htmlElementNodes(root, tagName) {
		if got, ok := htmlNodeAttributeValue(node, attribute); ok && got == value {
			return node
		}
	}
	t.Fatalf("rendered HTML has no <%s> with %s=%q", tagName, attribute, value)
	return nil
}

func htmlNodeAttribute(t *testing.T, node *xhtml.Node, name string) string {
	t.Helper()
	value, ok := htmlNodeAttributeValue(node, name)
	if !ok {
		t.Fatalf("<%s> has no %s attribute", node.Data, name)
	}
	return value
}

func htmlNodeAttributeValue(node *xhtml.Node, name string) (string, bool) {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val, true
		}
	}
	return "", false
}

func htmlNodeHasClassTokens(node *xhtml.Node, required ...string) bool {
	if node == nil {
		return false
	}
	classValue, ok := htmlNodeAttributeValue(node, "class")
	if !ok {
		return false
	}
	classes := make(map[string]struct{}, len(strings.Fields(classValue)))
	for _, className := range strings.Fields(classValue) {
		classes[className] = struct{}{}
	}
	for _, requiredClass := range required {
		if _, ok := classes[requiredClass]; !ok {
			return false
		}
	}
	return true
}

func requireHTMLNodeClassTokens(t *testing.T, node *xhtml.Node, required ...string) {
	t.Helper()
	if !htmlNodeHasClassTokens(node, required...) {
		t.Errorf("<%s> missing class tokens %q", node.Data, required)
	}
}

func previousHTMLElementSibling(node *xhtml.Node) *xhtml.Node {
	for sibling := node.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
		if sibling.Type == xhtml.ElementNode {
			return sibling
		}
	}
	return nil
}

func htmlNodeText(node *xhtml.Node) string {
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

func requireUniqueHTMLIDs(t *testing.T, root *xhtml.Node) {
	t.Helper()
	counts := make(map[string]int)
	for _, node := range htmlElementNodes(root, "*") {
		if id, ok := htmlNodeAttributeValue(node, "id"); ok {
			counts[id]++
		}
	}
	for id, count := range counts {
		if count != 1 {
			t.Errorf("rendered HTML id %q occurs %d times, want once", id, count)
		}
	}
}

func iconOnlyHTMLControls(body string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<button\b[^>]*>.*?</button>`),
		regexp.MustCompile(`(?is)<a\b[^>]*>.*?</a>`),
	}
	stripTags := regexp.MustCompile(`<[^>]+>`)
	var controls []string
	for _, pattern := range patterns {
		for _, control := range pattern.FindAllString(body, -1) {
			if !strings.Contains(control, `navbar-toggler-icon`) && !strings.Contains(control, `class="ti `) {
				continue
			}
			openEnd := strings.IndexByte(control, '>')
			closeStart := strings.LastIndexByte(control, '<')
			if openEnd == -1 || closeStart <= openEnd {
				continue
			}
			visibleText := strings.TrimSpace(stripTags.ReplaceAllString(control[openEnd+1:closeStart], ""))
			if visibleText == "" {
				controls = append(controls, control)
			}
		}
	}
	return controls
}

func hasAccessibleName(control string) bool {
	openEnd := strings.IndexByte(control, '>')
	if openEnd == -1 {
		return false
	}
	startTag := control[:openEnd+1]
	for _, attribute := range []string{"aria-label", "aria-labelledby"} {
		if value, ok := htmlAttributeValue(startTag, attribute); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func readTemplateSource(t *testing.T, name string) string {
	t.Helper()
	body, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		t.Fatalf("read template %s: %v", name, err)
	}
	return string(body)
}

func readStaticSource(t *testing.T, name string) string {
	t.Helper()
	body, err := StaticFS.ReadFile("static/" + name)
	if err != nil {
		t.Fatalf("read static asset %s: %v", name, err)
	}
	return string(body)
}

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r
}

func renderPageBody(t *testing.T, name string, data any) string {
	t.Helper()
	r := newTestRenderer(t)
	if r.pages[name] == nil {
		t.Fatalf("renderer page %q is not registered", name)
	}
	w := httptest.NewRecorder()
	r.Render(w, name, data)
	if w.Code != http.StatusOK {
		t.Fatalf("Render %s status = %d; body=%s", name, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func blueprintFormRenderData() map[string]any {
	return map[string]any{
		"PageTitle":       "新建蓝图",
		"ActiveNav":       "blueprints",
		"Mode":            "create",
		"FormAction":      "/blueprints",
		"SubmitLabel":     "创建蓝图",
		"Error":           "请修正表单错误",
		"FieldErrors":     map[string]string{"name": "名称不能为空"},
		"ParamErrorField": "",
		"Projects":        []map[string]any{{"ID": int64(1), "Name": "demo-project"}},
		"Accounts":        []map[string]any{{"ID": int64(2), "Name": "dev", "AWSAccountID": "123456789012"}},
		"HasIngress":      false,
		"IngressPort":     0,
		"IngressProtocol": "",
		"IngressCIDR":     "",
		"Form": map[string]any{
			"Name":           "demo",
			"ProjectID":      int64(1),
			"CloudAccountID": int64(2),
			"Params": map[string]any{
				"Region": "ap-southeast-1",
				"EC2": map[string]any{
					"InstanceType": "t3.micro", "AMI": "", "Count": 1,
					"RootVolumeGB": 8, "KeyName": "",
				},
				"Network": map[string]any{
					"Enabled": false, "VPCCIDR": "10.0.0.0/16",
					"PublicSubnetCIDRs": []string{"10.0.1.0/24", "10.0.2.0/24"},
				},
				"RDS": map[string]any{
					"Enabled": false, "EngineVersion": "8.0", "InstanceClass": "db.t3.micro",
					"AllocatedStorageGB": 20, "DBName": "app", "Username": "admin",
				},
				"Redis": map[string]any{
					"Enabled": false, "AuthEnabled": false, "EngineVersion": "7.2",
					"NodeType": "cache.t3.micro", "NodeCount": 1,
				},
			},
		},
	}
}

func cssHexToken(t *testing.T, css, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`--color-` + regexp.QuoteMeta(name) + `:(#[0-9a-fA-F]{6}|#[0-9a-fA-F]{3})`)
	match := pattern.FindStringSubmatch(css)
	if len(match) != 2 {
		t.Fatalf("generated CSS missing hexadecimal color token %q", name)
	}
	if len(match[1]) == 4 {
		return "#" + strings.Repeat(match[1][1:2], 2) + strings.Repeat(match[1][2:3], 2) + strings.Repeat(match[1][3:4], 2)
	}
	return match[1]
}

func contrastRatio(foreground, background string) float64 {
	foregroundLuminance := relativeLuminance(foreground)
	backgroundLuminance := relativeLuminance(background)
	if foregroundLuminance < backgroundLuminance {
		foregroundLuminance, backgroundLuminance = backgroundLuminance, foregroundLuminance
	}
	return (foregroundLuminance + 0.05) / (backgroundLuminance + 0.05)
}

func relativeLuminance(hexColor string) float64 {
	channels := [3]float64{}
	for index := range channels {
		value, _ := strconv.ParseUint(hexColor[1+index*2:3+index*2], 16, 8)
		channel := float64(value) / 255
		if channel <= 0.04045 {
			channels[index] = channel / 12.92
		} else {
			channels[index] = math.Pow((channel+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}
