package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRendererParsesAllPages(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	for _, name := range []string{"login", "accounts", "account_form", "projects", "project_form", "blueprints", "blueprint_form", "blueprint_detail", "blueprint_deploy", "blueprint_delete", "environments", "environment_detail", "job_detail"} {
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

func TestStaticAppCSSIsEmbedded(t *testing.T) {
	css, err := StaticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("static app stylesheet must be embedded: %v", err)
	}
	for _, want := range []string{
		"tailwindcss", ".app-shell", ".skip-link", ".form-surface", ".password-control",
		"dialog::backdrop", `content:attr(data-label)`, `.table-wrap{margin-top:`,
		`@media (max-width:39.999rem){.responsive-table-wrap{border:0`,
		`.blueprint-form-page`, `.summary-grid`, `.disclosure-toggle`,
		`.job-history-wrap`, `.status-badge`, `.diagnostic-grid`, `.log-panel-full`, `.copy-log-status`,
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("static app stylesheet missing %q", want)
		}
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

func TestRenderLayoutLoadsAppStylesheet(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	w := httptest.NewRecorder()
	r.Render(w, "login", nil)
	out := w.Body.String()
	for _, want := range []string{`href="/static/app.css"`, `class="app-shell"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("layout output missing %q: %s", want, out)
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
