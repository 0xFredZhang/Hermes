package web

import (
	"bytes"
	"embed"
	"html/template"
	"io"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var StaticFS embed.FS

type Renderer struct {
	pages    map[string]*template.Template
	partials *template.Template
}

// NewRenderer parses each page together with the layout and all fragment files,
// plus a standalone partial set for htmx swaps.
func NewRenderer() (*Renderer, error) {
	shared := []string{
		"templates/layout.html",
		"templates/_account_rows.html",
		"templates/_fragments.html",
	}
	pageFiles := map[string]string{
		"login":              "templates/login.html",
		"accounts":           "templates/accounts.html",
		"account_form":       "templates/account_form.html",
		"projects":           "templates/projects.html",
		"project_form":       "templates/project_form.html",
		"blueprints":         "templates/blueprints.html",
		"blueprint_form":     "templates/blueprint_form.html",
		"blueprint_detail":   "templates/blueprint_detail.html",
		"blueprint_deploy":   "templates/blueprint_deploy.html",
		"blueprint_delete":   "templates/blueprint_delete.html",
		"environments":       "templates/environments.html",
		"environment_detail": "templates/environment_detail.html",
	}
	r := &Renderer{pages: map[string]*template.Template{}}
	for name, file := range pageFiles {
		files := append([]string{file}, shared...)
		t, err := template.ParseFS(templatesFS, files...)
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	partials, err := template.ParseFS(templatesFS,
		"templates/_account_rows.html", "templates/_fragments.html")
	if err != nil {
		return nil, err
	}
	r.partials = partials
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	r.RenderStatus(w, name, http.StatusOK, data)
}

func (r *Renderer) RenderStatus(w http.ResponseWriter, name string, status int, data any) {
	var body bytes.Buffer
	if err := r.pages[name].ExecuteTemplate(&body, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}

// RenderPartial writes a single named fragment (for htmx swaps).
func (r *Renderer) RenderPartial(w io.Writer, name string, data any) error {
	return r.partials.ExecuteTemplate(w, name, data)
}

// RenderRows writes the cloud-account rows fragment (kept for the M1 accounts flow).
func (r *Renderer) RenderRows(w io.Writer, accounts any) error {
	return r.partials.ExecuteTemplate(w, "rows", accounts)
}
