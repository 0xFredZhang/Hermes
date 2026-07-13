package api

import (
	"net/http"
	"strconv"
)

func parseDeleteID(w http.ResponseWriter, r *http.Request, event, message string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		if event == "" {
			http.Error(w, message, http.StatusBadRequest)
		} else {
			writeResourceDeleteError(w, r, event, message, http.StatusBadRequest)
		}
		return 0, false
	}
	return id, true
}

func writeResourceDeleteError(w http.ResponseWriter, r *http.Request, event, message string, status int) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", `{`+strconv.QuoteToASCII(event)+`:{"message":`+strconv.QuoteToASCII(message)+`}}`)
	}
	http.Error(w, message, status)
}

func writeResourceDeleteSuccess(w http.ResponseWriter, r *http.Request, event, message string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger-After-Swap", `{`+strconv.QuoteToASCII(event)+`:{"message":`+strconv.QuoteToASCII(message)+`}}`)
	}
}
