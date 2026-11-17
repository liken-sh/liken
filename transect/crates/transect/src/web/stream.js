// Each open page holds one event stream, and a browser keeps at most six
// HTTP/1.1 connections to one host, so a few tabs left open would stall
// every page load.  A hidden tab closes its stream, and when it shows
// again it fetches its page whole, which opens a new stream and shows
// what changed while the tab was hidden.
const streams = new Set();
const open = htmx.createEventSource;
htmx.createEventSource = (url) => {
  const stream = open(url);
  streams.add(stream);
  return stream;
};

let closed = false;
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") {
    closed = streams.size > 0;
    for (const stream of streams) stream.close();
    streams.clear();
  } else if (closed) {
    closed = false;
    htmx.ajax("GET", location.pathname + location.search, { target: "body", swap: "innerHTML" });
  }
});
