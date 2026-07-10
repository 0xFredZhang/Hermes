package api

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0xFredZhang/Hermes/internal/store"
)

const (
	maxProjectNameLength        = 128
	maxProjectDescriptionLength = 1000
)

type projectFormData struct {
	PageTitle   string
	ActiveNav   string
	HideNav     bool
	Name        string
	Description string
	Error       string
	FieldErrors map[string]string
}

func addProjectRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListProjects(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "projects", map[string]any{
			"PageTitle": "项目",
			"ActiveNav": "projects",
			"Projects":  list,
		})
	})
	mux.HandleFunc("GET /projects/new", func(w http.ResponseWriter, _ *http.Request) {
		renderProjectForm(w, d, http.StatusOK, projectFormData{
			PageTitle: "新建项目",
			ActiveNav: "projects",
		})
	})
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		form := projectFormData{
			PageTitle:   "新建项目",
			ActiveNav:   "projects",
			Name:        strings.TrimSpace(r.FormValue("name")),
			Description: strings.TrimSpace(r.FormValue("description")),
			FieldErrors: make(map[string]string),
		}
		validateProjectForm(&form)
		if len(form.FieldErrors) > 0 {
			form.Error = "请检查标出的字段。"
			renderProjectForm(w, d, http.StatusUnprocessableEntity, form)
			return
		}
		_, err := d.Store.CreateProject(r.Context(), store.Project{
			Name: form.Name, Description: form.Description,
		})
		if err != nil {
			http.Error(w, "无法创建项目", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
	})
	mux.HandleFunc("DELETE /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := d.Store.DeleteProject(r.Context(), id); err != nil {
			// FK RESTRICT (project still has blueprints) → inline error row
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<tr><td colspan="3" class="err">无法删除:该项目下还有蓝图</td></tr>`))
			return
		}
		writeProjectRows(w, r, d)
	})
}

func validateProjectForm(form *projectFormData) {
	if form.Name == "" {
		form.FieldErrors["name"] = "请输入项目名。"
	} else if utf8.RuneCountInString(form.Name) > maxProjectNameLength {
		form.FieldErrors["name"] = "项目名不能超过 128 个字符。"
	}
	if utf8.RuneCountInString(form.Description) > maxProjectDescriptionLength {
		form.FieldErrors["description"] = "描述不能超过 1000 个字符。"
	}
}

func renderProjectForm(w http.ResponseWriter, d Deps, status int, data projectFormData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	d.Renderer.Render(w, "project_form", data)
}

func writeProjectRows(w http.ResponseWriter, r *http.Request, d Deps) {
	list, err := d.Store.ListProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.Renderer.RenderPartial(w, "project_rows", list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
