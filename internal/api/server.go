package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

type Deps struct {
	Store     *store.Store
	Validator *cloud.Validator
	Auth      *auth.Authenticator
	Renderer  *web.Renderer
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(web.StaticFS))

	mux.HandleFunc("GET /login", func(w http.ResponseWriter, _ *http.Request) {
		d.Renderer.Render(w, "login", map[string]any{})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if d.Auth.CheckPassword(r.FormValue("password")) {
			http.SetCookie(w, d.Auth.IssueCookie())
			http.Redirect(w, r, "/accounts", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		d.Renderer.Render(w, "login", map[string]any{"Error": "口令错误"})
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "hermes_session", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /accounts", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListCloudAccounts(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "accounts", map[string]any{"Accounts": list})
	})
	mux.HandleFunc("POST /accounts", func(w http.ResponseWriter, r *http.Request) {
		handleCreateAccount(w, r, d)
	})
	mux.HandleFunc("DELETE /accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := d.Store.DeleteCloudAccount(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeRows(w, r.Context(), d)
	})

	return d.Auth.Middleware(mux)
}

func handleCreateAccount(w http.ResponseWriter, r *http.Request, d Deps) {
	acc := store.CloudAccount{
		Name:            r.FormValue("name"),
		DefaultRegion:   r.FormValue("default_region"),
		AccessKeyID:     r.FormValue("access_key_id"),
		SecretAccessKey: r.FormValue("secret_access_key"),
	}
	id, err := d.Validator.Validate(r.Context(), acc.AccessKeyID, acc.SecretAccessKey, acc.DefaultRegion)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<tr><td colspan="5" class="err">凭证验证失败:` + err.Error() + `</td></tr>`))
		return
	}
	acc.AWSAccountID = id.AccountID
	acc.ARN = id.ARN
	if _, err := d.Store.CreateCloudAccount(r.Context(), acc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeRows(w, r.Context(), d)
}

func writeRows(w http.ResponseWriter, ctx context.Context, d Deps) {
	list, err := d.Store.ListCloudAccounts(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Renderer.RenderRows(w, list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
