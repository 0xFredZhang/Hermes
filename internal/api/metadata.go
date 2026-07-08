package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
)

const catalogCacheTTL = 24 * time.Hour

// CatalogAPI is the subset of cloud.Catalog the metadata handlers need.
// *cloud.Catalog satisfies it; tests inject a fake.
type CatalogAPI interface {
	Regions(ctx context.Context, accessKey, secret string) ([]string, error)
	InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]cloud.InstanceType, error)
	Architecture(ctx context.Context, accessKey, secret, region, instanceType string) (string, error)
	Images(ctx context.Context, accessKey, secret, region, arch string) ([]cloud.Image, error)
}

func addMetadataRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /blueprints/regions", func(w http.ResponseWriter, r *http.Request) {
		handleRegions(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/instance-types", func(w http.ResponseWriter, r *http.Request) {
		handleInstanceTypes(w, r, d)
	})
	mux.HandleFunc("GET /blueprints/amis", func(w http.ResponseWriter, r *http.Request) {
		handleAMIs(w, r, d)
	})
}

// resolveAccount reads cloud_account_id and returns its decrypted credentials.
// On failure it writes an inline hint option and returns ok=false.
func resolveAccount(w http.ResponseWriter, r *http.Request, d Deps) (store.CloudAccount, bool) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("cloud_account_id"), 10, 64)
	acc, err := d.Store.GetCloudAccount(r.Context(), id)
	if err != nil {
		writeOptions(w, nil, "请先选择云账号")
		return store.CloudAccount{}, false
	}
	return acc, true
}

func handleRegions(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return
	}
	regions, fresh, err := readCachedCatalogValue[[]string](r.Context(), d.Store, acc.ID, store.CatalogCacheRegions, "", "")
	if err != nil {
		regions = []string{defaultRegion(acc)}
		refreshCatalogCacheAsync(d, acc)
	} else if !fresh {
		refreshCatalogCacheAsync(d, acc)
	}
	writeRegionOptions(w, regions, defaultRegion(acc), "")
}

func handleInstanceTypes(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	if region == "" {
		writeOptions(w, nil, "请先选择 Region")
		return
	}
	itypes, fresh, err := readCachedCatalogValue[[]cloud.InstanceType](r.Context(), d.Store, acc.ID, store.CatalogCacheInstanceTypes, region, "")
	if err != nil {
		refreshRegionCacheAsync(d, acc, region)
		writeInstanceTypeOptions(w, []cloud.InstanceType{defaultInstanceTypeDetails()}, defaultInstanceType, "实例规格缓存正在更新，暂用默认规格")
		return
	}
	if !fresh {
		refreshRegionCacheAsync(d, acc, region)
	}
	writeInstanceTypeOptions(w, itypes, defaultInstanceType, "")
}

func handleAMIs(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return // resolveAccount already wrote the inline hint
	}
	q := r.URL.Query()
	region, itype := q.Get("region"), q.Get("instance_type")
	if region == "" || itype == "" {
		writeAMIOptions(w, nil)
		return
	}
	arch, freshArch, err := readCachedCatalogValue[string](r.Context(), d.Store, acc.ID, store.CatalogCacheArchitecture, region, itype)
	if err != nil {
		refreshAMIDataAsync(d, acc, region, itype)
		writeAMIOptions(w, nil)
		return
	}
	imgs, freshImages, err := readCachedCatalogValue[[]cloud.Image](r.Context(), d.Store, acc.ID, store.CatalogCacheImages, region, arch)
	if err != nil {
		refreshAMIDataAsync(d, acc, region, itype)
		writeAMIOptions(w, nil)
		return
	}
	if !freshArch || !freshImages {
		refreshAMIDataAsync(d, acc, region, itype)
	}
	writeAMIOptions(w, imgs)
}

func readCachedCatalogValue[T any](
	ctx context.Context,
	st *store.Store,
	accountID int64,
	kind, region, lookupKey string,
) (T, bool, error) {
	var zero T
	if st == nil {
		return zero, false, sql.ErrNoRows
	}
	if entry, err := st.GetCatalogCache(ctx, accountID, kind, region, lookupKey); err == nil {
		var cached T
		if err := json.Unmarshal([]byte(entry.Payload), &cached); err != nil {
			return zero, false, err
		}
		return cached, time.Since(entry.FetchedAt) < catalogCacheTTL, nil
	} else {
		return zero, false, err
	}
}

