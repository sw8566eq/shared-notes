package httpserver

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestStatusRecorder_ExplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.WriteHeader(http.StatusNotFound)
	if sr.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", sr.status, http.StatusNotFound)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("underlying recorder code = %d, want %d (WriteHeader must still reach it)", rec.Code, http.StatusNotFound)
	}
}

func TestStatusRecorder_ImplicitOK(t *testing.T) {
	rec := httptest.NewRecorder()
	// Deliberately not pre-seeded to http.StatusOK here (unlike the other
	// tests in this file): the whole point of this test is that Write
	// itself derives the implicit 200, the same way net/http would for a
	// handler (like handleIndex's html/template execution) that never
	// calls WriteHeader explicitly. Pre-seeding OK would let this pass
	// even if Write forgot to set status at all — see the WriteHeader
	// bypass this regression-tests, once true of this type.
	sr := &statusRecorder{ResponseWriter: rec}
	if _, err := sr.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sr.status != http.StatusOK {
		t.Fatalf("status = %d, want implicit %d", sr.status, http.StatusOK)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("underlying recorder code = %d, want %d (Write must still reach the real ResponseWriter)", rec.Code, http.StatusOK)
	}
}

// TestStatusRecorder_WriteAfterWriteHeaderKeepsExplicitStatus guards the
// other direction of the same bug: once a handler has explicitly set a
// non-200 status, a later Write must not silently reset it back to the
// implicit-200 default.
func TestStatusRecorder_WriteAfterWriteHeaderKeepsExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec}
	sr.WriteHeader(http.StatusNotFound)
	if _, err := sr.Write([]byte("not found")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sr.status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d to survive the later Write", sr.status, http.StatusNotFound)
	}
}

func TestStatusRecorder_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	unwrapper, ok := http.ResponseWriter(sr).(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		t.Fatal("statusRecorder does not implement Unwrap() http.ResponseWriter")
	}
	if unwrapper.Unwrap() != rec {
		t.Fatal("Unwrap() did not return the underlying ResponseWriter")
	}
}

func TestLogged_CapturesStatusAndPath(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	s := &Server{}
	handler := s.logged(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/notes/42", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	line := buf.String()
	wantParts := []string{"127.0.0.1:9999", "GET", "/api/notes/42", strconv.Itoa(http.StatusTeapot)}
	for _, part := range wantParts {
		if !strings.Contains(line, part) {
			t.Fatalf("log line %q missing %q", line, part)
		}
	}
}

// TestLogged_ImplicitOKThroughWrite is the full-stack sibling of
// TestStatusRecorder_ImplicitOK: it drives the real logged() middleware
// against a handler shaped like handleIndex (Write only, no explicit
// WriteHeader) end to end, so it would catch a regression even if some
// future change bypassed statusRecorder.Write directly.
func TestLogged_ImplicitOKThroughWrite(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	}()

	s := &Server{}
	handler := s.logged(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html></html>")) // no WriteHeader call, like handleIndex
	}))

	req := httptest.NewRequest(http.MethodGet, "/{$}", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("recorder code = %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(buf.String(), strconv.Itoa(http.StatusOK)) {
		t.Fatalf("log line %q missing implicit status %d", buf.String(), http.StatusOK)
	}
}
