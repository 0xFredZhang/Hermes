package web

import (
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderPartial writes a single named fragment (for htmx swaps).
func (r *Renderer) RenderPartial(w io.Writer, name string, data any) error {
	return r.partials.ExecuteTemplate(w, name, data)
}

// RenderRows writes the cloud-account rows fragment (kept for the M1 accounts flow).
func (r *Renderer) RenderRows(w io.Writer, accounts any) error {
	return r.partials.ExecuteTemplate(w, "rows", accounts)
}
