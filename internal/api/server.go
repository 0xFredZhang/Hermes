package api

import (
	"net/http"

	"github.com/0xFredZhang/Hermes/internal/auth"
	"github.com/0xFredZhang/Hermes/internal/cloud"
	"github.com/0xFredZhang/Hermes/internal/orchestrator"
	"github.com/0xFredZhang/Hermes/internal/store"
	"github.com/0xFredZhang/Hermes/internal/web"
)

type Deps struct {
	Store        *store.Store
	Validator    *cloud.Validator
	Auth         *auth.Authenticator
	Renderer     *web.Renderer
	Orchestrator *orchestrator.Orchestrator
	Broker       *orchestrator.Broker
	Catalog      CatalogAPI

	// DisableCatalogRefresh is used by handler tests to avoid background
	// goroutines making fake catalog calls after the response returns.
	DisableCatalogRefresh bool
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(web.StaticFS))

	mux.HandleFunc("GET /login", func(w http.ResponseWriter, _ *http.Request) {
		d.Renderer.Render(w, "login", map[string]any{
			"PageTitle": "登录",
			"HideNav":   true,
		})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if d.Auth.CheckPassword(r.FormValue("password")) {
			http.SetCookie(w, d.Auth.IssueCookie())
			http.Redirect(w, r, "/accounts", http.StatusSeeOther)
			return
		}
		d.Renderer.RenderStatus(w, "login", http.StatusUnauthorized, map[string]any{
			"PageTitle": "登录",
			"HideNav":   true,
			"Error":     "口令错误",
		})
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "hermes_session", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/accounts", http.StatusSeeOther)
	})

	addAccountRoutes(mux, d)
	addProjectRoutes(mux, d)
	addBlueprintRoutes(mux, d)
	addMetadataRoutes(mux, d)
	addEnvironmentRoutes(mux, d)
	addJobRoutes(mux, d)

	return d.Auth.Middleware(mux)
}
