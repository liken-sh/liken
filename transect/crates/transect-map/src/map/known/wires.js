// The wires of a sketch.  The browser lays the sketch's grid out and
// sizes each part to its text, so the server cannot know where a part
// stands.  The server writes each wire with the runs it draws, each run
// with its two areas, their edges, and each end's place along its edge,
// and this script measures the marks of the parts' ports and writes each
// path.  A run leaves its port at a right angle and bends halfway
// between the two areas, so it meets the port wherever the part stands
// in its area.  A role with several parts gets a bar along its area's
// edge, with a short line from the bar to each part's port, under the
// parts.  When a run is the only one at each of its ports and the two
// spans overlap, it runs straight in the overlap, so two parts that
// stand on one floor have no step between them.
//
// A wire is hidden until it is placed, and the list of wires shows until
// then, so a page without this script still names every wire.  The
// script draws each sketch that the page swaps in, before the browser
// paints it, and again when a sketch changes size.

// Where an end at `slot` of `of` meets the span of its port, as a share
// of the span: the middle for one end, and evenly from one end of the
// span to the other for several.
const share = (slot, of) => (of > 1 ? slot / (of - 1) : 0.5);

const side = (edge) => edge === "left" || edge === "right";

// The point on the port mark `rect` of a part's `edge` for an end at
// `t` along the span, relative to `origin`.
const onPort = (rect, edge, t, origin) =>
  side(edge)
    ? { x: rect.left + rect.width / 2 - origin.left, y: rect.top + rect.height * t - origin.top }
    : { x: rect.left + rect.width * t - origin.left, y: rect.top + rect.height / 2 - origin.top };

// One end of a run: the point where it ends, the rectangle of its area,
// its span when it is the only end at one part's port, and the bar and
// its short lines when the role has several parts.
// The plan's own areas are its grid's children, so an area of the same
// name in an elevation that a part draws inside the plan is not found.
const end = (plan, origin, name, edge, slot, of) => {
  const area = plan.querySelector(`:scope > .sketch-grid > [data-area="${CSS.escape(name)}"]`);
  if (!area) return null;
  const marks = [...area.querySelectorAll(`:scope > .shape-part .shape-port-${edge}`)];
  if (marks.length === 0) return null;
  const t = share(slot, of);
  const ports = marks.map((mark) => onPort(mark.getBoundingClientRect(), edge, t, origin));
  const box = area.getBoundingClientRect();
  const rect = {
    left: box.left - origin.left,
    right: box.right - origin.left,
    top: box.top - origin.top,
    bottom: box.bottom - origin.top,
  };
  if (ports.length === 1) {
    const mark = marks[0].getBoundingClientRect();
    const span = side(edge)
      ? [mark.top - origin.top, mark.bottom - origin.top]
      : [mark.left - origin.left, mark.right - origin.left];
    return { point: ports[0], rect, bars: "", span: of === 1 ? span : null };
  }
  const at = rect[edge];
  const along = ports.map((p) => (side(edge) ? p.y : p.x));
  const [low, high] = [Math.min(...along), Math.max(...along)];
  const middle = (low + high) / 2;
  const point = side(edge) ? { x: at, y: middle } : { x: middle, y: at };
  const bar = side(edge) ? `M${at} ${low}V${high}` : `M${low} ${at}H${high}`;
  const stubs = ports.map((p) => (side(edge) ? `M${at} ${p.y}H${p.x}` : `M${p.x} ${at}V${p.y}`));
  return { point, rect, bars: [bar, ...stubs].join(""), span: null };
};

// Moves both ends of a run into the overlap of their spans, when each is
// the only end at its port and the spans overlap.
const straighten = (a, b, across) => {
  if (!a.span || !b.span) return;
  const low = Math.max(a.span[0], b.span[0]);
  const high = Math.min(a.span[1], b.span[1]);
  if (low > high) return;
  const at = (low + high) / 2;
  if (across) [a.point.y, b.point.y] = [at, at];
  else [a.point.x, b.point.x] = [at, at];
};

// The points of the run from `a` to `b`: straight out of each port, with
// the bend halfway between the two areas, or one straight segment when
// the two ports line up.
const points = (a, b, edge) => {
  const [p, q] = [a.point, b.point];
  if (side(edge) ? p.y === q.y : p.x === q.x) return [p, q];
  if (side(edge)) {
    const mid = edge === "right" ? (a.rect.right + b.rect.left) / 2 : (a.rect.left + b.rect.right) / 2;
    return [p, { x: mid, y: p.y }, { x: mid, y: q.y }, q];
  }
  const mid = edge === "bottom" ? (a.rect.bottom + b.rect.top) / 2 : (a.rect.top + b.rect.bottom) / 2;
  return [p, { x: p.x, y: mid }, { x: q.x, y: mid }, q];
};

