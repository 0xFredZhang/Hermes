package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func addJobRoutes(mux *http.ServeMux, d Deps) {
	mux.HandleFunc("GET /jobs/{id}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		rc := http.NewResponseController(w)
		// SSE is long-lived: clear the server's write deadline for this connection.
		// (ResponseRecorder in tests returns ErrNotSupported here; safe to ignore.)
		_ = rc.SetWriteDeadline(time.Time{})

		history, ch, done, cancel := d.Broker.Subscribe(id)
		defer cancel()

		for _, line := range history {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		_ = rc.Flush()
		if done {
			fmt.Fprint(w, "event: done\ndata: end\n\n")
			_ = rc.Flush()
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case line, ok := <-ch:
				if !ok {
					fmt.Fprint(w, "event: done\ndata: end\n\n")
					_ = rc.Flush()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				_ = rc.Flush()
			}
		}
	})
}
