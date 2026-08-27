package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"linkshr/internal/hub"
	"linkshr/internal/store"
	"linkshr/web"
)

// newTestServer builds a real Server (real store on a temp file, real
// embedded web.Files, real hub) behind an httptest.Server, so tests
// exercise the actual routing/middleware/broadcast wiring rather than a
// hand-rolled substitute. Deliberately not shared with internal/store's
// test helper of the same shape — small enough that duplicating it per
// package beats an extra shared test-only package for a project this
// size.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv, err := New(st, hub.New(), web.Files)
	if err != nil {
		t.Fatalf("httpserver.New: %v", err)
	}
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func doRequest(t *testing.T, method, url, contentType, body string) *http.Response {
	t.Helper()
	return doRequestClient(t, method, url, contentType, body, "")
}

// doRequestClient is doRequest plus an optional clientIDHeader value, for
// tests that need to check which connection a broadcast gets attributed
// to (see event.Origin).
func doRequestClient(t *testing.T, method, url, contentType, body, clientID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building %s %s: %v", method, url, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if clientID != "" {
		req.Header.Set(clientIDHeader, clientID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func TestCreateNote_RequiresJSONContentType(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/notes", "text/plain", strings.NewReader(`{"title":"x","body":"y"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestCreateNote_BodyTooLarge(t *testing.T) {
	ts := newTestServer(t)
	big := strings.Repeat("a", maxNoteBytes+1)
	resp := postJSON(t, ts.URL+"/api/notes", fmt.Sprintf(`{"title":"x","body":"%s"}`, big))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestCreateNote_Success(t *testing.T) {
	ts := newTestServer(t)
	resp := postJSON(t, ts.URL+"/api/notes", `{"title":"Hello","body":"World"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var n store.Note
	if err := json.NewDecoder(resp.Body).Decode(&n); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if n.Title != "Hello" || n.Body != "World" {
		t.Fatalf("created note = %+v, want title=Hello body=World", n)
	}
}

func TestReorder_RequiresJSONContentType(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodPut, ts.URL+"/api/notes/reorder", "text/plain", `{"ids":[1,2]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestReorder_BodyTooLarge(t *testing.T) {
	ts := newTestServer(t)
	var buf bytes.Buffer
	buf.WriteString(`{"ids":[`)
	for i := 0; buf.Len() < maxReorderBytes+1; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString("1")
	}
	buf.WriteString(`]}`)
	resp := doRequest(t, http.MethodPut, ts.URL+"/api/notes/reorder", "application/json", buf.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestGetNote_NotFound(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/notes/999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestUpdateNote_NotFound(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodPut, ts.URL+"/api/notes/999", "application/json", `{"title":"x","body":"y"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDeleteNote_NotFound(t *testing.T) {
	ts := newTestServer(t)
	resp := doRequest(t, http.MethodDelete, ts.URL+"/api/notes/999", "", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// wsURL turns an httptest.Server's http(s) URL into the matching ws(s) URL.
func wsURL(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	switch {
	case strings.HasPrefix(ts.URL, "https://"):
		return "wss://" + strings.TrimPrefix(ts.URL, "https://") + "/ws"
	case strings.HasPrefix(ts.URL, "http://"):
		return "ws://" + strings.TrimPrefix(ts.URL, "http://") + "/ws"
	default:
		t.Fatalf("unexpected test server URL %q", ts.URL)
		return ""
	}
}

// readEvent reads and decodes the next broadcast event off conn, failing
// the test if none arrives within the timeout.
func readEvent(t *testing.T, conn *websocket.Conn) event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading ws message: %v", err)
	}
	var ev event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("decoding ws message %q: %v", data, err)
	}
	return ev
}

// dialWS connects to /ws and consumes the "hello" message every
// connection gets first, returning the connection and the ClientID it
// was assigned.
func dialWS(t *testing.T, ts *httptest.Server) (*websocket.Conn, string) {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL(t, ts), nil)
	if err != nil {
		t.Fatalf("dialing /ws: %v", err)
	}
	hello := readEvent(t, conn)
	if hello.Type != "hello" || hello.ClientID == "" {
		t.Fatalf("first message = %+v, want a hello with a non-empty clientId", hello)
	}
	return conn, hello.ClientID
}

// TestWebSocketBroadcast_FullFlow exercises the real end-to-end path: a
// connected /ws client should see a note_upsert for create, another for
// update, a notes_reorder for reorder, and a note_delete for delete —
// the three mutation shapes internal/hub fans out — and each should
// carry back the clientId the request was tagged with in its Origin
// field, exactly, not just eventually/probably (see event's doc comment
// on why Origin exists at all).
func TestWebSocketBroadcast_FullFlow(t *testing.T) {
	ts := newTestServer(t)

	conn, clientID := dialWS(t, ts)
	defer conn.CloseNow()

	// Create.
	resp := doRequestClient(t, http.MethodPost, ts.URL+"/api/notes", "application/json", `{"title":"Hello","body":"World"}`, clientID)
	var created store.Note
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	ev := readEvent(t, conn)
	if ev.Type != "note_upsert" || ev.Note == nil || ev.Note.ID != created.ID {
		t.Fatalf("after create, got event %+v, want note_upsert for id %d", ev, created.ID)
	}
	if ev.Origin != clientID {
		t.Fatalf("after create, event origin = %q, want the requesting client's ID %q", ev.Origin, clientID)
	}

	// Update.
	updateResp := doRequestClient(t, http.MethodPut, fmt.Sprintf("%s/api/notes/%d", ts.URL, created.ID),
		"application/json", `{"title":"Hello","body":"Updated"}`, clientID)
	updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want %d", updateResp.StatusCode, http.StatusOK)
	}
	ev = readEvent(t, conn)
	if ev.Type != "note_upsert" || ev.Note == nil || ev.Note.Body != "Updated" {
		t.Fatalf("after update, got event %+v, want note_upsert with body=Updated", ev)
	}
	if ev.Origin != clientID {
		t.Fatalf("after update, event origin = %q, want %q", ev.Origin, clientID)
	}

	// Reorder (single-note list, but exercises the broadcast shape).
	reorderResp := doRequestClient(t, http.MethodPut, ts.URL+"/api/notes/reorder",
		"application/json", fmt.Sprintf(`{"ids":[%d]}`, created.ID), clientID)
	reorderResp.Body.Close()
	if reorderResp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder status = %d, want %d", reorderResp.StatusCode, http.StatusNoContent)
	}
	ev = readEvent(t, conn)
	if ev.Type != "notes_reorder" || len(ev.IDs) != 1 || ev.IDs[0] != created.ID {
		t.Fatalf("after reorder, got event %+v, want notes_reorder with ids=[%d]", ev, created.ID)
	}
	if ev.Origin != clientID {
		t.Fatalf("after reorder, event origin = %q, want %q", ev.Origin, clientID)
	}

	// Delete.
	deleteResp := doRequestClient(t, http.MethodDelete, fmt.Sprintf("%s/api/notes/%d", ts.URL, created.ID), "", "", clientID)
	deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}
	ev = readEvent(t, conn)
	if ev.Type != "note_delete" || ev.ID != created.ID {
		t.Fatalf("after delete, got event %+v, want note_delete for id %d", ev, created.ID)
	}
	if ev.Origin != clientID {
		t.Fatalf("after delete, event origin = %q, want %q", ev.Origin, clientID)
	}
}

// TestWebSocketBroadcast_OriginDistinguishesConnections is the other
// half of the origin-tagging contract: two different connections must
// get two different IDs, and a broadcast caused by one must name that
// one, not the other — the exact comparison the frontend now relies on
// instead of the old self-mutation timing guess.
func TestWebSocketBroadcast_OriginDistinguishesConnections(t *testing.T) {
	ts := newTestServer(t)

	connA, clientA := dialWS(t, ts)
	defer connA.CloseNow()
	connB, clientB := dialWS(t, ts)
	defer connB.CloseNow()

	if clientA == clientB {
		t.Fatalf("two separate connections got the same clientId %q", clientA)
	}

	resp := doRequestClient(t, http.MethodPost, ts.URL+"/api/notes", "application/json", `{"title":"Hello","body":"World"}`, clientA)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	for _, tc := range []struct {
		name string
		conn *websocket.Conn
	}{{"A (the actor)", connA}, {"B (a bystander)", connB}} {
		ev := readEvent(t, tc.conn)
		if ev.Type != "note_upsert" {
			t.Fatalf("connection %s: got event type %q, want note_upsert", tc.name, ev.Type)
		}
		if ev.Origin != clientA {
			t.Fatalf("connection %s: event origin = %q, want the actor's ID %q — a bystander must be able to tell this wasn't its own change", tc.name, ev.Origin, clientA)
		}
	}
}
