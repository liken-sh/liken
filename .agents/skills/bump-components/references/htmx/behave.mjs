// Drives what htmx does on transect's pages, and checks each result:
// a link swaps the page and pushes its URL, Back and Forward restore a
// page with its title and its scroll position, the cluster map's
// Escape and click go back, the event stream replaces the view and
// opens however late the SSE extension arrives, quick search shows
// rows and opens one, a server error or a proxy's error page leaves
// the page alone, and a click with ctrl opens a new tab.  The checks expect the fixture cluster of `--fixture`,
// because they name its namespaces, such as acme-shop.
//
// Run it against a server on the fixture cluster:
//
//   node behave.mjs http://127.0.0.1:18432 <output directory>
//
// It prints PASS or FAIL for each check, then each console error and
// failed request, then a count.  It exits 1 when a check failed or
// the browser reported an error.  browser.mjs says which environment
// variables it needs.

import fs from "node:fs";
import { args, launch } from "./browser.mjs";

const { base, out } = args("behave.mjs");
fs.mkdirSync(out, { recursive: true });
const browser = await launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await context.newPage();
const errors = [];
const requests = [];
function watch(p) {
  p.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") errors.push(`console.${m.type()}: ${m.text()}`); });
  p.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
  p.on("response", (r) => { if (r.status() >= 400) errors.push(`status ${r.status()}: ${r.url()}`); });
  p.on("request", (r) => requests.push({ url: r.url().replace(base, ""), headers: r.headers() }));
}
watch(page);

