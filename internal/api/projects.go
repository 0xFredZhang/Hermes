package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
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

type projectDeleteData struct {
	PageTitle string
	ActiveNav string
	HideNav   bool
	Project   store.Project
	Error     string
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
	mux.HandleFunc("GET /projects/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleProjectDeleteConfirmation(w, r, d)
	})
	mux.HandleFunc("DELETE /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteProject(w, r, d, false)
	})
	mux.HandleFunc("POST /projects/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		handleDeleteProject(w, r, d, true)
	})
}

func handleProjectDeleteConfirmation(w http.ResponseWriter, r *http.Request, d Deps) {
	id, ok := parseDeleteID(w, r, "", "项目 ID 无效")
	if !ok {
		return
	}
	project, err := d.Store.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load project %d delete confirmation: %v", id, err)
		http.Error(w, "无法读取项目", http.StatusInternalServerError)
		return
	}
	renderProjectDelete(w, d, http.StatusOK, project, "")
}

func handleDeleteProject(w http.ResponseWriter, r *http.Request, d Deps, redirect bool) {
	event := "project-delete-error"
	if redirect {
		event = ""
	}
	id, ok := parseDeleteID(w, r, event, "项目 ID 无效")
	if !ok {
		return
	}

	var project store.Project
	if redirect {
		var err error
		project, err = d.Store.GetProject(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("load project %d before delete: %v", id, err)
			http.Error(w, "无法读取项目", http.StatusInternalServerError)
			return
		}
	}

	if err := d.Store.DeleteProject(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeResourceDeleteError(w, r, "project-delete-error", "项目不存在或已被删除", http.StatusNotFound)
		case errors.Is(err, store.ErrProjectReferenced):
			const message = "该项目仍有蓝图引用，无法删除"
			if redirect {
				renderProjectDelete(w, d, http.StatusConflict, project, message)
			} else {
				writeResourceDeleteError(w, r, "project-delete-error", message, http.StatusConflict)
			}
		default:
			log.Printf("delete project %d: %v", id, err)
			const message = "无法删除项目，请稍后重试"
			if redirect {
				renderProjectDelete(w, d, http.StatusInternalServerError, project, message)
			} else {
				writeResourceDeleteError(w, r, "project-delete-error", message, http.StatusInternalServerError)
			}
		}
		return
	}
	if redirect {
		http.Redirect(w, r, "/projects", http.StatusSeeOther)
		return
	}

	body, err := renderProjectRows(r.Context(), d)
	if err != nil {
		log.Printf("render projects after deleting %d: %v", id, err)
		writeResourceDeleteError(w, r, "project-delete-error", "项目已删除，但列表刷新失败，请重新加载页面", http.StatusInternalServerError)
		return
	}
	writeResourceDeleteSuccess(w, r, "project-delete-success", "项目已删除")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func renderProjectDelete(w http.ResponseWriter, d Deps, status int, project store.Project, message string) {
	d.Renderer.RenderStatus(w, "project_delete", status, projectDeleteData{
		PageTitle: "删除项目 · " + project.Name,
		ActiveNav: "projects",
		Project:   project,
		Error:     message,
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
	d.Renderer.RenderStatus(w, "project_form", status, data)
}

func renderProjectRows(ctx context.Context, d Deps) ([]byte, error) {
	list, err := d.Store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := d.Renderer.RenderPartial(&body, "project_rows", list); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}