func fetchAndCacheCatalogValue[T any](
	ctx context.Context,
	st *store.Store,
	accountID int64,
	kind, region, lookupKey string,
	fetch func() (T, error),
) (T, error) {
	var zero T
	value, err := fetch()
	if err != nil {
		return zero, err
	}
	if st == nil {
		return value, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	if err := st.UpsertCatalogCache(ctx, store.CatalogCacheEntry{
		AccountID: accountID,
		Kind:      kind,
		Region:    region,
		LookupKey: lookupKey,
		Payload:   string(raw),
	}); err != nil {
		return zero, err
	}
	return value, nil
}

func RefreshCatalogCache(ctx context.Context, d Deps, acc store.CloudAccount) error {
	if d.Catalog == nil || d.Store == nil {
		return nil
	}
	regions, err := fetchAndCacheCatalogValue(ctx, d.Store, acc.ID, store.CatalogCacheRegions, "", "", func() ([]string, error) {
		return d.Catalog.Regions(ctx, acc.AccessKeyID, acc.SecretAccessKey)
	})
	if err != nil {
		return err
	}
	region := defaultRegion(acc)
	if !contains(regions, region) && len(regions) > 0 {
		region = regions[0]
	}
	if err := refreshRegionCache(ctx, d, acc, region); err != nil {
		return err
	}
	return refreshAMIData(ctx, d, acc, region, defaultInstanceType)
}

func refreshRegionCache(ctx context.Context, d Deps, acc store.CloudAccount, region string) error {
	_, err := fetchAndCacheCatalogValue(ctx, d.Store, acc.ID, store.CatalogCacheInstanceTypes, region, "", func() ([]cloud.InstanceType, error) {
		return d.Catalog.InstanceTypes(ctx, acc.AccessKeyID, acc.SecretAccessKey, region)
	})
	return err
}

func refreshAMIData(ctx context.Context, d Deps, acc store.CloudAccount, region, instanceType string) error {
	arch, err := fetchAndCacheCatalogValue(ctx, d.Store, acc.ID, store.CatalogCacheArchitecture, region, instanceType, func() (string, error) {
		return d.Catalog.Architecture(ctx, acc.AccessKeyID, acc.SecretAccessKey, region, instanceType)
	})
	if err != nil {
		return err
	}
	_, err = fetchAndCacheCatalogValue(ctx, d.Store, acc.ID, store.CatalogCacheImages, region, arch, func() ([]cloud.Image, error) {
		return d.Catalog.Images(ctx, acc.AccessKeyID, acc.SecretAccessKey, region, arch)
	})
	return err
}

func WarmCatalogCache(ctx context.Context, d Deps) {
	if d.Store == nil || d.Catalog == nil {
		return
	}
	accounts, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		log.Printf("catalog cache warm list accounts: %v", err)
		return
	}
	for _, account := range accounts {
		acc, err := d.Store.GetCloudAccount(ctx, account.ID)
		if err != nil {
			log.Printf("catalog cache warm get account %d: %v", account.ID, err)
			continue
		}
		if err := RefreshCatalogCache(ctx, d, acc); err != nil {
			log.Printf("catalog cache warm account %d: %v", acc.ID, err)
		}
	}
}

func refreshCatalogCacheAsync(d Deps, acc store.CloudAccount) {
	if d.DisableCatalogRefresh {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := RefreshCatalogCache(ctx, d, acc); err != nil {
			log.Printf("catalog cache refresh account %d: %v", acc.ID, err)
		}
	}()
}

func refreshRegionCacheAsync(d Deps, acc store.CloudAccount, region string) {
	if d.DisableCatalogRefresh {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := refreshRegionCache(ctx, d, acc, region); err != nil {
			log.Printf("instance type cache refresh account %d region %s: %v", acc.ID, region, err)
		}
	}()
}

func refreshAMIDataAsync(d Deps, acc store.CloudAccount, region, instanceType string) {
	if d.DisableCatalogRefresh {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := refreshAMIData(ctx, d, acc, region, instanceType); err != nil {
			log.Printf("ami cache refresh account %d region %s type %s: %v", acc.ID, region, instanceType, err)
		}
	}()
}

const defaultInstanceType = "t3.micro"

func defaultInstanceTypeDetails() cloud.InstanceType {
	return cloud.InstanceType{Name: defaultInstanceType, VCPUs: 2, MemoryMiB: 1024}
}

