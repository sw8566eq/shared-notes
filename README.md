# linkshr

A shared notepad for two housemates. Home-LAN-only, no accounts, no login,
no per-person identity at all — anyone who can reach it can read and edit
every note.

## Screenshots

| Notes list | Editing a note | Delete confirmation |
|---|---|---|
| ![Notes list](docs/screenshots/notes-list.png) | ![Note editor](docs/screenshots/note-editor.png) | ![Delete confirmation](docs/screenshots/delete-confirm.png) |

## Security model

There's nothing to configure here — that's the point. **The home WiFi
password is the entire access-control story.** This app must never be
port-forwarded or otherwise exposed past the LAN; anything that can reach
it is treated as fully trusted.

Notes carry no author — no name, no per-device identity, nothing to set
up. **Deleting a note is permanent** — no trash, no restore, no undo.
The one remaining safety net is narrower: a per-note revision history
(last 20 edits, kept internally, not exposed in the UI) protects against
accidentally overwriting a note *while editing it*, not against deleting
it on purpose.

Note: periodic backups (below) mean a deleted note can still exist in an
older backup snapshot for up to `backupsToKeep` × the backup interval
(7 days, by default) after you delete it — "permanent" means gone from
the live app and its revision history immediately, not necessarily from
every historical snapshot on disk. Shorten `-backup-dir`'s retention in
`main.go` if that gap matters to you.

**Hardening that's in place despite there being no login**, so a lack of
auth doesn't also mean a lack of basic web hygiene:
- All SQL is parameterized (no injection); note content is only ever
  rendered via `.value`/`.textContent` or explicitly HTML-escaped in the
  frontend (no stored/reflected XSS path).
- `/ws` rejects cross-origin WebSocket upgrade attempts by default (the
  library checks `Origin` against `Host`), so another website open in a
  housemate's browser can't silently listen in on the live note feed.
- Creating/editing a note requires an exact `application/json`
  Content-Type. This isn't pedantry — without it, a page on some other
  site could blind-POST a look-alike request (`Content-Type: text/plain`
  is a CORS "simple request" the browser sends without asking) and Go's
  JSON decoder doesn't care what Content-Type it arrived with. Requiring
  `application/json` forces a CORS preflight that this server never
  approves, so it can't be forged cross-origin. (`PUT`/`DELETE` were
  already safe this way, since browsers always preflight those methods —
  deleting a note is a `DELETE`, so this covers it too, with no
  restore-style endpoint left as a softer target.)
- Request bodies are capped at 256 KB and the HTTP server has explicit
  read/write/idle timeouts, so a huge or slow-drip request can't exhaust
  memory or tie up the server (doesn't affect `/ws`, which hands its
  connection off to the WebSocket library once upgraded).
- Server errors are logged locally but returned to clients as a generic
  message — internal details (file paths, driver errors) don't leak
  into API responses.
- The process runs under `umask 0077`, so the database file, its WAL/SHM
  files, and every backup snapshot are created readable only by whoever
  runs it — not by other local accounts on the machine. This only
  applies to files created *after* this change; if you already have a
  `linkshr.db` from before, run `chmod 600 linkshr.db` once by hand.

## Setup

1. Build it:
   ```
   go build -o linkshr .
   ```
2. Run it:
   ```
   ./linkshr -addr :8080
   ```
   Then visit `http://<this-machine's-LAN-IP>:8080` from either housemate's
   device.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8080` | Address to listen on. Bind to your LAN interface specifically (e.g. `-addr 192.168.1.5:8080`) if you want the extra layer of not even listening on other interfaces. |
| `-db` | `linkshr.db` | Path to the SQLite database file. |
| `-backup-dir` | `backups` | Where periodic DB snapshots go (empty string disables backups). A snapshot is taken on startup and every 24h; the last 7 are kept. |

## Running it as a background service

Since this runs on your everyday machine rather than a dedicated server,
it needs to survive you logging out (and ideally restarts if the machine
reboots). A systemd **user** service does this without needing root:

```ini
# ~/.config/systemd/user/linkshr.service
[Unit]
Description=linkshr household notepad

[Service]
WorkingDirectory=%h/linkshr
ExecStart=%h/linkshr/linkshr -addr :8080
Restart=on-failure

[Install]
WantedBy=default.target
```

(`%h` is systemd's specifier for your home directory — no need to hardcode a path or username.)

Then:
```
systemctl --user daemon-reload
systemctl --user enable --now linkshr
loginctl enable-linger "$USER"   # lets it keep running after you log out
```

Also worth checking your machine's sleep/suspend settings — a sleeping
machine means your housemate can't reach the notepad.
