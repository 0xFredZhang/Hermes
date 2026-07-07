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
	pages map[string]*template.Template
}

// NewRenderer parses each page template together with the shared layout and rows partial.
func NewRenderer() (*Renderer, error) {
	shared := []string{"templates/layout.html", "templates/_account_rows.html"}
	pages := map[string]string{
		"login":    "templates/login.html",
		"accounts": "templates/accounts.html",
	}
	r := &Renderer{pages: map[string]*template.Template{}}
	for name, file := range pages {
		files := append([]string{file}, shared...)
		t, err := template.ParseFS(templatesFS, files...)
		if err != nil {
			return nil, err
		}
		r.pages[name] = t
	}
	// standalone partial for htmx swaps
	partial, err := template.ParseFS(templatesFS, "templates/_account_rows.html")
	if err != nil {
		return nil, err
	}
	r.pages["rows"] = partial
	return r, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RenderRows writes just the <tbody> content for htmx partial swaps.
func (r *Renderer) RenderRows(w io.Writer, accounts any) error {
	return r.pages["rows"].ExecuteTemplate(w, "rows", accounts)
}
