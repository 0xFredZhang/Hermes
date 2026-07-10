package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0xFredZhang/Hermes/internal/store"
)

const (
	maxAccountNameLength = 128
	maxRegionLength      = 64
	maxAccessKeyIDLength = 128
	maxSecretKeyLength   = 256
)

// validationRegion only configures the STS client. GetCallerIdentity is a
// global call; DefaultRegion remains the operator's deployment preference.
const validationRegion = "us-east-1"

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)

type accountFormData struct {
	PageTitle     string
	ActiveNav     string
	HideNav       bool
	Name          string
	DefaultRegion string
	AccessKeyID   string
	Error         string
	FieldErrors   map[string]string
}

func addAccountRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /accounts", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListCloudAccounts(r.Context())
		if err != nil {
			http.Error(w, "无法读取账号", http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "accounts", map[string]any{
			"PageTitle": "AWS 云账号",
			"ActiveNav": "accounts",
			"Accounts":  list,
		})
	})
	mux.HandleFunc("GET /accounts/new", func(w http.ResponseWriter, _ *http.Request) {
		renderAccountForm(w, d, http.StatusOK, accountFormData{
			PageTitle:     "添加 AWS 云账号",
			ActiveNav:     "accounts",
			DefaultRegion: "ap-southeast-1",
		})
	})
	mux.HandleFunc("POST /accounts", func(w http.ResponseWriter, r *http.Request) {
		handleCreateAccount(w, r, d)
	})
	mux.HandleFunc("DELETE /accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "账号 ID 无效", http.StatusBadRequest)
			return
		}
		if err := d.Store.DeleteCloudAccount(r.Context(), id); err != nil {
			http.Error(w, "无法删除账号", http.StatusInternalServerError)
			return
		}
		writeRows(w, r.Context(), d)
	})
}

func handleCreateAccount(w http.ResponseWriter, r *http.Request, d Deps) {
	form := accountFormData{
		PageTitle:     "添加 AWS 云账号",
		ActiveNav:     "accounts",
		Name:          strings.TrimSpace(r.FormValue("name")),
		DefaultRegion: strings.TrimSpace(r.FormValue("default_region")),
		AccessKeyID:   strings.TrimSpace(r.FormValue("access_key_id")),
		FieldErrors:   make(map[string]string),
	}
	secret := r.FormValue("secret_access_key")
	validateAccountForm(&form, secret)
	if len(form.FieldErrors) > 0 {
		form.Error = "请检查标出的字段。"
		renderAccountForm(w, d, http.StatusUnprocessableEntity, form)
		return
	}

	identity, err := d.Validator.Validate(r.Context(), form.AccessKeyID, secret, validationRegion)
	if err != nil {
		form.Error = "凭证验证失败，请检查 Access Key ID 和 Secret Access Key。"
		renderAccountForm(w, d, http.StatusUnprocessableEntity, form)
		return
	}
	acc := store.CloudAccount{
		Name:            form.Name,
		DefaultRegion:   form.DefaultRegion,
		AccessKeyID:     form.AccessKeyID,
		SecretAccessKey: secret,
		AWSAccountID:    identity.AccountID,
		ARN:             identity.ARN,
	}
	accountID, err := d.Store.CreateCloudAccount(r.Context(), acc)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateAccount) {
			form.Error = "该 AWS 账号已添加。"
			renderAccountForm(w, d, http.StatusConflict, form)
			return
		}
		http.Error(w, "无法保存账号", http.StatusInternalServerError)
		return
	}
	acc.ID = accountID
	refreshCatalogCacheAsync(d, acc)
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

func validateAccountForm(form *accountFormData, secret string) {
	if form.Name == "" {
		form.FieldErrors["name"] = "请输入账号别名。"
	} else if utf8.RuneCountInString(form.Name) > maxAccountNameLength {
		form.FieldErrors["name"] = "账号别名不能超过 128 个字符。"
	}
	if form.DefaultRegion == "" {
		form.FieldErrors["default_region"] = "请输入默认区域。"
	} else if len(form.DefaultRegion) > maxRegionLength || !awsRegionPattern.MatchString(form.DefaultRegion) {
		form.FieldErrors["default_region"] = "请输入有效的 AWS 区域，例如 ap-southeast-1。"
	}
	if form.AccessKeyID == "" {
		form.FieldErrors["access_key_id"] = "请输入 Access Key ID。"
	} else if len(form.AccessKeyID) > maxAccessKeyIDLength {
		form.FieldErrors["access_key_id"] = "Access Key ID 过长。"
	}
	if secret == "" {
		form.FieldErrors["secret_access_key"] = "请输入 Secret Access Key。"
	} else if len(secret) > maxSecretKeyLength {
		form.FieldErrors["secret_access_key"] = "Secret Access Key 过长。"
	}
}

func renderAccountForm(w http.ResponseWriter, d Deps, status int, data accountFormData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	d.Renderer.Render(w, "account_form", data)
}

func writeRows(w http.ResponseWriter, ctx context.Context, d Deps) {
	list, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		http.Error(w, "无法读取账号", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Renderer.RenderRows(w, list); err != nil {
		http.Error(w, "无法渲染账号", http.StatusInternalServerError)
	}
}
