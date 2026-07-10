package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/store"
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

func TestProjectListContainsDataButNoCreateForm(t *testing.T) {
	deps := testDeps(t)
	if _, err := deps.Store.CreateProject(context.Background(), store.Project{Name: "web", Description: "public API"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	rec := authedGet(t, deps, "/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"web", "public API", `href="/projects/new"`, `class="table-wrap responsive-table-wrap"`, `class="data-table responsive-table"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project list missing %q", want)
		}
	}
	for _, forbidden := range []string{`name="description"`, `action="/projects"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("project list unexpectedly contains create control %q", forbidden)
		}
	}
}

func TestNewProjectPageRendersDedicatedForm(t *testing.T) {
	deps := testDeps(t)
	rec := authedGet(t, deps, "/projects/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/projects"`, `method="post"`, `name="name"`, `name="description"`, `href="/projects"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("new project page missing %q", want)
		}
	}
}

func TestCreateProjectPersistsAndRedirects(t *testing.T) {
	deps := testDeps(t)
	rec := authedPost(t, deps, "/projects", url.Values{"name": {"  web  "}, "description": {"  public API  "}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/projects" {
		t.Fatalf("Location = %q, want /projects", location)
	}
	list, err := deps.Store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 1 || list[0].Name != "web" || list[0].Description != "public API" {
		t.Fatalf("stored project = %+v", list)
	}
}

func TestCreateProjectValidationReturns422AndPreservesInput(t *testing.T) {
	deps := testDeps(t)
	name := strings.Repeat("n", 129)
	description := `owner <platform> & "ops"`
	rec := authedPost(t, deps, "/projects", url.Values{"name": {name}, "description": {description}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{name, `owner &lt;platform&gt; &amp; &#34;ops&#34;`, `role="alert"`, `action="/projects"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project validation response missing %q", want)
		}
	}
	list, _ := deps.Store.ListProjects(context.Background())
	if len(list) != 0 {
		t.Fatalf("projects = %d, want 0 after invalid submission", len(list))
	}
}

func TestCreateProjectCountsMultibyteFieldsInCharacters(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		description string
		wantStatus  int
	}{
		{name: "name below limit", projectName: "  " + strings.Repeat("项", 127) + "  ", wantStatus: http.StatusSeeOther},
		{name: "name at limit", projectName: "  " + strings.Repeat("项", 128) + "  ", wantStatus: http.StatusSeeOther},
		{name: "name over limit", projectName: strings.Repeat("项", 129), wantStatus: http.StatusUnprocessableEntity},
		{name: "empty optional description", projectName: "project", description: "", wantStatus: http.StatusSeeOther},
		{name: "description below limit", projectName: "project", description: "  " + strings.Repeat("述", 999) + "  ", wantStatus: http.StatusSeeOther},
		{name: "description at limit", projectName: "project", description: "  " + strings.Repeat("述", 1000) + "  ", wantStatus: http.StatusSeeOther},
		{name: "description over limit", projectName: "project", description: strings.Repeat("述", 1001), wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDeps(t)
			rec := authedPost(t, deps, "/projects", url.Values{
				"name":        {tt.projectName},
				"description": {tt.description},
			})
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestProjectDescriptionErrorIsAssociatedWithTextarea(t *testing.T) {
	deps := testDeps(t)
	rec := authedPost(t, deps, "/projects", url.Values{
		"name":        {"project"},
		"description": {strings.Repeat("d", 1001)},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`aria-invalid="true"`,
		`aria-describedby="project-description-error"`,
		`id="project-description-error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("description validation response missing %q", want)
		}
	}

	rec = authedGet(t, deps, "/projects/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("new page status = %d, want 200", rec.Code)
	}
	body = rec.Body.String()
	for _, forbidden := range []string{
		`aria-describedby="project-description-error"`,
		`id="project-description-error"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("valid description field unexpectedly contains %q", forbidden)
		}
	}
}
