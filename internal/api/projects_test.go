package api

import (
	"context"
	"encoding/json"
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

func createProjectForDelete(t *testing.T, deps Deps, name, description string) int64 {
	t.Helper()
	id, err := deps.Store.CreateProject(context.Background(), store.Project{Name: name, Description: description})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return id
}

func TestProjectDeleteListAndConfirmationProvideNoJavaScriptPath(t *testing.T) {
	deps := testDeps(t)
	projectID := createProjectForDelete(t, deps, strings.Repeat("project-name-", 12), strings.Repeat("description-", 20))

	list := authedGet(t, deps, "/projects")
	for _, want := range []string{
		`id="project-feedback"`,
		`id="project-delete-status" class="notice ok" role="status" aria-live="polite" tabindex="-1" hidden`,
		`href="/projects/` + itoa(projectID) + `/delete"`,
		`hx-delete="/projects/` + itoa(projectID) + `"`,
		`hx-confirm="删除项目`,
		`data-label="名称" class="long-value"`,
		`data-label="描述" class="long-value"`,
	} {
		if !strings.Contains(list.Body.String(), want) {
			t.Fatalf("project list missing %q: %s", want, list.Body.String())
		}
	}

	confirmation := authedGet(t, deps, "/projects/"+itoa(projectID)+"/delete")
	if confirmation.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d; body=%s", confirmation.Code, confirmation.Body.String())
	}
	for _, want := range []string{
		`role="alert"`,
		`action="/projects/` + itoa(projectID) + `/delete"`,
		`type="submit"`,
		`class="long-value"`,
	} {
		if !strings.Contains(confirmation.Body.String(), want) {
			t.Fatalf("project confirmation missing %q: %s", want, confirmation.Body.String())
		}
	}
}

func TestProjectDeleteNoJavaScriptPOSTRedirectsAfterSuccess(t *testing.T) {
	deps := testDeps(t)
	projectID := createProjectForDelete(t, deps, "delete-me", "temporary")

	rec := authedPost(t, deps, "/projects/"+itoa(projectID)+"/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/projects" {
		t.Fatalf("POST delete status/location = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if _, err := deps.Store.GetProject(context.Background(), projectID); err == nil {
		t.Fatal("confirmed POST did not delete project")
	}
}

func TestProjectDeleteRejectsMalformedAndMissingIDsAcrossRoutes(t *testing.T) {
	deps := testDeps(t)
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/projects/nope/delete", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/projects/999/delete", want: http.StatusNotFound},
		{method: http.MethodPost, path: "/projects/nope/delete", want: http.StatusBadRequest},
		{method: http.MethodPost, path: "/projects/999/delete", want: http.StatusNotFound},
		{method: http.MethodDelete, path: "/projects/nope", want: http.StatusBadRequest},
		{method: http.MethodDelete, path: "/projects/999", want: http.StatusNotFound},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.AddCookie(deps.Auth.IssueCookie())
			rec := httptest.NewRecorder()
			NewRouter(deps).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestHTMXProjectDeleteMalformedIDExposesSafeFeedbackEvent(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodDelete, "/projects/not-a-number", nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(deps.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	header := rec.Header().Get("HX-Trigger")
	if !isASCII(header) {
		t.Fatalf("HX-Trigger is not transport-safe ASCII: %q", header)
	}
	var events map[string]struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(header), &events); err != nil {
		t.Fatalf("HX-Trigger is invalid JSON: %q: %v", header, err)
	}
	if got := events["project-delete-error"].Message; got != "项目 ID 无效" {
		t.Fatalf("feedback message = %q", got)
	}
}

func TestProjectDeleteReferenceFailureUsesRenderedNoJavaScriptAndAnnouncedHTMXErrors(t *testing.T) {
	deps := testDeps(t)
	projectID := createProjectForDelete(t, deps, "referenced-project", "still in use")
	accountID := createAccountForDelete(t, deps, "owner", "007", "stored-secret")
	if _, err := deps.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: projectID, CloudAccountID: accountID, Name: "reference", Params: validBPParams(),
	}); err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	post := authedPost(t, deps, "/projects/"+itoa(projectID)+"/delete", nil)
	if post.Code != http.StatusConflict {
		t.Fatalf("POST referenced status = %d, want 409; body=%s", post.Code, post.Body.String())
	}
	for _, want := range []string{`role="alert"`, "仍有蓝图引用", "referenced-project"} {
		if !strings.Contains(post.Body.String(), want) {
			t.Fatalf("POST referenced response missing %q: %s", want, post.Body.String())
		}
	}
	for _, leaked := range []string{"FOREIGN KEY", "constraint failed", "stored-secret"} {
		if strings.Contains(post.Body.String(), leaked) {
			t.Fatalf("POST referenced response leaked %q", leaked)
		}
	}

	req := httptest.NewRequest(http.MethodDelete, "/projects/"+itoa(projectID), nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(deps.Auth.IssueCookie())
	htmx := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(htmx, req)
	if htmx.Code != http.StatusConflict {
		t.Fatalf("HTMX referenced status = %d, want 409; body=%s", htmx.Code, htmx.Body.String())
	}
	var events map[string]struct {
		Message string `json:"message"`
	}
	header := htmx.Header().Get("HX-Trigger")
	if !isASCII(header) {
		t.Fatalf("HX-Trigger is not transport-safe ASCII: %q", header)
	}
	if err := json.Unmarshal([]byte(header), &events); err != nil {
		t.Fatalf("HX-Trigger is invalid JSON: %q: %v", header, err)
	}
	if got := events["project-delete-error"].Message; got != "该项目仍有蓝图引用，无法删除" {
		t.Fatalf("feedback message = %q", got)
	}
}

func TestProjectDeleteHTMXSuccessReturnsBufferedRowsAndOperationalFailureIsSafe(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		deps := testDeps(t)
		targetID := createProjectForDelete(t, deps, "delete-me", "temporary")
		_ = createProjectForDelete(t, deps, "keep-me", "persistent")

		req := httptest.NewRequest(http.MethodDelete, "/projects/"+itoa(targetID), nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(deps.Auth.IssueCookie())
		rec := httptest.NewRecorder()
		NewRouter(deps).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "keep-me") || strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Fatalf("HTMX success status/body = %d %s", rec.Code, rec.Body.String())
		}
		assertAfterSwapDeleteSuccess(t, rec, "project-delete-success", "项目已删除")
	})

	t.Run("operational failure", func(t *testing.T) {
		deps := testDeps(t)
		projectID := createProjectForDelete(t, deps, "target", "temporary")
		if _, err := deps.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_project_delete BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT, 'sensitive project delete failure'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		req := httptest.NewRequest(http.MethodDelete, "/projects/"+itoa(projectID), nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(deps.Auth.IssueCookie())
		rec := httptest.NewRecorder()
		NewRouter(deps).ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sensitive") {
			t.Fatalf("operational failure status/body = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no JavaScript operational failure", func(t *testing.T) {
		deps := testDeps(t)
		projectID := createProjectForDelete(t, deps, "target-no-js", "temporary")
		if _, err := deps.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_project_post_delete BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT, 'sensitive project POST failure'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		rec := authedPost(t, deps, "/projects/"+itoa(projectID)+"/delete", nil)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), "target-no-js") {
			t.Fatalf("no-JS operational failure status/body = %d %s", rec.Code, rec.Body.String())
		}
		for _, leaked := range []string{"sensitive", "constraint failed"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Fatalf("no-JS operational failure leaked %q", leaked)
			}
		}
	})
}
