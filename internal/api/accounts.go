package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
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

type accountDeleteData struct {
	PageTitle string
	ActiveNav string
	HideNav   bool
	Account   accountDeleteView
	Error     string
}

// accountDeleteView deliberately contains no credential fields, so neither a
// successful confirmation nor an error render can expose stored secrets.
type accountDeleteView struct {
	ID            int64
	Name          string
	DefaultRegion string
	AWSAccountID  string
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
	mux.HandleFunc("GET /accounts/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleAccountDeleteConfirmation(w, r, d)
	})
	mux.HandleFunc("DELETE /accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteAccount(w, r, d, false)
	})
	mux.HandleFunc("POST /accounts/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteAccount(w, r, d, true)
	})
}

func handleAccountDeleteConfirmation(w http.ResponseWriter, r *http.Request, d Deps) {
	id, ok := parseDeleteID(w, r, "", "账号 ID 无效")
	if !ok {
		return
	}
	account, err := d.Store.GetCloudAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load account %d delete confirmation: %v", id, err)
		http.Error(w, "无法读取账号", http.StatusInternalServerError)
		return
	}
	renderAccountDelete(w, d, http.StatusOK, accountDeleteViewFrom(account), "")
}

func handleDeleteAccount(w http.ResponseWriter, r *http.Request, d Deps, redirect bool) {
	event := "account-delete-error"
	if redirect {
		event = ""
	}
	id, ok := parseDeleteID(w, r, event, "账号 ID 无效")
	if !ok {
		return
	}

	var view accountDeleteView
	if redirect {
		account, err := d.Store.GetCloudAccount(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("load account %d before delete: %v", id, err)
			http.Error(w, "无法读取账号", http.StatusInternalServerError)
			return
		}
		view = accountDeleteViewFrom(account)
	}

	if err := d.Store.DeleteCloudAccount(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeResourceDeleteError(w, r, "account-delete-error", "账号不存在或已被删除", http.StatusNotFound)
		case errors.Is(err, store.ErrCloudAccountReferenced):
			const message = "该账号仍被蓝图或环境引用，无法删除"
			if redirect {
				renderAccountDelete(w, d, http.StatusConflict, view, message)
			} else {
				writeResourceDeleteError(w, r, "account-delete-error", message, http.StatusConflict)
			}
		default:
			log.Printf("delete account %d: %v", id, err)
			const message = "无法删除账号，请稍后重试"
			if redirect {
				renderAccountDelete(w, d, http.StatusInternalServerError, view, message)
			} else {
				writeResourceDeleteError(w, r, "account-delete-error", message, http.StatusInternalServerError)
			}
		}
		return
	}
	if redirect {
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
		return
	}

	body, err := renderAccountRows(r.Context(), d)
	if err != nil {
		log.Printf("render accounts after deleting %d: %v", id, err)
		writeResourceDeleteError(w, r, "account-delete-error", "账号已删除，但列表刷新失败，请重新加载页面", http.StatusInternalServerError)
		return
	}
	writeResourceDeleteSuccess(w, r, "account-delete-success", "账号已删除")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func accountDeleteViewFrom(account store.CloudAccount) accountDeleteView {
	return accountDeleteView{
		ID: account.ID, Name: account.Name, DefaultRegion: account.DefaultRegion, AWSAccountID: account.AWSAccountID,
	}
}

func renderAccountDelete(w http.ResponseWriter, d Deps, status int, account accountDeleteView, message string) {
	d.Renderer.RenderStatus(w, "account_delete", status, accountDeleteData{
		PageTitle: "删除账号 · " + account.Name,
		ActiveNav: "accounts",
		Account:   account,
		Error:     message,
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
	d.Renderer.RenderStatus(w, "account_form", status, data)
}

func renderAccountRows(ctx context.Context, d Deps) ([]byte, error) {
	list, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := d.Renderer.RenderRows(&body, list); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}
