package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func authedPost(t *testing.T, deps Deps, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(deps.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func TestCreateProjectPersistsAndRendersRows(t *testing.T) {
	deps := testDeps(t)
	rec := authedPost(t, deps, "/projects", url.Values{"name": {"web"}, "description": {"d"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "web") {
		t.Fatalf("rows should show new project: %s", rec.Body.String())
	}
	list, _ := deps.Store.ListProjects(context.Background())
	if len(list) != 1 {
		t.Fatalf("project not persisted: %d", len(list))
	}
}
