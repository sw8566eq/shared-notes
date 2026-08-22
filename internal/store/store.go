// Package store wraps the SQLite database: notes (delete is permanent —
// no soft-delete/trash) and a small per-note revision history kept as an
// undo safety net for accidental edits.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// maxRevisionsPerNote bounds how much history we keep per note so the
// revisions table doesn't grow unbounded.
const maxRevisionsPerNote = 20

type Note struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// _pragma sets are applied per-connection by modernc.org/sqlite via the DSN.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	// A home LAN app with a handful of concurrent housemates doesn't need
	// a connection pool; one writer avoids SQLite's "database is locked"
	// surprises entirely.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Backup writes a consistent snapshot of the database to dstPath using
// SQLite's VACUUM INTO, which is safe to run against a live database.
func (s *Store) Backup(ctx context.Context, dstPath string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dstPath)
	return err
}

func (s *Store) ListNotes(ctx context.Context) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, body, created_at, updated_at FROM notes ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Start as an empty (not nil) slice so an empty result marshals to
	// JSON `[]` rather than `null` — the frontend does `notes.length`
	// unconditionally.
	notes := []Note{}
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func (s *Store) GetNote(ctx context.Context, id int64) (Note, error) {
	var n Note
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, body, created_at, updated_at
		 FROM notes WHERE id = ?`, id,
	).Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

func (s *Store) CreateNote(ctx context.Context, title, body string) (Note, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (title, body, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		title, body, now, now)
	if err != nil {
		return Note{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Note{}, err
	}
	return s.GetNote(ctx, id)
}

// UpdateNote saves new content and records the *previous* content as a
// revision, so a save never silently destroys what was there before.
func (s *Store) UpdateNote(ctx context.Context, id int64, title, body string) (Note, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, err
	}
	defer tx.Rollback()

	var prevTitle, prevBody string
	err = tx.QueryRowContext(ctx, `SELECT title, body FROM notes WHERE id = ?`, id).
		Scan(&prevTitle, &prevBody)
	if err != nil {
		return Note{}, err
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO note_revisions (note_id, title, body, edited_at) VALUES (?, ?, ?, ?)`,
		id, prevTitle, prevBody, now,
	); err != nil {
		return Note{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM note_revisions WHERE note_id = ? AND id NOT IN (
			SELECT id FROM note_revisions WHERE note_id = ? ORDER BY edited_at DESC LIMIT ?
		)`, id, id, maxRevisionsPerNote,
	); err != nil {
		return Note{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE notes SET title = ?, body = ?, updated_at = ? WHERE id = ?`,
		title, body, now, id,
	); err != nil {
		return Note{}, err
	}

	if err := tx.Commit(); err != nil {
		return Note{}, err
	}
	return s.GetNote(ctx, id)
}

// DeleteNote permanently removes a note and its revision history
// (cascaded via the foreign key). There is no undo — see README for why
// this is a deliberate choice rather than an oversight. found reports
// whether a note with that id existed to delete.
func (s *Store) DeleteNote(ctx context.Context, id int64) (found bool, err error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
