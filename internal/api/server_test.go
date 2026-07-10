package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	NewRouter(testDeps(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want \"ok\"", got)
	}
}

func TestTopLevelPagesMarkActiveNavigation(t *testing.T) {
	deps := testDeps(t)
	tests := []struct {
		path  string
		nav   string
		title string
	}{
		{path: "/accounts", nav: "/accounts", title: "AWS 云账号"},
		{path: "/projects", nav: "/projects", title: "项目"},
		{path: "/blueprints", nav: "/blueprints", title: "蓝图"},
		{path: "/environments", nav: "/environments", title: "环境"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := authedGet(t, deps, tt.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `href="`+tt.nav+`" aria-current="page"`) {
				t.Fatal("top-level page did not mark its navigation item active")
			}
			if !strings.Contains(body, `<title>`+tt.title+` · Hermes</title>`) {
				t.Fatal("top-level page did not provide its document title")
			}
		})
	}
}
