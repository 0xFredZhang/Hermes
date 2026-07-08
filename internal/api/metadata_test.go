package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/cloud"
)

// fakeCatalog implements CatalogAPI; nil func fields return empty results.
type fakeCatalog struct {
	regions func() ([]string, error)
	itypes  func() ([]string, error)
	arch    func() (string, error)
	images  func() ([]cloud.Image, error)
}

func (f fakeCatalog) Regions(context.Context, string, string) ([]string, error) {
	if f.regions != nil {
		return f.regions()
	}
	return nil, nil
}
func (f fakeCatalog) InstanceTypes(context.Context, string, string, string) ([]string, error) {
	if f.itypes != nil {
		return f.itypes()
	}
	return nil, nil
}
func (f fakeCatalog) Architecture(context.Context, string, string, string, string) (string, error) {
	if f.arch != nil {
		return f.arch()
	}
	return "x86_64", nil
}
func (f fakeCatalog) Images(context.Context, string, string, string, string) ([]cloud.Image, error) {
	if f.images != nil {
		return f.images()
	}
	return nil, nil
}

func authedGet(t *testing.T, d Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(d.Auth.IssueCookie())
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, req)
	return rec
}

func TestRegionsEndpointRendersOptions(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	d.Catalog = fakeCatalog{regions: func() ([]string, error) { return []string{"ap-southeast-1", "us-west-2"}, nil }}
	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<option value="ap-southeast-1">`) {
		t.Fatalf("missing region option: %s", rec.Body.String())
	}
}

func TestAMIsEndpointRendersFallbackAndSelectedDefault(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	d.Catalog = fakeCatalog{
		arch:   func() (string, error) { return "x86_64", nil },
		images: func() ([]cloud.Image, error) { return []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}}, nil },
	}
	rec := authedGet(t, d, "/blueprints/amis?cloud_account_id="+itoa(aid)+"&region=ap-southeast-1&instance_type=t3.micro")
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="">自动:最新 Ubuntu 26.04 LTS</option>`) {
		t.Fatalf("missing fallback option: %s", body)
	}
	if !strings.Contains(body, `<option value="ami-123" selected>Ubuntu 26.04 LTS</option>`) {
		t.Fatalf("missing selected default AMI: %s", body)
	}
}

func TestMetadataEndpointUnknownAccountDegrades(t *testing.T) {
	d := testDeps(t)
	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id=999")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 graceful", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请先选择云账号") {
		t.Fatalf("expected inline hint, got: %s", rec.Body.String())
	}
}
