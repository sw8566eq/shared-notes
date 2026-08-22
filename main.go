// Command linkshr runs the household shared notepad: a small HTTP +
// WebSocket server with SQLite storage, meant to run on the home LAN only.
// There's no login or per-person identity — see README.md.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"linkshr/internal/httpserver"
	"linkshr/internal/hub"
	"linkshr/internal/store"
	"linkshr/web"
)

const (
	backupInterval = 24 * time.Hour
	backupsToKeep  = 7
)

func main() {
	// Notes are plain text and this app has no login, so the database
	// file, its WAL/SHM sidecars, and every backup snapshot should still
	// be readable only by whoever owns the process — not by other local
	// accounts on this machine. A restrictive umask covers every file
	// this process creates without having to chmod each one individually
	// (Unix-only; this app targets Linux, see README).
	syscall.Umask(0o077)

	addr := flag.String("addr", ":8080", "address to listen on — bind this to your LAN interface, never port-forward it")
	dbPath := flag.String("db", "linkshr.db", "path to the SQLite database file")
	backupDir := flag.String("backup-dir", "backups", "directory for periodic database backups (empty to disable)")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	h := hub.New()

	srv, err := httpserver.New(st, h, web.Files)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	if *backupDir != "" {
		go runBackups(st, *backupDir)
	}

	// net/http's zero-value timeouts are unlimited, which leaves a plain
	// ListenAndServe open to slow-client resource exhaustion (Slowloris-
	// style). These only govern normal request/response handling — once
	// /ws hijacks a connection for WebSocket use, it's out from under the
	// server's timeout management, so this doesn't affect long-lived
	// WebSocket connections.
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("linkshr listening on %s", *addr)
	log.Printf("this app assumes it is ONLY reachable on your home LAN; do not port-forward it")
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// runBackups periodically snapshots the database with SQLite's
// VACUUM INTO, which is safe against a live database, and prunes old
// snapshots so backupDir doesn't grow without bound.
func runBackups(st *store.Store, dir string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("backups: %v, disabling", err)
		return
	}
	backupOnce(st, dir)
	ticker := time.NewTicker(backupInterval)
	defer ticker.Stop()
	for range ticker.C {
		backupOnce(st, dir)
	}
}

func backupOnce(st *store.Store, dir string) {
	dst := filepath.Join(dir, "linkshr-"+time.Now().UTC().Format("20060102-150405")+".db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.Backup(ctx, dst); err != nil {
		log.Printf("backup failed: %v", err)
		return
	}
	log.Printf("backup written: %s", dst)
	pruneBackups(dir)
}

func pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamped names sort chronologically
	for len(names) > backupsToKeep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
