(() => {
  "use strict";

  const state = { notes: [], editingId: null };

  const el = (id) => document.getElementById(id);
  const noteList = el("note-list");
  const emptyState = el("empty-state");
  const fab = el("fab");
  const backdrop = el("modal-backdrop");
  const modalTitle = el("modal-title");
  const modalBody = el("modal-body");
  const modalMeta = el("modal-meta");
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

  async function api(path, opts) {
    const res = await fetch(path, opts);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body.error || `${res.status} ${res.statusText}`);
    }
    if (res.status === 204) return null;
    return res.json();
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
    emptyState.hidden = state.notes.length !== 0;
    noteList.innerHTML = state.notes.map(cardHTML).join("");
  }

  function cardHTML(n) {
    const preview = (n.body || "").slice(0, 160);
    const title = n.title || "(untitled)";
    return `
      <li class="note-card" data-id="${n.id}" draggable="true">
        <span class="drag-handle" title="Drag to reorder" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="16" height="16"><path fill="currentColor" d="M9 6a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm6-16a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4zm0 8a2 2 0 1 1 0-4 2 2 0 0 1 0 4z"/></svg>
        </span>
        <div class="note-card-main">
          <div class="note-card-title">${escapeHTML(title)}</div>
          <div class="note-card-preview">${escapeHTML(preview)}</div>
        </div>
        <button class="copy-btn" data-id="${n.id}" type="button" title="Copy to clipboard" aria-label="Copy to clipboard">
          <svg class="icon-copy" viewBox="0 0 24 24" width="18" height="18"><path fill="currentColor" d="M16 1H4a2 2 0 0 0-2 2v14h2V3h12V1zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm0 16H8V7h11v14z"/></svg>
          <svg class="icon-check" viewBox="0 0 24 24" width="18" height="18" hidden><path fill="currentColor" d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>
        </button>
      </li>`;
  }

  function setConfirmingDelete(confirming) {
    modalActionsNormal.hidden = confirming;
    modalActionsConfirm.hidden = !confirming;
  }

  function openModal(note) {
    state.editingId = note ? note.id : null;
    modalTitle.value = note ? note.title : "";
    modalBody.value = note ? note.body : "";
    modalMeta.textContent = note ? `Last edited ${fmtTime(note.updatedAt)}` : "New note";
    modalDelete.hidden = !note;
    setConfirmingDelete(false);
    backdrop.hidden = false;
    modalTitle.focus();
  }

  function closeModal() {
    backdrop.hidden = true;
    state.editingId = null;
    setConfirmingDelete(false);
  }

  async function saveModal() {
    const payload = { title: modalTitle.value.trim(), body: modalBody.value };
    if (!payload.title && !payload.body.trim()) {
      closeModal();
      return;
    }
    const note = state.editingId
      ? await api(`/api/notes/${state.editingId}`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
      : await api("/api/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
    upsertLocal(note);
    render();
    closeModal();
  }

  async function deleteModal() {
    if (!state.editingId) return;
    await api(`/api/notes/${state.editingId}`, { method: "DELETE" });
    removeLocal(state.editingId);
    render();
    closeModal();
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
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !backdrop.hidden) closeModal();
  });

  function connectWS() {
    const proto = location.protocol === "https:" ? "wss://" : "ws://";
    const ws = new WebSocket(proto + location.host + "/ws");
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.type === "note_upsert") {
        upsertLocal(msg.note);
        render();
      } else if (msg.type === "note_delete") {
        removeLocal(msg.id);
        render();
      } else if (msg.type === "notes_reorder") {
        reorderLocal(msg.ids);
        render();
      }
    };
    ws.onclose = () => setTimeout(connectWS, 2000);
  }

  async function init() {
    state.notes = (await api("/api/notes")) || [];
    render();
    connectWS();
  }

  init().catch((err) => {
    document.body.innerHTML = `<p style="padding:2rem;font-family:sans-serif">Failed to load: ${escapeHTML(err.message)}</p>`;
  });
})();
