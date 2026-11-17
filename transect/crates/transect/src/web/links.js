// A click with a modifier key opens a link in a new tab or window, as
// the browser does on its own; htmx would otherwise swap the page in
// here.  Stopping the click while it is captured at the document keeps
// it from reaching htmx's listeners and leaves the browser's default.
document.addEventListener("click", (event) => {
  const modified = event.ctrlKey || event.metaKey || event.shiftKey;
  if (modified && event.target.closest("a[href]")) event.stopPropagation();
}, true);

// Every link swaps its page into the body, and an htmx swap leaves the
// window scrolled where it was, so a link clicked far down one page
// would open the next page as far down.  A request for another path is
// a new page, which opens at its top; a request for the same path, such
// as a selection, an edge toggle, or a page fetched again when its tab
// shows, keeps the reader's place.  Back and Forward restore the place
// htmx saved with the page.
document.addEventListener("htmx:beforeRequest", (event) => {
  if (event.detail.target !== document.body) return;
  const next = new URL(event.detail.requestConfig.path, location.href);
  event.detail.xhr.newPage = next.pathname !== location.pathname;
});

// A link to a place on a page, such as a device on its machine's page,
// names it after a "#".  The page's event stream swaps its view in
// whole, and a swapped-in element is never the browser's :target, so
// the place is marked here after every swap.  htmx scrolls a new page
// to the place itself, after this; a new page with no place opens at
// its top.
const markTarget = (newPage) => {
  for (const old of document.querySelectorAll(".link-target")) old.classList.remove("link-target");
  const id = decodeURIComponent(location.hash.slice(1));
  const target = id && document.getElementById(id);
  if (target) target.classList.add("link-target");
  else if (newPage) window.scrollTo(0, 0);
};
document.addEventListener("DOMContentLoaded", () => markTarget(false));
document.addEventListener("htmx:afterSettle", (event) => markTarget(event.detail.xhr?.newPage === true));
