package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// revisionBodies returns the body of every revision recorded for noteID,
// oldest first. It reaches into the store's private db handle directly —
// fine for a same-package test — rather than adding a public read API,
// since revision history isn't exposed outside this package.
func revisionBodies(t *testing.T, st *Store, noteID int64) []string {
	t.Helper()
	rows, err := st.db.Query(
		`SELECT body FROM note_revisions WHERE note_id = ? ORDER BY edited_at ASC, id ASC`, noteID)
	if err != nil {
		t.Fatalf("querying revisions: %v", err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scanning revision: %v", err)
		}
		bodies = append(bodies, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating revisions: %v", err)
	}
	return bodies
}

func TestListNotesEmptyIsNotNil(t *testing.T) {
	st := newTestStore(t)
	notes, err := st.ListNotes(context.Background())
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if notes == nil {
		t.Fatal("ListNotes returned nil, want a non-nil empty slice (frontend does notes.length unconditionally)")
	}
	if len(notes) != 0 {
		t.Fatalf("want 0 notes, got %d", len(notes))
	}
}

func TestCreateGetListNotes(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	n1, err := st.CreateNote(ctx, "First", "Body one")
	if err != nil {
		t.Fatalf("CreateNote n1: %v", err)
	}
	n2, err := st.CreateNote(ctx, "Second", "Body two")
	if err != nil {
		t.Fatalf("CreateNote n2: %v", err)
	}

	got, err := st.GetNote(ctx, n1.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.Title != "First" || got.Body != "Body one" {
		t.Fatalf("GetNote returned %+v, want title=First body=%q", got, "Body one")
	}

	list, err := st.ListNotes(ctx)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 notes, got %d", len(list))
	}
	// New notes land at the top (one position below the current minimum),
	// so the most recently created note should sort first.
	if list[0].ID != n2.ID || list[1].ID != n1.ID {
		t.Fatalf("want order [n2, n1] = [%d, %d], got [%d, %d]", n2.ID, n1.ID, list[0].ID, list[1].ID)
	}
}

func TestUpdateNoteRecordsPreviousRevision(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	n, err := st.CreateNote(ctx, "T1", "B1")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	updated, err := st.UpdateNote(ctx, n.ID, "T2", "B2")
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if updated.Title != "T2" || updated.Body != "B2" {
		t.Fatalf("UpdateNote returned %+v, want title=T2 body=B2", updated)
	}

	bodies := revisionBodies(t, st, n.ID)
	if len(bodies) != 1 || bodies[0] != "B1" {
		t.Fatalf("revisions = %v, want exactly the pre-update body [B1]", bodies)
	}
}

func TestUpdateNoteMissing(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.UpdateNote(context.Background(), 999, "x", "y"); err == nil {
		t.Fatal("UpdateNote on a missing id: want an error, got nil")
	}
}

// TestRevisionCapAt20 confirms UpdateNote keeps only the newest
// maxRevisionsPerNote (20) revisions. Each update writes a distinct body
// so surviving revisions can be verified by content, not by timestamp
// ordering (SQLite DATETIME precision could otherwise make ties
// ambiguous).
func TestRevisionCapAt20(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	n, err := st.CreateNote(ctx, "T", "initial")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	const updates = 25
	for i := 0; i < updates; i++ {
		if _, err := st.UpdateNote(ctx, n.ID, "T", fmt.Sprintf("body-%d", i)); err != nil {
			t.Fatalf("UpdateNote #%d: %v", i, err)
		}
	}

	bodies := revisionBodies(t, st, n.ID)
	if len(bodies) != maxRevisionsPerNote {
		t.Fatalf("got %d revisions, want %d (cap)", len(bodies), maxRevisionsPerNote)
	}
	// 25 updates over an initial body produce 25 recorded revisions
	// (the pre-update content each time): "initial", "body-0", ...,
	// "body-23". Trimming to the newest 20 drops the oldest 5
	// ("initial", "body-0"..."body-3"), leaving "body-4"..."body-23".
	if bodies[0] != "body-4" {
		t.Fatalf("oldest surviving revision = %q, want %q", bodies[0], "body-4")
	}
	if bodies[len(bodies)-1] != "body-23" {
		t.Fatalf("newest surviving revision = %q, want %q", bodies[len(bodies)-1], "body-23")
	}
}

func TestReorderNotes(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	a, _ := st.CreateNote(ctx, "A", "")
	b, _ := st.CreateNote(ctx, "B", "")
	c, _ := st.CreateNote(ctx, "C", "")

	want := []int64{a.ID, c.ID, b.ID}
	if err := st.ReorderNotes(ctx, want); err != nil {
		t.Fatalf("ReorderNotes: %v", err)
	}

	list, err := st.ListNotes(ctx)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(list) != len(want) {
		t.Fatalf("got %d notes, want %d", len(list), len(want))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("position %d: got id %d, want %d", i, list[i].ID, id)
		}
	}
}

