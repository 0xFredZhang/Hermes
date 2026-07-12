package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
)

// fakeCatalog implements CatalogAPI; nil func fields return empty results.
type fakeCatalog struct {
	regions func() ([]string, error)
	itypes  func() ([]cloud.InstanceType, error)
	arch    func() (string, error)
	images  func() ([]cloud.Image, error)
}

func (f fakeCatalog) Regions(context.Context, string, string) ([]string, error) {
	if f.regions != nil {
		return f.regions()
	}
	return nil, nil
}
func (f fakeCatalog) InstanceTypes(context.Context, string, string, string) ([]cloud.InstanceType, error) {
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

func seedCatalogCacheJSON(t *testing.T, d Deps, accountID int64, kind, region, lookupKey string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := d.Store.UpsertCatalogCache(context.Background(), store.CatalogCacheEntry{
		AccountID: accountID,
		Kind:      kind,
		Region:    region,
		LookupKey: lookupKey,
		Payload:   string(raw),
	}); err != nil {
		t.Fatalf("UpsertCatalogCache: %v", err)
	}
}

func TestRegionsEndpointRendersOptions(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"ap-southeast-1", "us-west-2"})
	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<option value="ap-southeast-1" selected>亚太地区（新加坡） · ap-southeast-1</option>`) {
		t.Fatalf("missing region option: %s", rec.Body.String())
	}
}

func TestRegionsEndpointRendersReadableRegionLabels(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"ap-east-1"})

	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="ap-east-1" selected>亚太地区（香港） · ap-east-1</option>`) {
		t.Fatalf("missing readable region label: %s", body)
	}
}

func TestRegionsEndpointUsesCachedCatalogResult(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	calls := 0
	d.Catalog = fakeCatalog{regions: func() ([]string, error) {
		calls++
		return []string{"ap-east-1"}, nil
	}}
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"ap-east-1"})

	authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))
	authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))

	if calls != 0 {
		t.Fatalf("Regions calls = %d, want 0 synchronous calls with DB cache", calls)
	}
}

func TestRegionsEndpointReturnsDefaultWhileCacheWarms(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	calls := 0
	d.Catalog = fakeCatalog{regions: func() ([]string, error) {
		calls++
		return []string{"ap-east-1"}, nil
	}}

	rec := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid))

	if calls != 0 {
		t.Fatalf("Regions calls = %d, want 0 synchronous calls on cache miss", calls)
	}
	if !strings.Contains(rec.Body.String(), `亚太地区（新加坡） · ap-southeast-1`) {
		t.Fatalf("missing default region fallback: %s", rec.Body.String())
	}
}

func TestInstanceTypesEndpointUsesCachedRegion(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	calls := 0
	d.Catalog = fakeCatalog{itypes: func() ([]cloud.InstanceType, error) {
		calls++
		return []cloud.InstanceType{{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024}}, nil
	}}
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheInstanceTypes, "ap-east-1", "", []cloud.InstanceType{{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024}})

	path := "/blueprints/instance-types?cloud_account_id=" + itoa(aid) + "&region=ap-east-1"
	authedGet(t, d, path)
	authedGet(t, d, path)

	if calls != 0 {
		t.Fatalf("InstanceTypes calls = %d, want 0 synchronous calls with DB cache", calls)
	}
}

