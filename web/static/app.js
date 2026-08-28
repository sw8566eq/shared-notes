(() => {
  "use strict";

  const state = { notes: [], editingId: null, query: "", theme: "light", clientId: null };

  const el = (id) => document.getElementById(id);
  const noteList = el("note-list");
  const emptyState = el("empty-state");
  const searchInput = el("search-input");
  const exportAllBtn = el("export-all-btn");
  const themeToggle = el("theme-toggle");
  const liveRegion = el("live-region");
  const fab = el("fab");
  const backdrop = el("modal-backdrop");
  const modalEl = backdrop.querySelector(".modal");
  const modalTitle = el("modal-title");
  const modalBody = el("modal-body");
  const modalMeta = el("modal-meta");
  const modalExport = el("modal-export");
  const modalSave = el("modal-save");
  const modalClose = el("modal-close");
  const modalDelete = el("modal-delete");
  const modalActionsNormal = el("modal-actions-normal");
  const modalActionsConfirm = el("modal-actions-confirm");
  const confirmDeleteCancel = el("confirm-delete-cancel");
  const confirmDeleteYes = el("confirm-delete-yes");

  function fmtTime(iso) {
    const d = new Date(iso);
    return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  // Tags every request with this tab's WS connection ID (once known), so
  // the server can stamp its broadcasts with who caused them — see the
  // self-mutation section below for why that beats guessing from timing.
  async function api(path, opts) {
    opts = opts || {};
    const headers = Object.assign({}, opts.headers);
    if (state.clientId) headers["X-Linkshr-Client"] = state.clientId;
    const res = await fetch(path, Object.assign({}, opts, { headers }));
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || `${res.status} ${res.statusText}`);
    }
    if (res.status === 204) return null;
    return res.json();
  }

  // --- Dark mode -------------------------------------------------------
  // A tiny inline <script> in index.html's <head> already applies any
  // stored preference before first paint (to avoid a flash of the wrong
  // theme); initTheme() below re-derives the same value — falling back to
  // the OS preference when nothing's stored yet — and syncs the toggle's
  // icon/aria-pressed state to match.

  const THEME_KEY = "linkshr-theme";

  // Set while initTheme() is tracking the live OS preference (no explicit
  // choice stored yet); cleared by stopTrackingOS() the moment an explicit
  // preference takes over, so that old listener can't keep firing and
  // desyncing the toggle icon from the actually-applied theme — see
  // applyTheme.
  let osMedia = null;
  let syncToOS = null;

  function syncToggleUI(theme) {
    themeToggle.setAttribute("aria-pressed", String(theme === "dark"));
    themeToggle.querySelector(".icon-theme-dark").hidden = theme === "dark";
    themeToggle.querySelector(".icon-theme-light").hidden = theme !== "dark";
  }

  function stopTrackingOS() {
    if (osMedia && syncToOS) osMedia.removeEventListener("change", syncToOS);
    osMedia = null;
    syncToOS = null;
  }

  // Sets an explicit preference: state, the toggle icon, and the
  // data-theme attribute style.css keys off. Only call this for a
  // preference the user actually chose (toggleTheme, or initTheme
  // re-applying a stored one) — see initTheme for why the OS-fallback
  // path deliberately doesn't call this.
  function applyTheme(theme) {
    stopTrackingOS();
    state.theme = theme;
    document.documentElement.setAttribute("data-theme", theme);
    syncToggleUI(theme);
  }

  function toggleTheme() {
    const next = state.theme === "dark" ? "light" : "dark";
    try { localStorage.setItem(THEME_KEY, next); } catch { /* private mode, etc. — toggle still works this session */ }
    applyTheme(next);
  }

  function initTheme() {
    let stored = null;
    try { stored = localStorage.getItem(THEME_KEY); } catch { /* ignore */ }
    if (stored === "light" || stored === "dark") {
      applyTheme(stored);
      return;
    }
    // No explicit preference: track the OS setting live instead of
    // pinning to whatever it happened to be at load. Deliberately not
    // calling applyTheme here — it sets the data-theme attribute, and
    // style.css makes that attribute outrank the
    // @media(prefers-color-scheme) rule, which would stop this tab
    // from following any later OS-level theme switch for the rest of
    // the session.
    let media = null;
    try {
      media = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)");
    } catch { /* restricted/sandboxed context with no working matchMedia — falls back to light below */ }
    const sync = () => {
      state.theme = media && media.matches ? "dark" : "light";
      syncToggleUI(state.theme);
    };
    sync();
    if (media) {
      media.addEventListener("change", sync);
      osMedia = media;
      syncToOS = sync;
    }
  }

  initTheme();

  // --- Export ------------------------------------------------------------

  function downloadBlob(filename, content, mime) {
    const blob = new Blob([content], { type: mime });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    // Revoking in the same tick is a known footgun in WebKit-family
    // browsers: the download can still be reading the blob when the
    // URL is invalidated, producing an empty/truncated file. Give it a
    // beat — the object URL only needs to outlive the click, not the
    // page.
    setTimeout(() => URL.revokeObjectURL(url), 30000);
  }

  function safeFilename(s) {
    return (s || "").trim().replace(/[\\/:*?"<>|]+/g, "_").slice(0, 80) || "note";
  }

  function exportNoteTxt(note) {
    const text = note.title ? `${note.title}\n\n${note.body}` : note.body;
    downloadBlob(safeFilename(note.title) + ".txt", text, "text/plain");
  }

  // yyyy-mm-dd in the viewer's local calendar date, not UTC's — a
  // straight toISOString().slice(0,10) is always the UTC date, which
  // reads as tomorrow's date to anyone west of UTC in the evening.
  function localDateStamp(d) {
    const pad = (n) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  }

  async function exportAllJSON() {
    // Fetch fresh rather than trusting state.notes, which may be stale.
    const notes = await api("/api/notes");
    downloadBlob(`linkshr-notes-${localDateStamp(new Date())}.json`, JSON.stringify(notes, null, 2), "application/json");
  }

  // --- Search/filter -------------------------------------------------

  function matchesQuery(n, query) {
    if (!query) return true;
    const needle = query.toLowerCase();
    return (n.title || "").toLowerCase().includes(needle) || (n.body || "").toLowerCase().includes(needle);
  }

  // Order is manual (drag-to-reorder), not derived from recency, so an
  // edit replaces a note in place; only a genuinely new note gets
  // inserted, at the top — matching where the server puts it.
  function upsertLocal(note) {
    const i = state.notes.findIndex((n) => n.id === note.id);
    if (i >= 0) state.notes[i] = note;
    else state.notes.unshift(note);
  }

  function reorderLocal(ids) {
    const byId = new Map(state.notes.map((n) => [n.id, n]));
    const reordered = ids.map((id) => byId.get(id)).filter(Boolean);
    const seen = new Set(ids);
    for (const n of state.notes) if (!seen.has(n.id)) reordered.push(n);
    state.notes = reordered;
  }

  function removeLocal(id) {
    state.notes = state.notes.filter((n) => n.id !== id);
  }

  function render() {
    const query = state.query.trim();
    const visible = state.notes.filter((n) => matchesQuery(n, query));
    if (state.notes.length === 0) {
      emptyState.textContent = "No notes yet — tap + to add one.";
      emptyState.hidden = false;
    } else if (visible.length === 0) {
      emptyState.textContent = "No notes match your search.";
      emptyState.hidden = false;
    } else {
      emptyState.hidden = true;
    }
    noteList.innerHTML = visible.map(cardHTML).join("");
  }

  function cardHTML(n) {
    const preview = (n.body || "").slice(0, 160);
    const title = n.title || "(untitled)";
    // Dragging a filtered (partial) list would corrupt the other notes'
    // positions, so reordering is disabled while a search is active.
    const draggable = state.query.trim() ? "false" : "true";
    return `
      <li class="note-card" data-id="${n.id}" draggable="${draggable}">
        <span class="drag-handle" title="Drag to reorder" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16"><path fill="currentColor" d="M9 6a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm6-16a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4z"/></svg>
        </span>
        <div class="note-card-main">
          <div class="note-card-title">${escapeHTML(title)}</div>
          <div class="note-card-preview">${escapeHTML(preview)}</div>
        </div>
        <button class="icon-btn copy-btn" data-id="${n.id}" type="button" title="Copy to clipboard" aria-label="Copy to clipboard">
          <svg class="icon-copy" viewBox="0 0 24 24" width="18" height="18"><path fill="currentColor" d="M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm0 16H8V7h11v14z"/></svg>
          <svg class="icon-check" viewBox="0 0 24 24" width="18" height="18" hidden><path fill="currentColor" d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>
        </button>
      </li>`;
  }

  function setConfirmingDelete(confirming) {
    modalActionsNormal.hidden = confirming;
    modalActionsConfirm.hidden = !confirming;
  }

  // Restored on close so closing the modal (Escape, backdrop click, Save,
  // Delete) never strands keyboard focus on a page element that's gone.
  let lastFocusedEl = null;

  function openModal(note) {
    lastFocusedEl = document.activeElement;
    state.editingId = note ? note.id : null;
    modalTitle.value = note ? note.title : "";
    modalBody.value = note ? note.body : "";
    modalMeta.textContent = note ? `Last edited ${fmtTime(note.updatedAt)}` : "New note";
    modalDelete.hidden = !note;
    modalExport.hidden = !note;
    setConfirmingDelete(false);
    backdrop.hidden = false;
    modalTitle.focus();
  }

  function closeModal() {
    backdrop.hidden = true;
    state.editingId = null;
    setConfirmingDelete(false);
    // Elements inside #note-list never survive to be refocused: render()
    // (called right after this, on every save/delete/reorder — and also
    // reachable *before* this point, since a save's own WS self-echo can
    // race ahead of its HTTP response and trigger a render() while
    // saveModal() is still awaiting) always rebuilds that whole subtree
    // from scratch rather than patching it. So a lastFocusedEl in there
    // is either already detached (isConnected false — the race above
    // already tore it down) or, if a render() hasn't caught up yet,
    // about to be replaced by an equivalent-looking but distinct node.
    // Note .contains() alone can't tell "detached" from "never was in
    // note-list" — both return false — hence the explicit isConnected
    // check. Either way, .focus()'ing it here would be moot or
    // immediately undone, so fall back to the FAB — always present,
    // never touched by render() — instead of letting focus silently
    // fall through to <body>.
    const target = lastFocusedEl && lastFocusedEl.isConnected && !noteList.contains(lastFocusedEl) ? lastFocusedEl : fab;
    target.focus();
    lastFocusedEl = null;
  }

  // --- Self-mutation suppression for the aria-live region ----------------
  // The hub broadcasts every change to every connected client, including
  // the tab that made it — so without this, saving/deleting/reordering
  // would announce your own action back at you as if someone else did it.
  // The server tags each broadcast with the clientId of whichever
  // connection's request caused it (event.origin — see notes.go), so this
  // is an exact match against this tab's own ID, not a guess: no window
  // to race, no genuine remote change that can be wrongly swallowed.
  function isSelf(origin) {
    return !!state.clientId && origin === state.clientId;
  }

  function announce(msg) {
    // Clear first so two identical messages in a row both get announced
    // (screen readers generally don't re-announce unchanged text).
    liveRegion.textContent = "";
    requestAnimationFrame(() => { liveRegion.textContent = msg; });
  }

  async function saveModal() {
    const payload = { title: modalTitle.value.trim(), body: modalBody.value };
    if (!payload.title && !payload.body.trim()) {
      closeModal();
      return;
    }
    const id = state.editingId;
    const note = id
      ? await api(`/api/notes/${id}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
      : await api("/api/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    upsertLocal(note);
    // closeModal() before render(): it restores focus to whatever
    // triggered openModal, which can be a note card's own child (e.g.
    // the copy button). render() rebuilds #note-list's innerHTML, and
    // .focus() on a since-detached node is a silent no-op — so the
    // restore has to happen while that node still exists.
    closeModal();
    render();
  }

  async function deleteModal() {
    if (!state.editingId) return;
    const id = state.editingId;
    await api(`/api/notes/${id}`, { method: "DELETE" });
    removeLocal(id);
    closeModal(); // before render() — see saveModal's comment
    render();
  }

  async function copyNote(id, btn) {
    const note = state.notes.find((n) => n.id === Number(id));
    if (!note) return;
    try {
      await navigator.clipboard.writeText(note.body);
      flashCopied(btn);
    } catch {
      alert("Couldn't copy to clipboard — your browser blocked it.");
    }
  }

  function flashCopied(btn) {
    btn.querySelector(".icon-copy").hidden = true;
    btn.querySelector(".icon-check").hidden = false;
    setTimeout(() => {
      btn.querySelector(".icon-copy").hidden = false;
      btn.querySelector(".icon-check").hidden = true;
    }, 1200);
  }

  // Drag-to-reorder: native HTML5 drag and drop, no library. The dragged
  // card is physically moved in the DOM as it crosses other cards, so what
  // you see while dragging is exactly what gets saved on drop.
  let dragId = null;

  noteList.addEventListener("dragstart", (e) => {
    if (state.query.trim()) return; // reordering a filtered list would corrupt positions
    const card = e.target.closest(".note-card");
    if (!card) return;
    dragId = Number(card.dataset.id);
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", String(dragId)); // Firefox requires data to be set to allow the drag
    card.classList.add("dragging");
  });

  noteList.addEventListener("dragend", (e) => {
    const card = e.target.closest(".note-card");
    if (card) card.classList.remove("dragging");
    dragId = null;
  });

  noteList.addEventListener("dragover", (e) => {
    if (dragId == null) return;
    e.preventDefault(); // required to allow a drop
    const card = e.target.closest(".note-card");
    if (!card || Number(card.dataset.id) === dragId) return;
    const dragEl = noteList.querySelector(`.note-card[data-id="${dragId}"]`);
    if (!dragEl) return;
    const rect = card.getBoundingClientRect();
    const before = e.clientY - rect.top < rect.height / 2;
    (before ? card.before(dragEl) : card.after(dragEl));
  });

  noteList.addEventListener("drop", (e) => {
    if (dragId == null) return;
    e.preventDefault();
    const prevOrder = state.notes.slice();
    const ids = [...noteList.querySelectorAll(".note-card")].map((li) => Number(li.dataset.id));
    reorderLocal(ids);
    api("/api/notes/reorder", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ids }) })
      .catch((err) => {
        state.notes = prevOrder; // the save failed — undo the optimistic reorder
        render();
        alert("Couldn't save the new order: " + err.message);
      });
  });

  noteList.addEventListener("click", (e) => {
    const copyBtn = e.target.closest(".copy-btn");
    if (copyBtn) {
      e.stopPropagation();
      copyNote(copyBtn.dataset.id, copyBtn);
      return;
    }
    const card = e.target.closest(".note-card");
    if (card) {
      const note = state.notes.find((n) => n.id === Number(card.dataset.id));
      if (note) openModal(note);
    }
  });

  fab.addEventListener("click", () => openModal(null));
  modalClose.addEventListener("click", closeModal);
  modalSave.addEventListener("click", () => saveModal().catch((e) => alert(e.message)));
  modalDelete.addEventListener("click", () => setConfirmingDelete(true));
  confirmDeleteCancel.addEventListener("click", () => setConfirmingDelete(false));
  confirmDeleteYes.addEventListener("click", () => deleteModal().catch((e) => alert(e.message)));
  backdrop.addEventListener("click", (e) => {
    if (e.target === backdrop) closeModal();
  });

  searchInput.addEventListener("input", () => {
    state.query = searchInput.value;
    render();
  });
  exportAllBtn.addEventListener("click", () => exportAllJSON().catch((e) => alert(e.message)));
  themeToggle.addEventListener("click", toggleTheme);
  modalExport.addEventListener("click", () => {
    // Export what's actually in the modal, not the last-saved copy in
    // state.notes — otherwise unsaved edits are silently left out.
    if (state.editingId == null) return;
    exportNoteTxt({ title: modalTitle.value, body: modalBody.value });
  });

  // Elements considered for the modal's focus trap. Computed fresh on
  // every Tab press (not cached at open time) since which buttons are
  // visible changes — e.g. the delete confirm swap.
  function focusableIn(container) {
    const selector = 'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';
    return [...container.querySelectorAll(selector)].filter((e) => e.offsetParent !== null);
  }

  function isTextEntryTarget(t) {
    return t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable;
  }

  document.addEventListener("keydown", (e) => {
    if (!backdrop.hidden) {
      if (e.key === "Tab") {
        const focusable = focusableIn(modalEl);
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey) {
          if (document.activeElement === first || !focusable.includes(document.activeElement)) {
            e.preventDefault();
            last.focus();
          }
        } else if (document.activeElement === last || !focusable.includes(document.activeElement)) {
          // The !includes case covers focus having landed somewhere in
          // the modal that isn't a tab stop (e.g. a click on the meta
          // text) — without it, a plain Tab from there has no guard at
          // all and can escape the still-open modal via the browser's
          // default tab order instead of re-entering the trap.
          e.preventDefault();
          first.focus();
        }
        return;
      }
      if (e.key === "Escape") {
        closeModal();
      }
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isTextEntryTarget(e.target)) return;
    if (e.key === "/") {
      e.preventDefault();
      searchInput.focus();
      searchInput.select();
    } else if (e.key === "n" || e.key === "N") {
      openModal(null);
    }
  });

  // Messages arriving before the initial /api/notes fetch below resolves
  // are queued instead of applied immediately: applying one against the
  // still-empty state.notes, only to have the fetch's snapshot then
  // unconditionally overwrite state.notes, would silently discard
  // whatever it did (a just-created note vanishing, a just-deleted one
  // resurrected). Replayed once notesLoaded flips true in init(); after
  // that first load, later messages (including across a reconnect) run
  // immediately as before.
  let notesLoaded = false;
  let pendingMsgs = [];

  function handleWSMessage(msg) {
    if (msg.type === "note_upsert") {
      const self = isSelf(msg.origin);
      const editingThis = state.editingId === msg.note.id;
      upsertLocal(msg.note);
      render();
      if (!self && !editingThis) {
        announce(`"${msg.note.title || "(untitled)"}" was updated by someone else.`);
      }
    } else if (msg.type === "note_delete") {
      const self = isSelf(msg.origin);
      const wasEditingThis = state.editingId === msg.id;
      const deleted = state.notes.find((n) => n.id === msg.id);
      removeLocal(msg.id);
      if (wasEditingThis && !self) {
        // closeModal() before render() — see saveModal's comment: it
        // restores focus to a node render() is about to detach.
        closeModal();
        render();
        announce("The note you were editing was deleted by someone else.");
      } else {
        render();
        if (!self) announce(deleted ? `"${deleted.title || "(untitled)"}" was deleted.` : "A note was deleted.");
      }
    } else if (msg.type === "notes_reorder") {
      const self = isSelf(msg.origin);
      reorderLocal(msg.ids);
      render();
      if (!self) announce("Note order was updated.");
    }
  }

  function connectWS() {
    const proto = location.protocol === "https:" ? "wss://" : "ws://";
    const ws = new WebSocket(proto + location.host + "/ws");
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.type === "hello") {
        state.clientId = msg.clientId;
        return;
      }
      if (!notesLoaded) {
        pendingMsgs.push(msg);
        return;
      }
      handleWSMessage(msg);
    };
    ws.onclose = () => setTimeout(connectWS, 2000);
  }

  async function init() {
    // Connect first: the /ws handshake and its "hello" (which assigns
    // state.clientId — see isSelf above) then has the whole initial
    // fetch below to complete before the page is interactive, rather
    // than racing a user's first click against it. Any mutation
    // broadcasts that arrive in the meantime are queued (see
    // pendingMsgs above) and replayed here, instead of being silently
    // clobbered by the fetch's snapshot.
    connectWS();
    state.notes = (await api("/api/notes")) || [];
    notesLoaded = true;
    render();
    const queued = pendingMsgs;
    pendingMsgs = [];
    queued.forEach(handleWSMessage);
  }

  init().catch((err) => {
    document.body.innerHTML = `<p style="padding:2rem;font-family:sans-serif">Failed to load: ${escapeHTML(err.message)}</p>`;
  });
})();
