package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"linkshr/internal/store"
)

type noteInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// maxNoteBytes bounds a single create/update request body. Generous for
// plain-text notes, but keeps a stray huge paste (or a malicious request)
// from growing the database or memory use without limit.
const maxNoteBytes = 256 * 1024

// decodeNoteInput reads and validates a note create/update body.
//
// Requiring an exact application/json Content-Type isn't just pedantry:
// with no login on this app, it's what stops a page on some other site
// from silently writing notes into this one. A cross-origin fetch() with
// Content-Type text/plain (or a plain HTML form) is a CORS "simple
// request" that browsers send without asking permission first, and Go's
// JSON decoder doesn't care what Content-Type it was sent with — so
// without this check, any body that merely looks like our note JSON
// would be accepted no matter its origin. application/json is not on the
// CORS-safelisted list, so requiring it forces a preflight, and since
// this server never sends Access-Control-Allow-Origin, that preflight
// (and so the real request) never succeeds cross-origin.
func decodeNoteInput(w http.ResponseWriter, r *http.Request) (noteInput, bool) {
	var in noteInput
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return in, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNoteBytes)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "note is too large")
			return in, false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return in, false
	}
	return in, true
}

// event is the shape broadcast over /ws so connected browsers can update
// their note list live without a full refresh.
type event struct {
	Type string      `json:"type"` // "note_upsert" | "note_delete" | "notes_reorder"
	Note *store.Note `json:"note,omitempty"`
	ID   int64       `json:"id,omitempty"`
	IDs  []int64     `json:"ids,omitempty"`
}

func (s *Server) broadcastUpsert(n store.Note) {
	msg, err := json.Marshal(event{Type: "note_upsert", Note: &n})
	if err == nil {
		s.hub.Broadcast(msg)
	}
}

func (s *Server) broadcastDelete(id int64) {
	msg, err := json.Marshal(event{Type: "note_delete", ID: id})
	if err == nil {
		s.hub.Broadcast(msg)
	}
}

func (s *Server) broadcastReorder(ids []int64) {
	msg, err := json.Marshal(event{Type: "notes_reorder", IDs: ids})
	if err == nil {
		s.hub.Broadcast(msg)
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
	notes, err := s.store.ListNotes(r.Context())
	if err != nil {
		s.internalError(w, "list notes", err)
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) handleNoteGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid note id")
		return
	}
	n, err := s.store.GetNote(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleNotesCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeNoteInput(w, r)
	if !ok {
		return
	}
	n, err := s.store.CreateNote(r.Context(), in.Title, in.Body)
	if err != nil {
		s.internalError(w, "create note", err)
		return
	}
	s.broadcastUpsert(n)
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleNoteUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid note id")
		return
	}
	in, ok := decodeNoteInput(w, r)
	if !ok {
		return
	}
	n, err := s.store.UpdateNote(r.Context(), id, in.Title, in.Body)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	s.broadcastUpsert(n)
	writeJSON(w, http.StatusOK, n)
}

type reorderInput struct {
	IDs []int64 `json:"ids"`
}

// maxReorderBytes bounds the reorder request body. Even a large note
// collection's id list is small.
const maxReorderBytes = 64 * 1024

// decodeReorderInput applies the same application/json + size-cap rules as
// decodeNoteInput, and for the same reason: it's what stops a cross-origin
// page from silently reordering (or, via a too-large body, wasting effort
// on) someone's note list.
func decodeReorderInput(w http.ResponseWriter, r *http.Request) (reorderInput, bool) {
	var in reorderInput
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return in, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReorderBytes)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "too many notes to reorder")
			return in, false
		}
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return in, false
	}
	return in, true
}

// handleNotesReorder persists a new manual note order (drag-to-reorder in
// the UI) and pushes it to every other connected client.
func (s *Server) handleNotesReorder(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeReorderInput(w, r)
	if !ok {
		return
	}
	if err := s.store.ReorderNotes(r.Context(), in.IDs); err != nil {
		s.internalError(w, "reorder notes", err)
		return
	}
	s.broadcastReorder(in.IDs)
	w.WriteHeader(http.StatusNoContent)
}

// handleNoteDelete permanently deletes a note — there is no undo, see
// README.
func (s *Server) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid note id")
		return
	}
	found, err := s.store.DeleteNote(r.Context(), id)
	if err != nil {
		s.internalError(w, "delete note", err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	s.broadcastDelete(id)
	w.WriteHeader(http.StatusNoContent)
}
