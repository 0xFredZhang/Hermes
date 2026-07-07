package api

import (
	"context"
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
	// validator that always succeeds with a fixed identity
	v := &cloud.Validator{NewClient: nil}
	v.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		return cloud.Identity{AccountID: "123456789012", ARN: "arn:aws:iam::123456789012:user/x"}, nil
	}
	return Deps{
		Store:     s,
		Validator: v,
		Auth:      auth.New("pw", []byte("k")),
		Renderer:  r,
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

func TestCreateAccount_ValidatesAndPersists(t *testing.T) {
	deps := testDeps(t)
	form := url.Values{
		"name":              {"prod"},
		"default_region":    {"ap-southeast-1"},
		"access_key_id":     {"AKIA"},
		"secret_access_key": {"secret"},
	}
	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "123456789012") {
		t.Fatalf("response should show validated account id; got %s", rec.Body.String())
	}
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 1 || list[0].Name != "prod" {
		t.Fatalf("account not persisted: %+v", list)
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

func TestCreateAccount_InvalidCredentials(t *testing.T) {
	deps := testDeps(t)
	// Override validator to fail
	deps.Validator.ValidateFunc = func(_ context.Context, _, _, _ string) (cloud.Identity, error) {
		return cloud.Identity{}, errors.New("InvalidClientTokenId")
	}
	form := url.Values{
		"name":              {"prod"},
		"default_region":    {"ap-southeast-1"},
		"access_key_id":     {"AKIA"},
		"secret_access_key": {"bad"},
	}
	rec := authedCreate(t, deps, form)
	// Should return 200 so htmx swaps the error row
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Response should contain the error message
	if !strings.Contains(rec.Body.String(), "凭证验证失败") {
		t.Fatalf("response should contain validation error; got %s", rec.Body.String())
	}
	// Account should not be persisted
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 0 {
		t.Fatalf("account should not be persisted on validation failure; got %d accounts", len(list))
	}
}

func TestCreateAccount_Duplicate(t *testing.T) {
	deps := testDeps(t)
	form := url.Values{
		"name":              {"prod"},
		"default_region":    {"ap-southeast-1"},
		"access_key_id":     {"AKIA"},
		"secret_access_key": {"secret"},
	}
	// First add succeeds.
	if rec := authedCreate(t, deps, form); rec.Code != http.StatusOK {
		t.Fatalf("first add: status = %d", rec.Code)
	}
	// Second add of the same validated AWS account is rejected with a friendly
	// message (200 so htmx swaps it), and no second row is persisted.
	rec := authedCreate(t, deps, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("second add: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已添加") {
		t.Fatalf("second add should show duplicate message; got %s", rec.Body.String())
	}
	list, _ := deps.Store.ListCloudAccounts(context.Background())
	if len(list) != 1 {
		t.Fatalf("duplicate must not create a second account; got %d", len(list))
	}
}
