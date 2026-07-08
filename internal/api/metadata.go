package api

import (
	"context"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
)

// CatalogAPI is the subset of cloud.Catalog the metadata handlers need.
// *cloud.Catalog satisfies it; tests inject a fake.
type CatalogAPI interface {
	Regions(ctx context.Context, accessKey, secret string) ([]string, error)
	InstanceTypes(ctx context.Context, accessKey, secret, region string) ([]string, error)
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
	regions, err := d.Catalog.Regions(r.Context(), acc.AccessKeyID, acc.SecretAccessKey)
	if err != nil {
		writeOptions(w, nil, "无法获取 Region:"+err.Error())
		return
	}
	writeOptions(w, regions, "")
}

func handleInstanceTypes(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return
	}
	region := r.URL.Query().Get("region")
	itypes, err := d.Catalog.InstanceTypes(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region)
	if err != nil {
		writeOptions(w, nil, "无法获取实例规格:"+err.Error())
		return
	}
	writeOptions(w, itypes, "")
}

func handleAMIs(w http.ResponseWriter, r *http.Request, d Deps) {
	acc, ok := resolveAccount(w, r, d)
	if !ok {
		return // resolveAccount already wrote the inline hint
	}
	q := r.URL.Query()
	region, itype := q.Get("region"), q.Get("instance_type")
	arch, err := d.Catalog.Architecture(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region, itype)
	if err != nil {
		writeAMIOptions(w, nil)
		return
	}
	imgs, err := d.Catalog.Images(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, region, arch)
	if err != nil {
		writeAMIOptions(w, nil)
		return
	}
	writeAMIOptions(w, imgs)
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
