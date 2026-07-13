package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/crypto"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

func testDeps(t *testing.T) Deps {
	t.Helper()
	c, _ := crypto.NewCipher(make([]byte, 32))
	s, err := store.Open(":memory:", c)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := web.NewRenderer()
	if err != nil {
		t.Fatalf("web.NewRenderer: %v", err)
	}
	v := &cloud.Validator{ValidateFunc: func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		return cloud.Identity{AccountID: "123456789012", ARN: "arn:aws:iam::123456789012:user/x"}, nil
	}}
	return Deps{
		Store:     s,
		Validator: v,
		Auth:      auth.New("pw", []byte("k")),
		Renderer:  r,
		Catalog:   fakeCatalog{},

		DisableCatalogRefresh: true,
	}
}

func authedCreate(t *testing.T, deps Deps, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(deps.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func validAccountForm() url.Values {
	return url.Values{
		"name":              {"prod"},
		"default_region":    {"ap-southeast-1"},
		"access_key_id":     {"AKIAEXAMPLE"},
		"secret_access_key": {"test-secret-value"},
	}
}

func TestAccountListContainsDataButNoCreateForm(t *testing.T) {
	deps := testDeps(t)
	_, err := deps.Store.CreateCloudAccount(context.Background(), store.CloudAccount{
		Name: "prod-main", DefaultRegion: "ap-southeast-1", AccessKeyID: "AKIALIST",
		SecretAccessKey: "stored-only", AWSAccountID: "210987654321",
		ARN: "arn:aws:iam::210987654321:user/list",
	})
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}

	rec := authedGet(t, deps, "/accounts")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"prod-main", "210987654321", `href="/accounts/new"`, `class="table-wrap responsive-table-wrap"`, `class="data-table responsive-table"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("account list missing %q", want)
		}
	}
	for _, forbidden := range []string{`name="secret_access_key"`, `name="default_region"`, `action="/accounts"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("account list unexpectedly contains create control %q", forbidden)
		}
	}
}

func TestNewAccountPageRendersDedicatedForm(t *testing.T) {
	deps := testDeps(t)
	rec := authedGet(t, deps, "/accounts/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`action="/accounts"`, `method="post"`, `name="name"`, `name="default_region"`,
		`name="access_key_id"`, `name="secret_access_key"`, `type="password"`,
		`autocomplete="new-password"`, `data-password-toggle`, `href="/accounts"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("new account page missing %q", want)
		}
	}
}

func TestCreateAccountStoresDefaultRegion(t *testing.T) {
	deps := testDeps(t)
	var validateCalls int
	deps.Validator.ValidateFunc = func(_ context.Context, accessKey, secret, region string) (cloud.Identity, error) {
		validateCalls++
		if accessKey != "AKIAEXAMPLE" || secret == "" || region != validationRegion {
			t.Error("validator received unexpected credential fields")
		}
		return cloud.Identity{AccountID: "123456789012", ARN: "arn:aws:iam::123456789012:user/x"}, nil
	}
	form := validAccountForm()
	form.Set("name", "  prod  ")
	form.Set("default_region", "  ap-southeast-1  ")

	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "/accounts" {
		t.Fatalf("Location = %q, want /accounts", location)
	}
	if validateCalls != 1 {
		t.Fatalf("validator calls = %d, want 1", validateCalls)
	}
	list, err := deps.Store.ListCloudAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListCloudAccounts: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("accounts = %d, want 1", len(list))
	}
	if list[0].Name != "prod" || list[0].DefaultRegion != "ap-southeast-1" {
		t.Fatalf("stored account name/region = %q/%q", list[0].Name, list[0].DefaultRegion)
	}
}

func TestCreateAccountValidationPreservesSafeFieldsOnly(t *testing.T) {
	deps := testDeps(t)
	const submittedSecret = "must-not-appear-in-response"
	deps.Validator.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		return cloud.Identity{}, errors.New("credential failure: " + submittedSecret)
	}
	form := url.Values{
		"name":              {"  prod-edge  "},
		"default_region":    {"  ap-southeast-2  "},
		"access_key_id":     {"AKIA-SAFE-ID"},
		"secret_access_key": {submittedSecret},
	}

	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`value="prod-edge"`, `value="ap-southeast-2"`, `value="AKIA-SAFE-ID"`, `type="password"`, `role="alert"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("validation response missing safe form field %q", want)
		}
	}
	if strings.Contains(body, submittedSecret) {
		t.Fatal("validation response exposed the submitted secret")
	}
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 0 {
		t.Fatalf("accounts = %d, want 0 after validation failure", len(list))
	}
}

func TestCreateAccountRejectsInvalidFieldsBeforeCredentialValidation(t *testing.T) {
	deps := testDeps(t)
	var validateCalls int
	deps.Validator.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		validateCalls++
		return cloud.Identity{}, nil
	}
	form := validAccountForm()
	form.Set("name", strings.Repeat("x", 129))
	form.Set("default_region", " ")

	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if validateCalls != 0 {
		t.Fatalf("validator calls = %d, want 0", validateCalls)
	}
	if strings.Contains(rec.Body.String(), form.Get("secret_access_key")) {
		t.Fatal("invalid form response exposed the submitted secret")
	}
}