func defaultRegion(acc store.CloudAccount) string {
	if acc.DefaultRegion != "" {
		return acc.DefaultRegion
	}
	return "ap-southeast-1"
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// writeOptions renders <option> elements for a datalist. A non-empty note is
// rendered first so the user sees why the list is empty.
func writeOptions(w http.ResponseWriter, values []string, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if note != "" {
		b.WriteString(`<option value="">` + html.EscapeString(note) + `</option>`)
	}
	for _, v := range values {
		b.WriteString(`<option value="` + html.EscapeString(v) + `"></option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}

func writeInstanceTypeOptions(w http.ResponseWriter, values []cloud.InstanceType, selected, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if note != "" {
		b.WriteString(`<option value="">` + html.EscapeString(note) + `</option>`)
	}
	for _, v := range values {
		sel := ""
		if v.Name == selected {
			sel = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(v.Name) + `"` + sel + `>` + html.EscapeString(instanceTypeLabel(v)) + `</option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}

func instanceTypeLabel(v cloud.InstanceType) string {
	if v.VCPUs == 0 || v.MemoryMiB == 0 {
		return v.Name
	}
	return v.Name + " · " + strconv.FormatInt(int64(v.VCPUs), 10) + "C" + formatMemoryGiB(v.MemoryMiB)
}

func formatMemoryGiB(mib int64) string {
	if mib <= 0 {
		return ""
	}
	if mib%1024 == 0 {
		return strconv.FormatInt(mib/1024, 10) + "G"
	}
	gb := float64(mib) / 1024
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(gb, 'f', 1, 64), "0"), ".") + "G"
}

func writeRegionOptions(w http.ResponseWriter, regions []string, selected, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if note != "" {
		b.WriteString(`<option value="">` + html.EscapeString(note) + `</option>`)
	}
	for _, r := range regions {
		sel := ""
		if r == selected {
			sel = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(r) + `"` + sel + `>` + html.EscapeString(regionDisplayName(r)) + `</option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}

func regionDisplayName(region string) string {
	name, ok := regionNamesZH[region]
	if !ok {
		name = "未知区域"
	}
	return name + " · " + region
}

var regionNamesZH = map[string]string{
	"af-south-1":     "非洲（开普敦）",
	"ap-east-1":      "亚太地区（香港）",
	"ap-east-2":      "亚太地区（台北）",
	"ap-northeast-1": "亚太地区（东京）",
	"ap-northeast-2": "亚太地区（首尔）",
	"ap-northeast-3": "亚太地区（大阪）",
	"ap-south-1":     "亚太地区（孟买）",
	"ap-south-2":     "亚太地区（海得拉巴）",
	"ap-southeast-1": "亚太地区（新加坡）",
	"ap-southeast-2": "亚太地区（悉尼）",
	"ap-southeast-3": "亚太地区（雅加达）",
	"ap-southeast-4": "亚太地区（墨尔本）",
	"ap-southeast-5": "亚太地区（马来西亚）",
	"ap-southeast-6": "亚太地区（奥克兰）",
	"ap-southeast-7": "亚太地区（泰国）",
	"ca-central-1":   "加拿大（中部）",
	"ca-west-1":      "加拿大西部（卡尔加里）",
	"cn-north-1":     "中国（北京）",
	"cn-northwest-1": "中国（宁夏）",
	"eu-central-1":   "欧洲（法兰克福）",
	"eu-central-2":   "欧洲（苏黎世）",
	"eu-north-1":     "欧洲（斯德哥尔摩）",
	"eu-south-1":     "欧洲（米兰）",
	"eu-south-2":     "欧洲（西班牙）",
	"eu-west-1":      "欧洲（爱尔兰）",
	"eu-west-2":      "欧洲（伦敦）",
	"eu-west-3":      "欧洲（巴黎）",
	"eusc-de-east-1": "AWS 欧洲主权云（勃兰登堡）",
	"il-central-1":   "以色列（特拉维夫）",
	"me-central-1":   "中东（阿联酋）",
	"me-south-1":     "中东（巴林）",
	"mx-central-1":   "墨西哥（中部）",
	"sa-east-1":      "南美洲（圣保罗）",
	"us-east-1":      "美国东部（弗吉尼亚北部）",
	"us-east-2":      "美国东部（俄亥俄）",
	"us-gov-east-1":  "AWS GovCloud（美国东部）",
	"us-gov-west-1":  "AWS GovCloud（美国西部）",
	"us-west-1":      "美国西部（加利福尼亚北部）",
	"us-west-2":      "美国西部（俄勒冈）",
}

// writeAMIOptions renders <option> elements for the AMI <select>. The first
// option is always the auto/fallback (empty value → the program auto-resolves
// Ubuntu 26.04). The catalog's default image is pre-selected.
func writeAMIOptions(w http.ResponseWriter, imgs []cloud.Image) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<option value="">自动:最新 Ubuntu 26.04 LTS</option>`)
	for _, im := range imgs {
		sel := ""
		if im.Default {
			sel = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(im.ID) + `"` + sel + `>` + html.EscapeString(im.Name) + `</option>`)
	}
	_, _ = w.Write([]byte(b.String()))
}
