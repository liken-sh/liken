// A click with a modifier key opens a link in a new tab or window, as
// the browser does on its own.  htmx leaves that click alone on the
// link itself, but the click still bubbles to an ancestor's trigger,
// such as the cluster map's click that goes back, which would swap the
// page here as well.  Stopping the click while it is captured at the
// document keeps it from every htmx listener and leaves the browser's
// default.
document.addEventListener("click", (event) => {
  const modified = event.ctrlKey || event.metaKey || event.shiftKey;
  if (modified && event.target.closest("a[href]")) event.stopPropagation();
}, true);

// Every link swaps its page into the body, and an htmx swap leaves the
// window scrolled where it was, so a link clicked far down one page
// would open the next page as far down.  A request for another path is
// a new page, which opens at its top; a request for the same path, such
// as a selection or an edge toggle, keeps the reader's place.  Back and
// Forward request their page again, and htmx scrolls it back to where
// the reader left it.
document.addEventListener("htmx:before:request", (event) => {
  const { ctx } = event.detail;
  if (ctx.target !== document.body) return;
  const next = new URL(ctx.request.action, location.href);
  ctx.newPage = next.pathname !== location.pathname;
});

// An error with an empty body, such as the 500 of a page whose render
// failed, leaves the page as it is.  htmx 4 swaps every response except
// a 204 and a 304, so it would swap the empty body in, blank the page,
// and push the URL of the page that failed.  Cancelling the swap here
// also stops the push, because htmx pushes the URL after this event.
// An error with a body, such as the 404 page of an object that is gone,
// swaps in.
document.addEventListener("htmx:before:swap", (event) => {
  const { ctx } = event.detail;
  if (ctx.response?.status >= 400 && !ctx.text?.trim()) event.preventDefault();
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
document.addEventListener("htmx:after:swap", (event) => markTarget(event.detail.ctx.newPage === true));