func TestCreateAccountCountsMultibyteAliasInCharacters(t *testing.T) {
	tests := []struct {
		name          string
		aliasRunes    int
		wantStatus    int
		wantValidates int
	}{
		{name: "below limit", aliasRunes: 127, wantStatus: http.StatusSeeOther, wantValidates: 1},
		{name: "at limit", aliasRunes: 128, wantStatus: http.StatusSeeOther, wantValidates: 1},
		{name: "over limit", aliasRunes: 129, wantStatus: http.StatusUnprocessableEntity, wantValidates: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := testDeps(t)
			validateCalls := 0
			deps.Validator.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
				validateCalls++
				return cloud.Identity{AccountID: "123456789012", ARN: "arn:aws:iam::123456789012:user/x"}, nil
			}
			form := validAccountForm()
			form.Set("name", "  "+strings.Repeat("账", tt.aliasRunes)+"  ")

			rec := authedCreate(t, deps, form)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if validateCalls != tt.wantValidates {
				t.Fatalf("validator calls = %d, want %d", validateCalls, tt.wantValidates)
			}
		})
	}
}

func TestCreateAccountDuplicateReturnsConflictForm(t *testing.T) {
	deps := testDeps(t)
	form := validAccountForm()
	if rec := authedCreate(t, deps, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("first create status = %d, want 303", rec.Code)
	}
	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已添加") {
		t.Fatal("duplicate response should explain the conflict")
	}
	if strings.Contains(rec.Body.String(), form.Get("secret_access_key")) {
		t.Fatal("duplicate response exposed the submitted secret")
	}
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 1 {
		t.Fatalf("accounts = %d, want 1 after duplicate", len(list))
	}
}

func TestCreateAccount_RequiresAuth(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated create: status = %d, want 303", rec.Code)
	}
}

func TestDeepPageMarksActiveNavigation(t *testing.T) {
	deps := testDeps(t)
	rec := authedGet(t, deps, "/accounts/new")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/accounts" aria-current="page"`) {
		t.Fatal("deep account page should mark Accounts as the active navigation item")
	}
	if strings.Count(body, `aria-current="page"`) != 1 {
		t.Fatalf("active navigation count = %d, want 1", strings.Count(body, `aria-current="page"`))
	}
}

func createAccountForDelete(t *testing.T, deps Deps, name, suffix, secret string) int64 {
	t.Helper()
	id, err := deps.Store.CreateCloudAccount(context.Background(), store.CloudAccount{
		Name: name, DefaultRegion: "ap-southeast-1", AccessKeyID: "AKIA" + suffix,
		SecretAccessKey: secret, AWSAccountID: "100000000" + suffix,
		ARN: "arn:aws:iam::100000000" + suffix + ":user/delete-test",
	})
	if err != nil {
		t.Fatalf("CreateCloudAccount: %v", err)
	}
	return id
}

func TestAccountDeleteListAndConfirmationProvideNoJavaScriptPathWithoutSecrets(t *testing.T) {
	deps := testDeps(t)
	const secret = "must-never-appear-in-delete-ui"
	const accessKey = "AKIA001"
	accountID := createAccountForDelete(t, deps, strings.Repeat("account-name-", 12), "001", secret)

	list := authedGet(t, deps, "/accounts")
	for _, want := range []string{
		`id="account-feedback"`,
		`id="account-delete-status" class="notice ok" role="status" aria-live="polite" tabindex="-1" hidden`,
		`href="/accounts/` + itoa(accountID) + `/delete"`,
		`hx-delete="/accounts/` + itoa(accountID) + `"`,
		`hx-confirm="删除账号`,
		`data-label="别名" class="long-value"`,
		`data-label="默认区域" class="long-value"`,
	} {
		if !strings.Contains(list.Body.String(), want) {
			t.Fatalf("account list missing %q: %s", want, list.Body.String())
		}
	}
	for _, leaked := range []string{secret, accessKey} {
		if strings.Contains(list.Body.String(), leaked) {
			t.Fatalf("account list exposed credential %q", leaked)
		}
	}

	confirmation := authedGet(t, deps, "/accounts/"+itoa(accountID)+"/delete")
	if confirmation.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d; body=%s", confirmation.Code, confirmation.Body.String())
	}
	for _, want := range []string{
		`role="alert"`,
		`action="/accounts/` + itoa(accountID) + `/delete"`,
		`type="submit"`,
		`class="long-value"`,
	} {
		if !strings.Contains(confirmation.Body.String(), want) {
			t.Fatalf("account confirmation missing %q: %s", want, confirmation.Body.String())
		}
	}
	for _, leaked := range []string{secret, accessKey} {
		if strings.Contains(confirmation.Body.String(), leaked) {
			t.Fatalf("account confirmation exposed credential %q", leaked)
		}
	}
}

