// Quick search.  ctrl+k and / open the dialog empty; any other key
// that types a character, pressed outside a form field and without
// ctrl, alt, or meta, opens it with that character as the query's
// first.  In the dialog, and on the /search page's own field, the
// arrows move between rows, Enter opens the row in focus or the first,
// and Tab from the query reaches the first row's lens chips.  Escape
// and the close button close the dialog.  The page's body is swapped
// on every navigation, so each lookup finds the dialog again rather
// than keeping a reference to it.
//
// htmx saves the body to its history before a row's page replaces it.
// A dialog saved open comes back on Back with its `open` attribute but
// not as a modal, where Escape does nothing, so the dialog is closed
// before each save, and closed again on any restore that still has it
// open.
//
// The script runs in a function of its own, because every page script
// shares one global scope and stream.js already names `open`.
(() => {
  const dialog = () => document.getElementById("search");

  // Where the keys act: the dialog while it is open, or the /search
  // page while focus is in it.  Each has its own field and its own
  // list of rows.
  function scope() {
    const box = dialog();
    if (box?.open) return box;
    return document.activeElement?.closest?.(".search-page") ?? null;
  }
  const query = () => scope()?.querySelector(".search-input");
  const rows = () => [...(scope()?.querySelectorAll(".search-row") ?? [])];

  // The query whose rows are shown is stored on its field.  The rows
  // are out of date from the field's first keystroke, through the
  // trigger's delay, until that query's request swaps its rows in.  An
  // Enter or a Tab pressed meanwhile waits for the new rows, so it acts
  // on the first of them, not on the first of the last query's rows.  A
  // field the server drew with a query, as the /search page's is, shows
  // that query's rows.
  let openWhenSwapped = false;
  let chipWhenSwapped = false;

  function pending() {
    const input = query();
    if (!input) return false;
    return input.value !== (input.dataset.shown ?? input.defaultValue);
  }

  function firstChip() {
    return rows()[0]?.closest(".search-result")?.querySelector(".search-lens");
  }

  function open(text) {
    const box = dialog();
    const input = box?.querySelector(".search-input");
    if (!box || !input) return;
    if (!box.open) box.showModal();
    input.value = text;
    input.focus();
    htmx.trigger(input, "search");
  }

  function close() {
    const box = dialog();
    if (!box?.open) return;
    box.close();
    const input = box.querySelector(".search-input");
    if (input) input.value = "";
    box.querySelector(".search-results")?.replaceChildren();
  }

  document.addEventListener("htmx:beforeHistorySave", close);
  document.addEventListener("htmx:historyRestore", close);
  document.addEventListener("click", (event) => {
    if (event.target.closest?.(".search-close")) close();
    else if (event.target.closest?.(".search-open")) open("");
  });

  function typing(target) {
    return target.closest?.("input, textarea, select, [contenteditable]") != null;
  }

  document.addEventListener("keydown", (event) => {
    if (scope()) {
      within(event);
      return;
    }
    const modified = event.ctrlKey || event.altKey || event.metaKey;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      open("");
    } else if (typing(event.target) || modified) {
      return;
    } else if (event.key === "/") {
      event.preventDefault();
      open("");
    } else if (event.key.length === 1 && event.key !== " ") {
      event.preventDefault();
      open(event.key);
    }
  });

  function within(event) {
    const input = query();
    const list = rows();
    const row = document.activeElement?.closest?.(".search-result");
    const at = row ? list.indexOf(row.querySelector(".search-row")) : -1;
    if (event.key === "Escape" && dialog()?.open) {
      event.preventDefault();
      close();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      list[Math.min(at + 1, list.length - 1)]?.focus();
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (at <= 0) input?.focus();
      else list[at - 1].focus();
    } else if (event.key === "Enter" && document.activeElement === input) {
      event.preventDefault();
      if (pending()) openWhenSwapped = true;
      else list[0]?.click();
    } else if (event.key === "Tab" && !event.shiftKey && document.activeElement === input) {
      if (pending()) {
        event.preventDefault();
        chipWhenSwapped = true;
        return;
      }
      const chip = firstChip();
      if (chip) {
        event.preventDefault();
        chip.focus();
      }
    }
  }

  // A request's rows are swapped in before htmx reports the request
  // done.  An older request that a newer one replaced also ends here,
  // with the older query, so the rows stay out of date until the newest
  // request's rows arrive.
  document.addEventListener("htmx:afterRequest", (event) => {
    if (!event.detail.elt.matches?.(".search-input")) return;
    const path = event.detail.pathInfo?.finalRequestPath ?? "";
    const asked = new URL(path, location.origin).searchParams.get("q") ?? "";
    event.detail.elt.dataset.shown = asked;
    if (pending()) return;
    if (openWhenSwapped) {
      openWhenSwapped = false;
      chipWhenSwapped = false;
      requestAnimationFrame(() => rows()[0]?.click());
    } else if (chipWhenSwapped) {
      chipWhenSwapped = false;
      requestAnimationFrame(() => firstChip()?.focus());
    }
  });
})();