const results = [];
function check(name, ok, detail = "") {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? "  (" + detail + ")" : ""}`);
}
const settle = (p = page, ms = 800) => p.waitForTimeout(ms);
const here = () => { const u = new URL(page.url()); return u.pathname + u.search; };
// A full page load starts a new window, which drops this marker, so a
// marker that is still set shows that htmx swapped the page.
const mark = () => page.evaluate(() => { window.noFullLoad = true; });
const marked = () => page.evaluate(() => window.noFullLoad === true);
const dialogOpen = () => page.evaluate(() => document.getElementById("search")?.open === true);

// The cluster map: a tile selects its namespace, and Escape or a click
// away from a tile goes back.
await page.goto(base + "/", { waitUntil: "domcontentloaded" });
await settle(page, 1500);
await mark();
const tile = page.locator("a.ns-tile").first();
const tileHref = await tile.getAttribute("href");
await tile.click();
await settle();
check("a tile pushes its URL", here() === tileHref, `${here()}, want ${tileHref}`);
check("a tile swaps the page without a full load", await marked());
check("a tile selects its namespace", (await page.locator(".ns-tile-selected").count()) === 1);
await page.keyboard.press("Escape");
await settle();
check("Escape goes back to the plain map", here() === "/", here());
await tile.click();
await settle();
await page.locator("main.cluster .band-label").first().click();
await settle();
check("a click away from a tile goes back to the plain map", here() === "/", here());
await tile.click();
await settle();
const selected = here();
const other = page.locator("a.ns-tile").nth(1);
const otherHref = await other.getAttribute("href");
await other.click();
await settle();
check("a click on another tile selects that tile", here() === otherHref, `${here()}, want ${otherHref}`);

// Back and Forward between two selections.
await page.goBack();
await settle(page, 1200);
const selectedNamespace = new URL(selected, base).searchParams.get("select");
check("Back restores the URL", here() === selected, `${here()}, want ${selected}`);
check("Back restores the selection and the title", (await page.title()).startsWith(selectedNamespace) && (await page.locator(".ns-tile-selected").count()) === 1, await page.title());
check("Back makes no full load", await marked());
await page.goForward();
await settle(page, 1200);
check("Forward restores the next URL", here() === otherHref, here());

// A new page opens at its top, and Back restores the page before it.
await page.goto(base + "/", { waitUntil: "domcontentloaded" });
await settle(page, 1500);
await mark();
const machine = page.locator("a[href^='/machines/']").first();
const machinePath = (await machine.getAttribute("href")).split("#")[0];
await page.evaluate(() => window.scrollTo(0, 400));
await machine.click();
await settle();
check("a machine link pushes its URL", new URL(page.url()).pathname === machinePath, page.url());
check("a new page opens at its top", (await page.evaluate(() => window.scrollY)) === 0, `scrollY ${await page.evaluate(() => window.scrollY)}`);
await page.screenshot({ path: `${out}/machine.png` });
await page.locator("a.masthead-link[href='/kinds']").click();
await settle();
check("the masthead's kinds link opens /kinds with its title", new URL(page.url()).pathname === "/kinds" && (await page.title()).startsWith("kinds"), await page.title());
check("the masthead's link swaps the page without a full load", await marked());
await page.goBack();
await settle(page, 1200);
check("Back restores the machine page and its title", new URL(page.url()).pathname === machinePath && !(await page.title()).startsWith("kinds"), await page.title());

// Back restores the scroll position.  A short window makes the page
// long enough to scroll.
await page.setViewportSize({ width: 1440, height: 400 });
await page.goto(base + "/namespaces/acme-shop", { waitUntil: "domcontentloaded" });
await settle(page, 1500);
await page.evaluate(() => window.scrollTo(0, 300));
await settle(page, 300);
const before = await page.evaluate(() => window.scrollY);
await page.locator("main a[href^='/namespaces/acme-shop/deployments/']").last().click();
await settle();
const opened = await page.evaluate(() => window.scrollY);
await page.goBack();
await settle(page, 1500);
const after = await page.evaluate(() => window.scrollY);
check("Back restores where the reader left the page", before > 0 && opened === 0 && Math.abs(after - before) < 5, `scrollY ${before}, then ${opened} on the new page, then ${after} after Back`);
await page.setViewportSize({ width: 1440, height: 900 });

// The event stream.  The fixture cluster never changes, so the check
// changes the view in the browser and makes the stream connect again:
// the server sends the current view first on each new stream.  A
// hidden tab closes its stream and opens it again when it shows.  The
// check runs in a tab of its own and closes it, because a full load
// away from a stream that connected again logs an aborted request.
{
  const tab = await context.newPage();
  watch(tab);
  await tab.goto(base + "/", { waitUntil: "domcontentloaded" });
  await settle(tab, 2000);
  const streamsAt = (n) => requests.filter((r) => r.url.startsWith("/events")).length - n;
  const opened = streamsAt(0);
  check("the page opens its event stream", opened > 0, requests.filter((r) => r.url.startsWith("/events")).at(-1)?.url);
  await tab.evaluate(() => {
    const view = document.getElementById("view");
    view.dataset.probe = "changed";
    view.querySelector(".band-name").textContent = "CHANGED";
  });
  const visibility = (hidden) => tab.evaluate((hidden) => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (hidden ? "hidden" : "visible") });
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
  await visibility(true);
  await settle(tab, 500);
  await visibility(false);
  await tab.waitForFunction(() => !document.getElementById("view")?.dataset.probe, null, { timeout: 8000 }).catch(() => {});
  const replaced = await tab.evaluate(() => !document.getElementById("view").dataset.probe && document.querySelector(".band-name").textContent !== "CHANGED");
  check("a tab that shows again receives the current view from its stream", replaced, `new streams: ${streamsAt(opened)}`);
  await tab.close();
}

// Quick search.
await page.goto(base + "/?select=acme-shop", { waitUntil: "domcontentloaded" });
await settle(page, 1500);
await page.keyboard.press("/");
await settle(page, 300);
const opens = await dialogOpen();
await page.keyboard.press("Escape");
await settle();
check("Escape closes the dialog and keeps the selection", opens && !(await dialogOpen()) && here() === "/?select=acme-shop", here());
await page.goto(base + "/", { waitUntil: "domcontentloaded" });
await settle(page, 1500);
await mark();
await page.keyboard.press("/");
await page.keyboard.type("store");
await settle(page, 1000);
const rows = await page.locator("#search .search-row").count();
const asked = requests.filter((r) => r.url.startsWith("/search")).at(-1);
check("typing shows rows in the dialog", rows > 0, `${rows} rows`);
check("the request names the dialog's list in HX-Target", (asked?.headers["hx-target"] ?? "").endsWith("search-results"), `HX-Target: ${asked?.headers["hx-target"]}`);
check("the dialog receives rows, not a page", (await page.locator("#search .search-page, #search .masthead").count()) === 0);
await page.keyboard.press("Enter");
await settle();
check("Enter opens the first row and closes the dialog", !(await dialogOpen()) && here() !== "/", here());
check("the row's page swaps in without a full load", await marked());
await page.goBack();
await settle(page, 1200);
check("Back leaves the dialog closed", !(await dialogOpen()));
await page.keyboard.press("/");
await page.keyboard.type("acme-led", { delay: 5 });
await page.keyboard.press("Enter");
await settle(page, 1200);
check("Enter before the rows arrive opens the first row of the last query", here().includes("ledger"), here());

// The /search page's own field.
await page.goto(base + "/search?q=acme", { waitUntil: "domcontentloaded" });
await settle(page, 1000);
const first = await page.locator(".search-page .search-row").count();
await page.locator(".search-page .search-input").fill("ledger");
await settle(page, 1000);
const second = await page.locator(".search-page .search-row").count();
check("the search page's field replaces its rows", second > 0 && second !== first, `${first} rows, then ${second}`);
check("the search page keeps one masthead", (await page.locator(".masthead").count()) === 1);

// An error with an empty body, such as the 500 of a page whose render
// failed, leaves the page and its URL as they are.  The check answers a
// tile's request with that error, in a tab whose expected error the
// count of browser errors leaves out.
{
  const tab = await context.newPage();
  await tab.goto(base + "/", { waitUntil: "domcontentloaded" });
  await settle(tab, 1500);
  const tiles = tab.locator("a.ns-tile");
  const href = await tiles.first().getAttribute("href");
  await tab.route(`**${href}`, (route) => route.fulfill({ status: 500, body: "" }));
  await tiles.first().click();
  await settle(tab, 1000);
  const url = new URL(tab.url());
  check("an error with an empty body leaves the page and its URL", url.pathname + url.search === "/" && (await tiles.count()) > 0, `${url.pathname + url.search}, ${await tiles.count()} tiles`);
  await tab.close();
}

// An error page from a proxy, such as the 502 of an ingress while the
// pod restarts, also leaves the page and its URL as they are.
{
  const tab = await context.newPage();
  await tab.goto(base + "/", { waitUntil: "domcontentloaded" });
  await settle(tab, 1500);
  const title = await tab.title();
  const tiles = tab.locator("a.ns-tile");
  const href = await tiles.first().getAttribute("href");
  const body = "<html><head><title>502 Bad Gateway</title></head><body><h1>502 Bad Gateway</h1></body></html>";
  await tab.route(`**${href}`, (route) => route.fulfill({ status: 502, contentType: "text/html", body }));
  await tiles.first().click();
  await settle(tab, 1000);
  const url = new URL(tab.url());
  check("a proxy's error page leaves the page, its URL, and its title", url.pathname + url.search === "/" && (await tab.title()) === title && (await tab.locator(".masthead").count()) === 1, `${url.pathname + url.search}, title ${await tab.title()}`);
  await tab.close();
}

// A link to an object that is gone opens transect's 404 page of it,
// which has a body.  The link is added to the page, because the
// fixture's pages link only to objects that exist.
{
  const tab = await context.newPage();
  await tab.goto(base + "/namespaces/acme-shop", { waitUntil: "domcontentloaded" });
  await settle(tab, 1500);
  await tab.evaluate(() => {
    const link = document.createElement("a");
    link.id = "gone";
    link.href = "/namespaces/acme-shop/deployments/no-such-deployment";
    link.setAttribute("hx-get", link.getAttribute("href"));
    link.textContent = "gone";
    document.querySelector("main").prepend(link);
    htmx.process(link);
  });
  await tab.locator("#gone").click();
  await settle(tab, 1000);
  check("a link to an object that is gone opens its 404 page", new URL(tab.url()).pathname.endsWith("/no-such-deployment") && (await tab.locator("main.gone").count()) === 1, `${new URL(tab.url()).pathname}, title ${await tab.title()}`);
  await tab.close();
}

// The page opens its event stream however late htmx's SSE extension
// arrives.  The check delays the response of the extension's file, so
// htmx itself is ready first.
{
  const tab = await context.newPage();
  await tab.route("**/static/*sse*.min.js", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 300));
    await route.continue();
  });
  const stream = tab.waitForRequest((r) => r.url().includes("/events"), { timeout: 5000 }).catch(() => null);
  await tab.goto(base + "/", { waitUntil: "domcontentloaded" });
  check("the page opens its event stream when the SSE extension arrives 300 ms late", (await stream) !== null);
  await tab.close();
}

// A click with ctrl opens a new tab and leaves this page as it is, on a
// tile and on a link inside the map of a selected namespace, whose own
// click would go back.
async function ctrlClick(url, selector, name) {
  await page.goto(base + url, { waitUntil: "domcontentloaded" });
  await settle(page, 1500);
  const [tab] = await Promise.all([
    context.waitForEvent("page", { timeout: 4000 }).catch(() => null),
    page.locator(selector).first().click({ modifiers: ["Control"] }),
  ]);
  await settle();
  check(name, here() === url && tab !== null, `${here()}, new tab ${tab?.url()}`);
  if (tab) await tab.close();
}
await ctrlClick("/", "a.ns-tile", "ctrl and a click on a tile open a new tab");
await ctrlClick("/?select=acme-shop", "main.cluster a[href^='/machines/']", "ctrl and a click on a link in the map keep the selection");

await browser.close();
for (const e of errors) console.log(`ERROR ${e}`);
const failed = results.filter((r) => !r.ok).length;
console.log(`${results.length} checks, ${failed} failed, ${errors.length} browser errors`);
fs.writeFileSync(`${out}/behave.json`, JSON.stringify({ results, errors }, null, 1));
process.exit(failed || errors.length ? 1 : 0);
