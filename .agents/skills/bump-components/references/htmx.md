# htmx in transect

`transect` serves htmx to the browser from
`transect/crates/transect/src/web/static/`, and the binary embeds the
files with `include_bytes!`. Each file comes from the npm package
`htmx.org`:

- `htmx.min.js` is `dist/htmx.min.js`.
- `hx-sse.min.js` is `dist/ext/hx-sse.min.js`, the extension that
  streams each page's server-sent events into the page.
- `htmx.LICENSE` is the package's `LICENSE`.

## The report and the bump

`latest.sh` in that directory prints the `htmx` row of
`make versions`. htmx.org publishes releases under more than one npm
dist-tag, and both `latest` and `next` have named the newest major
version at some time. So the script follows the dist-tag whose release
is the newest that is not a pre-release, and prints that tag as the
channel and the newest channel.

Run the bump from the top of the repository:

```sh
transect/crates/transect/src/web/static/latest.sh --bump
```

The bump downloads the package's tarball from the npm registry and
checks it against the sha512 `integrity` that the registry gives for
the release. Then it writes each of the files above, `htmx.sha256`,
and the `version`, `url`, `sha256`, and `purl` of the `[htmx]` and
`[hx-sse]` entries of `pin.toml`. Its `next:` line, which you run from
the script's directory, says to update the version in the comments of
`../assets.rs` and to run `make -C ../../../../.. test`, the checks of
`transect`. A bump within one major version needs nothing else.

## A new major version is a port

`latest.sh --bump` stops when the newest release has another major
version than the pin. A new major version of htmx changes the
attributes, the events, and the extensions that the templates and the
scripts use, and `make test` does not run htmx. So port the code
first, then run `latest.sh --bump --major`. Check each of these
against the new version's migration guide and its `CHANGELOG.md`, and
against the source of `dist/htmx.js` and `dist/ext/hx-sse.js` in the
tarball, because the guide does not list every change:

- **Each `hx-` attribute that the templates use.** List them with
  `git grep -n -o -h -E 'hx-[a-z-]+(:[a-z:-]+)?' -- transect ':!*.min.js' ':!transect/plans' | sort | uniq -c`.
  Check whether an attribute was renamed or removed, and how an
  attribute reaches the elements inside the element that sets it. In
  htmx 4, a `main` or an `aside` sets `hx-target:inherited`,
  `hx-swap:inherited`, and `hx-push-url:inherited` for its links.
- **The trigger expressions.** `cluster.html` and the search fields
  use `hx-trigger` with filters, `from:`, `changed`, and `delay:`, and
  the search fields use `hx-sync`.
- **The event stream.** Each page sets `hx-sse:connect` on the `.page`
  element in `transect-frame/src/frame/layout.html`, and
  `transect/src/web/events.rs` writes the events. Check the
  extension's attributes, whether an event with a name swaps or
  dispatches a DOM event, and what the extension does while a tab is
  hidden.
- **The scripts and the events they listen for.**
  `transect/src/web/links.js` and `transect-map/src/search/search.js`
  listen for htmx events and read their `detail`. An event can change
  its name, its `detail`, or the moment that it fires.
- **The request headers that the server reads.** `search_route.rs`
  reads `HX-Target`.
- **What htmx swaps.** Check which response statuses swap, and how
  Back and Forward restore a page. htmx 4 swaps every response except
  a 204 and a 304, so `links.js` cancels the swap of an error with an
  empty body.

## The proof

Run the checks of `transect` first:

```sh
make -C transect test
```

No test runs htmx, so then drive the pages in a headless Chromium with
the scripts in [`htmx/`](htmx/). The repository pins no copy of
Playwright, so install `playwright-core` 1.65.1 into a scratch
directory. `npx playwright-core install chromium` downloads the
Chromium that this version drives, revision 1255 (Chrome for Testing
159.0.8154.6), into `~/.cache/ms-playwright/chromium-1255/`, and its
headless shell beside it. `CHROMIUM` names the first. Run these
commands from the top of the repository:

```sh
scratch="$(mktemp -d)"
npm install --prefix "$scratch" playwright-core@1.65.1
(cd "$scratch" && npx playwright-core install chromium)
export PLAYWRIGHT_CORE="$scratch/node_modules/playwright-core"
export CHROMIUM="$(node --input-type=module -e "import { chromium } from '$PLAYWRIGHT_CORE/index.mjs'; console.log(chromium.executablePath())")"
```

Build the new pin and start it on the fixture cluster, on a free
loopback port. The server keeps running, so start it in a second
terminal or in the background:

```sh
(cd transect && cargo build && target/debug/transect --fixture --listen 127.0.0.1:18432)
```

Run each script against it:

```sh
node .agents/skills/bump-components/references/htmx/pages.mjs http://127.0.0.1:18432 "$scratch/new"
node .agents/skills/bump-components/references/htmx/behave.mjs http://127.0.0.1:18432 "$scratch/new-behave"
```

Each script exits 0 on a pass. `pages.mjs` ends with
`... kinds of page from ... URLs, 0 failed`, and `behave.mjs` ends with
`... checks, 0 failed, 0 browser errors`. Above that line, each script
prints `FAIL` and the errors for each page or check that failed. The
checks cover these:

1. **`pages.mjs`:** each kind of page that a link from `/` reaches
   loads with no console error and no failed request, and opens its
   event stream when it sets `hx-sse:connect`.
2. **`behave.mjs`:** a tile and a link push their URL without a full
   load; Escape and a click away from a tile go back; Back and Forward
   restore the URL, the selection, the title, and the scroll position;
   a tab that shows again receives the current view from its stream;
   quick search shows rows, sends `HX-Target`, and opens a row with
   Enter; the search page's field replaces its rows; an error with an
   empty body leaves the page as it is; and a click with ctrl opens a
   new tab.

Then run the same scripts against a build of the old pin, and compare.
A debug build reads its static files from its own source tree, so the
old build runs from a worktree of its own. Before the bump is
committed, `origin/main` has the old pin:

```sh
git worktree add --detach .worktrees/htmx-old origin/main
(cd .worktrees/htmx-old/transect && cargo build && target/debug/transect --fixture --listen 127.0.0.1:18431)
node .agents/skills/bump-components/references/htmx/pages.mjs http://127.0.0.1:18431 "$scratch/old"
node .agents/skills/bump-components/references/htmx/behave.mjs http://127.0.0.1:18431 "$scratch/old-behave"
diff <(sort "$scratch/old/resolved.txt") <(sort "$scratch/new/resolved.txt")
python3 .agents/skills/bump-components/references/htmx/shotdiff.py "$scratch/old" "$scratch/new" "$scratch/diff"
```

The `diff` prints nothing when every link has the same target, swap,
and push-url under both versions. `shotdiff.py` writes an image of
each difference, and a difference in a small box is usually a clock,
such as "read at 16:57:58 UTC" on a logs page.

Stop each server and each browser by the PID that a separate `ps`
listing gives, because a `pkill -f` pattern also matches the shell
that runs it. Then remove the old worktree with
`git worktree remove .worktrees/htmx-old`.
