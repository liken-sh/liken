// Opens one page of each kind that transect serves, and checks that
// each page loads with no console error and no failed request, and
// that each page with an event stream opens it.
//
// The script first follows every link from / over plain HTTP, and it
// groups the URLs by the shape of their path, such as
// /namespaces/<namespace>/pods/<name>.  Then it opens the first URL of
// each group in Chromium.  For each page it writes a screenshot, and it
// adds a line to resolved.txt for each element with hx-get: the
// target, the swap, and the push-url that the element ends up with
// under the inheritance rules of the htmx version on the page.  Run it
// against the builds of the old pin and the new pin, then compare the
// two output directories with shotdiff.py and diff.
//
// Run it against a server on the fixture cluster:
//
//   node pages.mjs http://127.0.0.1:18432 <output directory>
//
// It prints one line for each kind of page, "ok" or "FAIL" with the
// errors under it, then a count.  It exits 1 when a page failed.
// browser.mjs says which environment variables it needs.

import fs from "node:fs";
import { args, launch } from "./browser.mjs";

const { base, out } = args("pages.mjs");
fs.mkdirSync(out, { recursive: true });

// The group of a URL: its path with each name replaced by a letter.
function kindOf(url) {
  const u = new URL(url, base);
  let p = u.pathname
    .replace(/^\/namespaces\/[^/]+/, "/namespaces/N")
    .replace(/^\/machines\/[^/]+/, "/machines/M")
    .replace(/^\/kinds\/[^/]+(\/[^/]+)?/, "/kinds/K")
    .replace(/^\/ground\/[^/]+/, "/ground/G")
    .replace(/^\/vulnerabilities\/[^/]+/, "/vulnerabilities/V")
    .replace(/^\/access\/accounts\/.+/, "/access/accounts/A")
    .replace(/\/elevations\/[^/]+$/, "/elevations/E");
  const segs = p.split("/");
  if (segs[1] === "namespaces" && segs.length > 3) {
    const rest = segs.slice(3);
    p = "/namespaces/N/" + rest[0] + (rest.length > 1 ? "/X" : "") + (rest.length > 2 ? "/" + rest.slice(2).join("/") : "");
  }
  if (u.search) p += "?" + [...u.searchParams.keys()].join("&");
  return p;
}

// Every URL that a link reaches from /, at most 600, and the first URL
// of each group.
async function crawl() {
  const seen = new Set(["/"]);
  const queue = ["/"];
  const kinds = new Map();
  while (queue.length && seen.size < 600) {
    const url = queue.shift();
    const res = await fetch(base + url);
    if (!res.ok) continue;
    const html = await res.text();
    const k = kindOf(url);
    if (!kinds.has(k)) kinds.set(k, url);
    for (const m of html.matchAll(/href="(\/[^"#]*)/g)) {
      const link = m[1].replaceAll("&amp;", "&");
      if (link.startsWith("/static/")) continue;
      if (!seen.has(link)) {
        seen.add(link);
        queue.push(link);
      }
    }
  }
  return { kinds, crawled: seen.size };
}

const { kinds, crawled } = await crawl();
const browser = await launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const report = [];
fs.writeFileSync(`${out}/resolved.txt`, "");
for (const [kind, url] of [...kinds].sort()) {
  const page = await context.newPage();
  const errors = [];
  page.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") errors.push(`console.${m.type()}: ${m.text()}`); });
  page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
  page.on("requestfailed", (r) => errors.push(`requestfailed: ${r.url()} ${r.failure()?.errorText}`));
  page.on("response", (r) => { if (r.status() >= 400) errors.push(`status ${r.status()}: ${r.url()}`); });
  const stream = page.waitForRequest((r) => r.url().includes("/events"), { timeout: 10000 }).catch(() => null);
  await page.goto(base + url, { waitUntil: "domcontentloaded", timeout: 15000 });
  // A page that sets hx-sse:connect (htmx 4) or sse-connect (htmx 2)
  // must open its stream.
  const streams = await page.evaluate(() => document.querySelector("[hx-sse\\:connect], [sse-connect]") !== null);
  if (streams && !(await stream)) errors.push("the page did not open its event stream");
  await page.waitForTimeout(1000);
  const resolved = await page.evaluate(() => {
    const four = String(window.htmx?.version || "").startsWith("4");
    const lines = [];
    for (const el of document.querySelectorAll("[hx-get]")) {
      const vals = ["target", "swap", "push-url"].map((a) => {
        const n = `hx-${a}`;
        if (!four) return el.closest(`[${CSS.escape(n)}]`)?.getAttribute(n) ?? "-";
        if (el.hasAttribute(n)) return el.getAttribute(n);
        const inherited = `${n}:inherited`;
        return el.closest(`[${CSS.escape(inherited)}]`)?.getAttribute(inherited) ?? "-";
      });
      lines.push(`${el.tagName.toLowerCase()}.${(el.getAttribute("class") || "").split(" ")[0]} ${el.getAttribute("hx-get")} => ${vals.join(" | ")}`);
    }
    return lines;
  });
  fs.appendFileSync(`${out}/resolved.txt`, resolved.map((l) => `${url} ${l}\n`).join(""));
  const name = kind.replace(/[^a-zA-Z0-9]+/g, "_").replace(/^_|_$/g, "") || "root";
  await page.screenshot({ path: `${out}/${name}.png` });
  report.push({ kind, url, errors });
  await page.close();
}
await browser.close();
fs.writeFileSync(`${out}/report.json`, JSON.stringify({ crawled, pages: report }, null, 1));
for (const r of report) {
  console.log(`${r.errors.length ? "FAIL" : "ok  "} ${r.kind}  ${r.url}`);
  for (const e of r.errors) console.log(`      ${e}`);
}
const failed = report.filter((r) => r.errors.length).length;
console.log(`${report.length} kinds of page from ${crawled} URLs, ${failed} failed`);
process.exit(failed ? 1 : 0);
