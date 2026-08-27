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
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	// A handler that only calls Write, without an explicit WriteHeader,
	// gets an implicit 200 from net/http — statusRecorder must default
	// to that rather than reporting a zero value.
	if _, err := sr.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sr.status != http.StatusOK {
		t.Fatalf("status = %d, want default %d", sr.status, http.StatusOK)
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