func TestDeleteNoteCascadesRevisions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	n, err := st.CreateNote(ctx, "T", "B1")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := st.UpdateNote(ctx, n.ID, "T", "B2"); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if bodies := revisionBodies(t, st, n.ID); len(bodies) != 1 {
		t.Fatalf("want 1 revision before delete, got %d", len(bodies))
	}

	found, err := st.DeleteNote(ctx, n.ID)
	if err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if !found {
		t.Fatal("DeleteNote: want found=true for an existing note")
	}
	if bodies := revisionBodies(t, st, n.ID); len(bodies) != 0 {
		t.Fatalf("want revisions cascade-deleted, got %d remaining", len(bodies))
	}

	found, err = st.DeleteNote(ctx, n.ID)
	if err != nil {
		t.Fatalf("DeleteNote (again): %v", err)
	}
	if found {
		t.Fatal("DeleteNote on an already-deleted id: want found=false")
	}
}

// TestMigratePositions is the regression test for the ordering gotcha
// documented in CLAUDE.md and store.go: Open must add notes.position to a
// pre-existing database (created before that column existed) *before*
// schema.sql's CREATE INDEX runs, or upgrading a real install breaks on
// startup. It exercises the real Store.Open upgrade path against a
// hand-built old-shape database, not migratePositions in isolation.
func TestMigratePositions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE notes (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL DEFAULT '',
			body       TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`); err != nil {
		raw.Close()
		t.Fatalf("creating old-shape notes table: %v", err)
	}
	// Seed rows whose updated_at order deliberately doesn't match
	// insertion/id order, so the backfill (ORDER BY updated_at DESC)
	// is actually being exercised rather than accidentally matching id
	// order for free.
	seed := []struct {
		title, updatedAt string
	}{
		{"oldest", "2024-01-01T00:00:00Z"},
		{"newest", "2024-03-01T00:00:00Z"},
		{"middle", "2024-02-01T00:00:00Z"},
	}
	for _, s := range seed {
		if _, err := raw.Exec(
			`INSERT INTO notes (title, body, created_at, updated_at) VALUES (?, '', ?, ?)`,
			s.title, s.updatedAt, s.updatedAt,
		); err != nil {
			raw.Close()
			t.Fatalf("seeding %q: %v", s.title, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	// The real upgrade path: Open must succeed against this pre-position
	// database instead of failing when schema.sql's CREATE INDEX runs
	// against a notes table lacking the column.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-migration database: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	list, err := st.ListNotes(ctx)
	if err != nil {
		t.Fatalf("ListNotes after migration: %v", err)
	}
	if len(list) != len(seed) {
		t.Fatalf("got %d notes after migration, want %d", len(list), len(seed))
	}
	// Backfilled position should match updated_at DESC: newest, middle, oldest.
	wantOrder := []string{"newest", "middle", "oldest"}
	for i, title := range wantOrder {
		if list[i].Title != title {
			t.Fatalf("position %d after migration: got title %q, want %q", i, list[i].Title, title)
		}
	}

	// The migrated position column must still work for new writes: a
	// fresh note should land at the top, ahead of every migrated row.
	created, err := st.CreateNote(ctx, "brand new", "")
	if err != nil {
		t.Fatalf("CreateNote after migration: %v", err)
	}
	list, err = st.ListNotes(ctx)
	if err != nil {
		t.Fatalf("ListNotes after post-migration create: %v", err)
	}
	if list[0].ID != created.ID {
		t.Fatalf("newest note (id %d) not at top after migration; got %+v", created.ID, list[0])
	}
}

// Sanity check that CreatedAt/UpdatedAt round-trip as real timestamps
// (not zero values) — a cheap guard against a broken scan/format.
func TestCreateNoteTimestamps(t *testing.T) {
	st := newTestStore(t)
	before := time.Now().Add(-time.Second)
	n, err := st.CreateNote(context.Background(), "T", "B")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	after := time.Now().Add(time.Second)
	if n.CreatedAt.Before(before) || n.CreatedAt.After(after) {
		t.Fatalf("CreatedAt = %v, want between %v and %v", n.CreatedAt, before, after)
	}
	if !n.CreatedAt.Equal(n.UpdatedAt) {
		t.Fatalf("CreatedAt (%v) and UpdatedAt (%v) should match on creation", n.CreatedAt, n.UpdatedAt)
	}
}
