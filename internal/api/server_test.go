package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type headerCountingRecorder struct {
	*httptest.ResponseRecorder
	writeHeaders int
}

func (w *headerCountingRecorder) WriteHeader(status int) {
	w.writeHeaders++
	w.ResponseRecorder.WriteHeader(status)
}

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

func TestRenderedErrorResponsesCommitStatusExactlyOnce(t *testing.T) {
	deps := testDeps(t)
	for _, tc := range []struct {
		name       string
		path       string
		form       url.Values
		authed     bool
		wantStatus int
		wantBody   string
	}{
		{name: "invalid login", path: "/login", form: url.Values{"password": {"wrong"}}, wantStatus: http.StatusUnauthorized, wantBody: "口令错误"},
		{name: "invalid account", path: "/accounts", form: url.Values{"name": {""}}, authed: true, wantStatus: http.StatusUnprocessableEntity, wantBody: "请输入账号别名"},
		{name: "invalid project", path: "/projects", form: url.Values{"name": {""}}, authed: true, wantStatus: http.StatusUnprocessableEntity, wantBody: "请输入项目名"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.authed {
				req.AddCookie(deps.Auth.IssueCookie())
			}
			rec := &headerCountingRecorder{ResponseRecorder: httptest.NewRecorder()}
			NewRouter(deps).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.writeHeaders != 1 {
				t.Fatalf("WriteHeader calls = %d, want exactly 1", rec.writeHeaders)
			}
			if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want rendered HTML", got)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) || strings.Count(rec.Body.String(), "<!doctype html>") != 1 {
				t.Fatalf("rendered error body missing %q or malformed: %s", tc.wantBody, rec.Body.String())
			}
		})
	}
}
