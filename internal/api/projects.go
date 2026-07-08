package api

import (
	"net/http"
	"strconv"

	"github.com/0xFredZhang/Hermes/internal/store"
)

func addProjectRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Store.ListProjects(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Renderer.Render(w, "projects", map[string]any{"Projects": list})
	})
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		name := r.FormValue("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		_, err := d.Store.CreateProject(r.Context(),
			store.Project{Name: name, Description: r.FormValue("description")})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProjectRows(w, r, d)
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