func TestInstanceTypesEndpointRendersVisibleSelectOptions(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheInstanceTypes, "ap-east-1", "", []cloud.InstanceType{
		{Name: "c7g.large", VCPUs: 2, MemoryMiB: 4096},
		{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024},
	})

	rec := authedGet(t, d, "/blueprints/instance-types?cloud_account_id="+itoa(aid)+"&region=ap-east-1")
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="t3.micro" selected>t3.micro · 2C1G</option>`) {
		t.Fatalf("missing visible selected instance type option: %s", body)
	}
	if !strings.Contains(body, `<option value="c7g.large">c7g.large · 2C4G</option>`) {
		t.Fatalf("missing visible instance type option: %s", body)
	}
}

func TestAMIsEndpointUsesCachedArchitectureAndImages(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	archCalls, imageCalls := 0, 0
	d.Catalog = fakeCatalog{
		arch: func() (string, error) {
			archCalls++
			return "x86_64", nil
		},
		images: func() ([]cloud.Image, error) {
			imageCalls++
			return []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}}, nil
		},
	}
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheArchitecture, "ap-east-1", "t3.micro", "x86_64")
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheImages, "ap-east-1", "x86_64", []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}})

	path := "/blueprints/amis?cloud_account_id=" + itoa(aid) + "&region=ap-east-1&instance_type=t3.micro"
	authedGet(t, d, path)
	authedGet(t, d, path)

	if archCalls != 0 || imageCalls != 0 {
		t.Fatalf("arch calls = %d, image calls = %d, want 0/0 synchronous calls with DB cache", archCalls, imageCalls)
	}
}

func TestAMIsEndpointRendersFallbackAndSelectedDefault(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheArchitecture, "ap-southeast-1", "t3.micro", "x86_64")
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheImages, "ap-southeast-1", "x86_64", []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}})
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

func TestRefreshCatalogCacheWarmsDefaultBlueprintMetadata(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	d.Catalog = fakeCatalog{
		regions: func() ([]string, error) { return []string{"ap-southeast-1"}, nil },
		itypes: func() ([]cloud.InstanceType, error) {
			return []cloud.InstanceType{{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024}}, nil
		},
		arch: func() (string, error) { return "x86_64", nil },
		images: func() ([]cloud.Image, error) {
			return []cloud.Image{{ID: "ami-123", Name: "Ubuntu 26.04 LTS", Default: true}}, nil
		},
	}
	acc, err := d.Store.GetCloudAccount(context.Background(), aid)
	if err != nil {
		t.Fatalf("GetCloudAccount: %v", err)
	}

	if err := RefreshCatalogCache(context.Background(), d, acc); err != nil {
		t.Fatalf("RefreshCatalogCache: %v", err)
	}

	for _, tc := range []struct {
		kind      string
		region    string
		lookupKey string
	}{
		{store.CatalogCacheRegions, "", ""},
		{store.CatalogCacheInstanceTypes, "ap-southeast-1", ""},
		{store.CatalogCacheArchitecture, "ap-southeast-1", "t3.micro"},
		{store.CatalogCacheImages, "ap-southeast-1", "x86_64"},
	} {
		if _, err := d.Store.GetCatalogCache(context.Background(), aid, tc.kind, tc.region, tc.lookupKey); err != nil {
			t.Fatalf("missing warmed cache %s/%s/%s: %v", tc.kind, tc.region, tc.lookupKey, err)
		}
	}
}

func TestMetadataOptionsPreserveSelectedLegacyValues(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"ap-southeast-1"})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheInstanceTypes, "legacy-region-1", "", []cloud.InstanceType{{Name: "t3.micro", VCPUs: 2, MemoryMiB: 1024}})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheArchitecture, "legacy-region-1", "legacy.large", "x86_64")
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheImages, "legacy-region-1", "x86_64", []cloud.Image{{ID: "ami-current", Name: "Current"}})

	regions := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid)+"&selected_region=legacy-region-1").Body.String()
	if !strings.Contains(regions, `<option value="legacy-region-1" selected>`) {
		t.Fatalf("legacy region was not preserved: %s", regions)
	}
	types := authedGet(t, d, "/blueprints/instance-types?cloud_account_id="+itoa(aid)+"&region=legacy-region-1&selected_instance_type=legacy.large").Body.String()
	if !strings.Contains(types, `<option value="legacy.large" selected>legacy.large</option>`) {
		t.Fatalf("legacy instance type was not preserved: %s", types)
	}
	amis := authedGet(t, d, "/blueprints/amis?cloud_account_id="+itoa(aid)+"&region=legacy-region-1&instance_type=legacy.large&selected_ami=ami-legacy").Body.String()
	if !strings.Contains(amis, `<option value="ami-legacy" selected>ami-legacy</option>`) {
		t.Fatalf("legacy AMI was not preserved: %s", amis)
	}
}

func TestMetadataOptionsDoNotForceLegacyValuesAfterHintsReset(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"eu-west-1"})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheInstanceTypes, "eu-west-1", "", []cloud.InstanceType{{Name: "m7g.large"}})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheArchitecture, "eu-west-1", "m7g.large", "arm64")
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheImages, "eu-west-1", "arm64", []cloud.Image{{ID: "ami-arm", Name: "ARM image", Default: true}})

	regions := authedGet(t, d, "/blueprints/regions?cloud_account_id="+itoa(aid)+"&selected_region=").Body.String()
	if strings.Contains(regions, "legacy-region") || strings.Contains(regions, `value="ap-southeast-1"`) || !strings.Contains(regions, `<option value="eu-west-1" selected>`) {
		t.Fatalf("reset region options = %s", regions)
	}
	types := authedGet(t, d, "/blueprints/instance-types?cloud_account_id="+itoa(aid)+"&region=eu-west-1&selected_instance_type=").Body.String()
	if strings.Contains(types, "legacy.large") || strings.Contains(types, `value="t3.micro"`) || !strings.Contains(types, `<option value="m7g.large" selected>`) {
		t.Fatalf("reset instance options = %s", types)
	}
	amis := authedGet(t, d, "/blueprints/amis?cloud_account_id="+itoa(aid)+"&region=eu-west-1&instance_type=m7g.large&selected_ami=").Body.String()
	if strings.Contains(amis, "ami-legacy") || !strings.Contains(amis, `value="ami-arm"`) {
		t.Fatalf("reset AMI options = %s", amis)
	}
}

func TestARMOnlyCatalogCascadeUsesCatalogInstanceAndAMI(t *testing.T) {
	d := testDeps(t)
	_, aid := seedProjectAccount(t, d)
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheRegions, "", "", []string{"eu-west-1"})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheInstanceTypes, "eu-west-1", "", []cloud.InstanceType{{Name: "m7g.large"}})
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheArchitecture, "eu-west-1", "m7g.large", "arm64")
	seedCatalogCacheJSON(t, d, aid, store.CatalogCacheImages, "eu-west-1", "arm64", []cloud.Image{{ID: "ami-arm", Name: "ARM image", Default: true}})

	types := authedGet(t, d, "/blueprints/instance-types?cloud_account_id="+itoa(aid)+"&region=eu-west-1&selected_instance_type=").Body.String()
	if strings.Contains(types, `value="t3.micro"`) || !strings.Contains(types, `<option value="m7g.large" selected>`) {
		t.Fatalf("ARM-only instance cascade selected an unavailable default: %s", types)
	}
	amIs := authedGet(t, d, "/blueprints/amis?cloud_account_id="+itoa(aid)+"&region=eu-west-1&instance_type=m7g.large&selected_ami=").Body.String()
	if !strings.Contains(amIs, `<option value="ami-arm" selected>ARM image</option>`) {
		t.Fatalf("ARM-only cascade did not resolve the catalog AMI: %s", amIs)
	}
}
