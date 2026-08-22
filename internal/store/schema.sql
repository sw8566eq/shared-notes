CREATE TABLE IF NOT EXISTS notes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- Manual order (drag-to-reorder in the UI). New notes get a position
-- below the current lowest so they appear at the top; see
-- Store.CreateNote / Store.ReorderNotes. Databases created before this
-- column existed are migrated in Store.Open (migratePositions).
CREATE INDEX IF NOT EXISTS idx_notes_position ON notes(position);

-- Undo safety net for accidental *edits* (not deletes — deleting a note
-- is permanent, see README). Every save keeps a copy of the previous
-- content, so an overwrite while editing is still recoverable.
CREATE TABLE IF NOT EXISTS note_revisions (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id   INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    title     TEXT NOT NULL,
    body      TEXT NOT NULL,
    edited_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_revisions_note_id ON note_revisions(note_id, edited_at);
