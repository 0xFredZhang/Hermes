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
		"dialog::backdrop", `content:attr(data-label)`, `.table-wrap{margin-top:`, `:focus-visible`,
		`@media (max-width:39.999rem){.responsive-table-wrap{border:0`,
		`@media (prefers-reduced-motion:reduce)`, `button[data-loading-label][aria-busy=true]:after`,
		`.blueprint-form-page`, `.summary-grid`, `.disclosure-toggle`,
		`.job-history-wrap`, `.status-badge`, `.diagnostic-grid`, `.log-panel{max-height:420px`, `.log-panel-full{max-height:70vh`, `.copy-log-status`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("static app stylesheet missing %q", want)
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
		`<span class="status-badge status-success">运行中</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered environment table missing %q", want)
		}
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
		`<label class="field-label" for="blueprint-name">名称 *</label>`,
		`<input id="blueprint-name" name="name" value="demo" required aria-invalid="true" aria-describedby="error-name">`,
		`<span class="field-error" id="error-name" role="alert">名称不能为空</span>`,
		`class="disclosure-toggle" hidden aria-expanded="true" data-enhanced-expanded="false" aria-controls="network-fields"`,
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
			want: []string{`id="default-region"`, `aria-invalid="true" aria-describedby="default-region-error"`, `id="default-region-error" role="alert">区域无效`},
		},
		{
			page: "project_form",
			data: map[string]any{"Error": "请检查标出的字段。", "Name": "ops", "Description": "too long", "FieldErrors": map[string]string{"description": "描述过长"}},
			want: []string{`id="project-description"`, `aria-invalid="true" aria-describedby="project-description-error"`, `id="project-description-error" role="alert">描述过长`},
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

func TestStatusBadgesIncludeText(t *testing.T) {
	fragments := readTemplateSource(t, "_fragments.html")
	for _, want := range []string{
		`define "environment_status_badge"`,
		`status-badge status-neutral">待预演`,
		`status-badge status-active">预演中`,
		`status-badge status-success">运行中`,
		`status-badge status-danger">失败`,
		`status-badge status-neutral">已销毁`,
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
		want   string
	}{
		{"pending", `status-neutral">待预演`},
		{"previewing", `status-active">预演中`},
		{"up", `status-success">运行中`},
		{"failed", `status-danger">失败`},
		{"destroyed", `status-neutral">已销毁`},
	} {
		t.Run(tc.status, func(t *testing.T) {
			var body bytes.Buffer
			r := newTestRenderer(t)
			if err := r.RenderPartial(&body, "env_status", map[string]any{
				"Env": map[string]any{"ID": int64(9), "Name": "demo", "Status": tc.status},
			}); err != nil {
				t.Fatalf("RenderPartial env_status: %v", err)
			}
			if !strings.Contains(body.String(), tc.want) {
				t.Errorf("rendered status %q missing labelled badge %q: %s", tc.status, tc.want, body.String())
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
		`<dd class="long-value">` + name + `</dd>`,
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