func TestAccountDeleteNoJavaScriptPOSTRedirectsAfterSuccess(t *testing.T) {
	deps := testDeps(t)
	accountID := createAccountForDelete(t, deps, "delete-me", "002", "stored-secret")

	rec := authedPost(t, deps, "/accounts/"+itoa(accountID)+"/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/accounts" {
		t.Fatalf("POST delete status/location = %d %q; body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if _, err := deps.Store.GetCloudAccount(context.Background(), accountID); err == nil {
		t.Fatal("confirmed POST did not delete account")
	}
}

func TestAccountDeleteRejectsMalformedAndMissingIDsAcrossRoutes(t *testing.T) {
	deps := testDeps(t)
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/accounts/nope/delete", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/accounts/999/delete", want: http.StatusNotFound},
		{method: http.MethodPost, path: "/accounts/nope/delete", want: http.StatusBadRequest},
		{method: http.MethodPost, path: "/accounts/999/delete", want: http.StatusNotFound},
		{method: http.MethodDelete, path: "/accounts/nope", want: http.StatusBadRequest},
		{method: http.MethodDelete, path: "/accounts/999", want: http.StatusNotFound},
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

func TestHTMXAccountDeleteMalformedIDExposesSafeFeedbackEvent(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodDelete, "/accounts/not-a-number", nil)
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
	if got := events["account-delete-error"].Message; got != "账号 ID 无效" {
		t.Fatalf("feedback message = %q", got)
	}
}

func TestAccountDeleteReferenceFailureUsesRenderedNoJavaScriptAndAnnouncedHTMXErrors(t *testing.T) {
	deps := testDeps(t)
	const secret = "referenced-account-secret"
	accountID := createAccountForDelete(t, deps, "referenced-account", "003", secret)
	projectID, err := deps.Store.CreateProject(context.Background(), store.Project{Name: "owner"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := deps.Store.CreateBlueprint(context.Background(), store.Blueprint{
		ProjectID: projectID, CloudAccountID: accountID, Name: "reference", Params: validBPParams(),
	}); err != nil {
		t.Fatalf("CreateBlueprint: %v", err)
	}

	post := authedPost(t, deps, "/accounts/"+itoa(accountID)+"/delete", nil)
	if post.Code != http.StatusConflict {
		t.Fatalf("POST referenced status = %d, want 409; body=%s", post.Code, post.Body.String())
	}
	for _, want := range []string{`role="alert"`, "仍被蓝图或环境引用", "referenced-account"} {
		if !strings.Contains(post.Body.String(), want) {
			t.Fatalf("POST referenced response missing %q: %s", want, post.Body.String())
		}
	}
	for _, leaked := range []string{secret, "FOREIGN KEY", "constraint failed"} {
		if strings.Contains(post.Body.String(), leaked) {
			t.Fatalf("POST referenced response leaked %q", leaked)
		}
	}

	req := httptest.NewRequest(http.MethodDelete, "/accounts/"+itoa(accountID), nil)
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
	if got := events["account-delete-error"].Message; got != "该账号仍被蓝图或环境引用，无法删除" {
		t.Fatalf("feedback message = %q", got)
	}
}

func TestAccountDeleteHTMXSuccessReturnsBufferedRowsAndOperationalFailureIsSafe(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		deps := testDeps(t)
		targetID := createAccountForDelete(t, deps, "delete-me", "004", "target-secret")
		_ = createAccountForDelete(t, deps, "keep-me", "005", "kept-secret")

		req := httptest.NewRequest(http.MethodDelete, "/accounts/"+itoa(targetID), nil)
		req.Header.Set("HX-Request", "true")
		req.AddCookie(deps.Auth.IssueCookie())
		rec := httptest.NewRecorder()
		NewRouter(deps).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "keep-me") || strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Fatalf("HTMX success status/body = %d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "kept-secret") {
			t.Fatal("HTMX account rows exposed a stored secret")
		}
		assertAfterSwapDeleteSuccess(t, rec, "account-delete-success", "账号已删除")
	})

	t.Run("operational failure", func(t *testing.T) {
		deps := testDeps(t)
		accountID := createAccountForDelete(t, deps, "target", "006", "stored-secret")
		if _, err := deps.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_account_delete BEFORE DELETE ON cloud_accounts BEGIN SELECT RAISE(ABORT, 'sensitive account delete failure'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		req := httptest.NewRequest(http.MethodDelete, "/accounts/"+itoa(accountID), nil)
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
		accountID := createAccountForDelete(t, deps, "target-no-js", "008", "stored-secret")
		if _, err := deps.Store.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_account_post_delete BEFORE DELETE ON cloud_accounts BEGIN SELECT RAISE(ABORT, 'sensitive account POST failure'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		rec := authedPost(t, deps, "/accounts/"+itoa(accountID)+"/delete", nil)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), "target-no-js") {
			t.Fatalf("no-JS operational failure status/body = %d %s", rec.Code, rec.Body.String())
		}
		for _, leaked := range []string{"sensitive", "stored-secret", "constraint failed"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Fatalf("no-JS operational failure leaked %q", leaked)
			}
		}
	})
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 0x7f {
			return false
		}
	}
	return true
}