const path = (points) => points.map((p, at) => `${at ? "L" : "M"}${p.x} ${p.y}`).join("");

// The middle of the longest horizontal segment of `segments`, where the
// label stands, above the line, and the width it has: the segment less
// 8 pixels at each end, so the label never reaches a bend.  A wire with
// no horizontal segment has its label beside its first segment.
const labelAt = (segments) => {
  const flat = segments.filter(([p, q]) => p.y === q.y);
  const length = ([p, q]) => Math.abs(q.x - p.x);
  const longest = flat.sort((a, b) => length(b) - length(a))[0];
  if (!longest) {
    const [p, q] = segments[0];
    return { x: p.x + 8, y: (p.y + q.y) / 2, anchor: "start", width: Infinity };
  }
  const [p, q] = longest;
  return { x: (p.x + q.x) / 2, y: p.y - 8, anchor: "middle", width: length(longest) - 16 };
};

// Writes `words` into `text` as lines no wider than `width`, broken at
// spaces, with the last line on `y`, so a label stacks upward from the
// wire.  It returns the number of lines.
const wrap = (text, words, x, y, width) => {
  text.textContent = "";
  const lines = [];
  for (const word of words.split(" ")) {
    const line = lines.length ? `${lines[lines.length - 1]} ${word}` : word;
    text.textContent = line;
    if (lines.length && text.getComputedTextLength() > width) lines.push(word);
    else if (lines.length) lines[lines.length - 1] = line;
    else lines.push(word);
  }
  text.textContent = "";
  lines.forEach((line, at) => {
    const span = document.createElementNS("http://www.w3.org/2000/svg", "tspan");
    span.setAttribute("x", x);
    span.setAttribute("y", y - (lines.length - 1 - at) * 15);
    span.textContent = line;
    text.appendChild(span);
  });
  return lines.length;
};

const draw = (plan) => {
  const svg = plan.querySelector(":scope > .sketch-wires");
  if (!svg) return;
  const origin = plan.getBoundingClientRect();
  svg.setAttribute("width", origin.width);
  svg.setAttribute("height", origin.height);
  svg.setAttribute("viewBox", `0 0 ${origin.width} ${origin.height}`);
  for (const wire of svg.querySelectorAll(".sketch-wire")) {
    const segments = [];
    for (const run of wire.querySelectorAll(".sketch-run")) {
      const data = run.dataset;
      const a = end(plan, origin, data.from, data.fromEdge, +data.fromSlot, +data.fromOf);
      const b = end(plan, origin, data.to, data.toEdge, +data.toSlot, +data.toOf);
      if (!a || !b) continue;
      straighten(a, b, side(data.fromEdge));
      const drawn = points(a, b, data.fromEdge);
      run.querySelector(".sketch-wire-path").setAttribute("d", path(drawn));
      run.querySelector(".sketch-wire-hit").setAttribute("d", path(drawn));
      run.querySelector(".sketch-wire-bars").setAttribute("d", a.bars + b.bars);
      for (let at = 1; at < drawn.length; at += 1) segments.push([drawn[at - 1], drawn[at]]);
    }
    if (segments.length === 0) continue;
    const at = labelAt(segments);
    const label = wire.querySelector(".sketch-wire-label");
    label.dataset.words ??= label.textContent;
    label.setAttribute("text-anchor", at.anchor);
    wrap(label, label.dataset.words, at.x, at.y, at.width);
    const source = wire.querySelector(".sketch-wire-source");
    source.setAttribute("x", at.x);
    source.setAttribute("y", at.y + 26);
    source.setAttribute("text-anchor", at.anchor);
    wire.removeAttribute("visibility");
  }
  plan.closest(".sketch")?.classList.add("sketch-drawn");
};

// A sketch changes size when the window does, when a part's text wraps
// again, and when the fonts load.
const watched = new ResizeObserver((entries) => entries.forEach((entry) => draw(entry.target)));

const drawIn = (element) => {
  const plans = element.matches(".sketch-plan") ? [element] : element.querySelectorAll(".sketch-plan");
  plans.forEach((plan) => {
    draw(plan);
    watched.observe(plan);
  });
};

// The page swaps content in from links and from its event stream.  A
// mutation observer runs before the browser paints the new content, so
// each new sketch is drawn before it shows, whatever swapped it in.
new MutationObserver((records) => {
  for (const record of records) {
    for (const node of record.addedNodes) if (node instanceof Element) drawIn(node);
  }
}).observe(document.documentElement, { childList: true, subtree: true });
document.addEventListener("DOMContentLoaded", () => drawIn(document.body));
document.fonts?.ready.then(() => drawIn(document.body));
