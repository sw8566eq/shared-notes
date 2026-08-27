// Package httpserver wires the household notepad's HTTP + WebSocket routes:
// note CRUD and live broadcast-on-save. There's no per-person identity —
// the home WiFi password (and not port-forwarding this) is the entire
// access-control story; see README.md.
package httpserver

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"linkshr/internal/hub"
	"linkshr/internal/store"
)

type Server struct {
	store  *store.Store
	hub    *hub.Hub
	tmpl   *template.Template
	static http.Handler
}

func New(st *store.Store, h *hub.Hub, assets fs.FS) (*Server, error) {
	tmplFS, err := fs.Sub(assets, "templates")
	if err != nil {
		return nil, err
	}
	tmpl, err := template.ParseFS(tmplFS, "*.html")
	if err != nil {
		return nil, err
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}

	s := &Server{store: st, hub: h, tmpl: tmpl}
	s.static = http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	return s, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/notes", s.handleNotesList)
	mux.HandleFunc("POST /api/notes", s.handleNotesCreate)
	mux.HandleFunc("PUT /api/notes/reorder", s.handleNotesReorder)
	mux.HandleFunc("GET /api/notes/{id}", s.handleNoteGet)
	mux.HandleFunc("PUT /api/notes/{id}", s.handleNoteUpdate)
	mux.HandleFunc("DELETE /api/notes/{id}", s.handleNoteDelete)

	return s.logged(mux)
}

// statusRecorder wraps a ResponseWriter to capture the status code a
// handler sends, so logged can report it after the handler returns.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets net/http's ResponseController see through this wrapper to
// the underlying ResponseWriter's Hijacker support. Without it,
// wrapping the ResponseWriter here would silently break /ws: it's how
// github.com/coder/websocket's Accept finds the connection to hijack for
// the WebSocket upgrade.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logged logs one line per request: remote address, method, path, status
// code, and how long the handler took. For /ws, "how long" means the
// entire lifetime of that WebSocket connection, since the line can only
// be written once the handler returns — so a long-lived connection logs
// at disconnect, not at connect.
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %s %d %s", r.RemoteAddr, r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		log.Printf("render index: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalError logs the real error (which may contain internal details —
// file paths, driver internals) server-side, and sends the client only a
// generic message, so a failure doesn't leak implementation details.
func (s *Server) internalError(w http.ResponseWriter, context string, err error) {
	log.Printf("%s: %v", context, err)
	writeJSONError(w, http.StatusInternalServerError, "internal error")
}
