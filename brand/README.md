# liken brand

This directory is the brand component of
[liken](https://github.com/liken-sh/liken): the mark, the shared
stylesheet, the Hugo theme, and the voice rules. Every `liken` site
uses the theme, and every word those sites publish follows the
voice rules.

Each kind of program reads this directory in its own way:

* **Hugo sites** set `themesDir: ../..` and `theme: brand` in
  `docs/hugo.yaml`, so Hugo finds this directory at the repository
  root. The theme is the page layout (`layouts/`), the
  files every site serves (`static/`), and the entries of the top
  navigation (`data/nav.yaml`).
* **Go programs** that build pages outside Hugo import it as the
  module `github.com/liken-sh/liken/brand`, with a `replace` directive to
  this directory. The package embeds the
  stylesheet and the mark, so a page builder such as the release
  channel's can inline them without reading files at run time.
* **Rust programs** that draw with the `iced` toolkit name `iced/` as
  a path dependency, and their image builds take this directory as a
  second build context named `brand`. The crate
  parses the mark and the palette out of `liken.svg` and
  `liken.css`, so a screen and a page draw the same shape in the
  same colors.

`voice.md` states the voice and tone rules for every word the
project publishes, on the sites and in the comments of the source
files. The root `AGENTS.md` imports it with the line
`@brand/voice.md`, and each component's `AGENTS.md` imports it by its
path from that component, so every agent session loads it. Other
files in this directory hold rules that only some work needs, and
`voice.md` links to each:
[`messages.md`](messages.md) for errors, log lines, the messages of
conditions and `Event`s, and test failures,
[`names.md`](names.md) for identifiers, tests, and names in the API,
and [`instructions.md`](instructions.md) for `AGENTS.md` files,
skills, and briefs for agents. No `AGENTS.md` imports them, so a
session has one of them only when it opens the file.

[`editing-prompt.md`](editing-prompt.md) is the brief that a session
gives to an agent that corrects prose, messages, or names in one part
of the repository. It says how to keep every fact through an edit,
which names and texts other code depends on (the agent reports
these and does not change them), and what the agent returns.

## Using the theme

Declare the theme in the site's `hugo.yaml`, together with the
Markdown output format:

```yaml
themesDir: ../..
theme: brand

params:
  # The repository this site documents. The nav's "repository"
  # entry links here, and the sidebar's heading is the project
  # name, the last path segment of the URL.
  repository: https://github.com/liken-sh/liken

outputFormats:
  markdown:
    mediaType: text/markdown
    baseName: index
    isPlainText: true

outputs:
  home: [html, markdown]
  section: [html, markdown]
  page: [html, markdown]
```

The theme writes every page twice: as HTML for people, and as the
source Markdown for agents and scripts. Both files go in the same
directory, so the Markdown version of `/docs/guides/install/` is
`/docs/guides/install/index.md`, and every HTML page's footer links
to its Markdown version. The `outputFormats` and `outputs` settings above
turn the Markdown version on. Without them, the site builds HTML only.

The theme's `static/` tree merges into the site's output, so every
site serves the same URLs: `/favicon.ico` and `/icon.svg` for
browsers, and `/brand/liken.svg` and `/brand/liken.png` for anyone
who needs the mark at a stable address. The stylesheet has no URL,
because the layout inlines it into every page. "The stylesheet"
below gives the reason.

`data/nav.yaml` lists the entries of the top nav, in order. Every
site shows the same labels from this file. "liken", "operators",
"releases", and "devlog" have absolute URLs, so they go to the same
page from every site, and you change one of them in this file only.
"repository" links to the URL that the site sets in
`params.repository`. A site that does not set `params.repository`
shows no "repository" entry.

The nav has no entry for the site's manual, because the sidebar
shows it. When a site has a `/docs` section, every page, the front
page included, shows the manual's page tree in a sidebar. The
sidebar appears only on wide viewports. On narrow viewports the
breadcrumbs show where the page is. The sidebar's heading is the
project name, the last path segment of `params.repository`:
`liken` on liken.sh, `bluetooth-operator` on the bluetooth
operator's site. Without the parameter, the heading is the word
"Manual". A site with no `/docs` section shows no sidebar.

A site can replace any layout of the theme with its own file of the
same name, because Hugo prefers a site's `layouts/` over the theme's.
A site can also add layouts that the theme does not have, as liken.sh
does for its `llms.txt` outputs.

Do not copy `baseof.html` to change one part of the page. It is
split into partials under `layouts/_partials/`, each with a default,
and a site overrides only the partial it changes:

* `head.html` is empty in the theme. A site uses it to add style
  rules or `link` elements at the end of the `head`.
* `nav.html` is the top nav, built from `data/nav.yaml`.
* `aside.html` is the sidebar. The default shows the manual's page
  tree, and the page gets the two-column manual layout whenever this
  partial renders anything.
* `crumbs.html` is the breadcrumb trail.
* `footer.html` is the footer.

The devlog overrides `head.html`, `aside.html`, `crumbs.html`, and
`footer.html`, and does not copy `baseof.html`.

# The mark

`liken`'s icon is a patch of lichen, drawn as hexagonal tiles.

![The liken mark](liken.svg)

The mark refers to lichen biology, and to the way `liken` combines
Linux and Kubernetes.

A lichen is a fungus and a photosynthetic partner, an alga or a
cyanobacterium, living so closely that the pair is named and
classified as one lichen. `liken` combines the Linux kernel and `k3s`,
which remain separate upstream projects, and a machine boots them
as one system.

Lichens are also pioneers. They are among the first living things to
take hold on bare rock. They survive drought, heat, and bare mineral
where nothing else grows, and they begin to turn rock into soil.
`liken` starts from a blank machine with nothing installed.

A lichen also needs very little to live. `liken` runs a Kubernetes
cluster on machines with 1 GB of memory.

## The tiles

Many crustose lichens grow flat against rock. As they age, and as
repeated wetting and drying shrinks the crust, the surface cracks into
small polygonal plates. Each plate is called an *areole*, and a
thallus built this way is *areolate*. The icon draws that mosaic, one
areole to a tile.

The areoles are drawn as hexagons because the Kubernetes community
uses hexagons, in Helm's logo and in the backdrops of community
talks. So the same picture refers to lichen on rock and to
Kubernetes.

One tile is orange and the rest are green. *Xanthoria*, one of the
most common rock lichens, is a vivid orange, and the single orange
tile gives the mark a focal point.

The tiles get smaller toward one edge of the mark. This is a design
choice, not biology. Real areoles do not reliably
shrink toward the margin, because cracking tends to start in the
older center. The pattern suggests a colony that is still spreading
into bare rock.

## The colors

The greens come from crustose lichens on stone, from deep moss to
pale sage. The orange tile comes from *Xanthoria*.

| Swatch | Hex | Name |
| --- | --- | --- |
| Deep moss | `#4a5d3a` | the darkest green |
| Mid sage | `#6e8352` | the middle green |
| Light sage | `#93a877` | |
| Pale sage | `#b4c49a` | the lightest green |
| Xanthoria | `#e0872f` | the orange tile |

The mark has a transparent background, so it works on both light
and dark surfaces. Every tile is one flat color with no gradient or
effect, so the mark stays legible at favicon size and would print
in one ink.

## The stylesheet

`liken.css` is the stylesheet that every `liken` site and
releases.liken.sh share. It sets the colors, the type, and the few
elements that prose and reference tables need. It has a light and a
dark scheme, chosen by the reader's system setting, and its accent
color is one of the greens above. A second accent, `--warm`, comes
from the orange tile: the tile's orange in the dark scheme, and a
darker shade of it in the light scheme. The theme uses `--warm` for
the badge of an HTTP method that changes state. The manual's sidebar
is styled in `layouts/baseof.html`, and the release channel's digest
column in `liken/releases/page.html.tmpl`.

No page loads the stylesheet over the network. Every page inlines
it. The release channel stores its pages in object storage
outside the cluster, because machines upgrade themselves from that
channel and must be able to reach it when the cluster is down. A
stylesheet fetched from liken.sh would make the website a
dependency of that upgrade path.

The two consumers read the file differently. The Hugo theme inlines
the committed copy in `assets/`. The release channel's pages read
the original through the `brand` Go package, because a Go program
can embed only files from its own module. Edit only
`brand/liken.css`, and run `make` to write the copy in `assets/`.

## The coverage report

`coverage/` is a Go program that turns the coverage data from a
component's tests into one HTML page. A component pins it as a tool
dependency of its docs module, the way it pins Hugo, and runs it
as `go tool coverage`:

```sh
go tool coverage -title "media-operator" -label Go -label Rust \
  -out coverage.html -root .. go.out cobertura.xml
```

An input is a Go coverage profile (`go test -coverprofile`) or a
Cobertura report (`cargo llvm-cov --cobertura`). The program detects
the format from the content of each file, so the files can have any
name, and one page can report coverage for a
Go program and a Rust program. For Go the page counts statements,
the same unit that `go-test-coverage` counts, so the page and the
coverage gate show the same total. For Cobertura it counts lines.
The page states the unit of every number.

The page is one file. It inlines the shared stylesheet, requests
nothing over the network, and needs no JavaScript, so a site can
publish it and a release can ship it. It starts with a summary of
each input, then a table of every file, then each source file with
numbered lines, colored to show which lines the tests ran and which
they did not.

A site that publishes the report serves it at `/coverage.html` and
sets this parameter:

```yaml
params:
  coverage: true
```

Every page's footer then links to the report. A site that does not
set the parameter has no link.

## The route reference generator

`apiref/` is a Go program that turns an API's OpenAPI 3.1 document
into one Markdown page. A component runs it as `go tool apiref`,
pinned as a tool dependency of its docs module the way `crdref` is:

```sh
go tool apiref -title Routes -weight 55 \
  -postamble apiref/routes-postamble.md \
  openapi.json content/docs/reference/routes.md apiref/routes-preamble.md
```

The page starts with the document's own summary, then has one
section per operation, headed with the method and the path. Each
section lists the parameters, the responses, and the fields of each
response. After the operations come the component schemas, as the
same field tables that `crdref` writes for a CRD, and the
credentials the document declares. The page is plain Markdown with
no Swagger UI and no JavaScript, and every section is a heading
that the manual's link check can resolve.

A route's heading is written like this:

```markdown
## `GET` `/v1/widget/widgets/{name}` {data-method=GET}
```

The attribute tells the theme the method, and the theme draws it as
a badge. Goldmark removes the attribute before it computes the
heading's id, and backticks add nothing to an id, so the id is the
same as for the plain heading `GET /v1/widget/widgets/{name}`, and
Hugo's table of contents lists the route. A theme
that draws no badge shows the heading as the method and the path.

The preamble and the postamble are hand-written files that the
generator copies unchanged to the top and the bottom of the page.
They hold what an OpenAPI document has no place for, such as what
the API is for and a link to the manual page that explains it in
prose. `plans/completed/02-openapi-as-a-page.md` is the design.

## The skills generator

`skills/` is a Go program that turns a site's guides into
[Agent Skills](https://agentskills.io/), one `SKILL.md` per guide. A
component runs it as `go tool skills`, pinned as a tool dependency
of its docs module the way `crdref` is:

```sh
go tool skills -base https://liken.sh/media/ content/docs/guides ../skills
```

Every guide's front matter needs a `description` that says what the
guide does and ends with when an agent should use it. The generator
copies the description, drops the title and the weight, turns every
link into a full URL under `-base`, and starts the skill with the
guide's URL and an instruction to confirm the `kubectl` context.
Edit the guide, never the skill. The generator replaces the whole
output directory, so a renamed or deleted guide leaves no stale
skill.

Each component commits its `skills/` directory, so a checkout
includes the skills, and CI fails when they are out of date. An agent loads
them with `claude --plugin-dir <component directory>` or
`npx skills add https://github.com/liken-sh/liken/tree/main/<component>`.
`plans/completed/01-guides-as-skills.md` is the design.

## The `liken-iced` crate

`iced/` is a Rust crate, `liken-iced`, for a program that draws the
brand with the [`iced`](https://iced.rs/) toolkit, such as an idle
screen on a television. The crate provides:

* **The mark.** It embeds `liken.svg` and parses the fourteen
  polygons out of it. A caller reads the six vertices, the fill, and
  the stroke width of each hexagon, in the SVG's own coordinate space
  with its bounding box, and one call draws the whole mark into a
  canvas frame at any center and any width.
* **The palette.** It embeds `liken.css` and parses `--ink`,
  `--ink-muted`, `--page`, and `--link` out of both schemes.
* **The pulse.** `motion.md` describes the animation, and the crate
  defines its numbers. Each hexagon's size follows the average of
  two sine waves. The rate of the first is between 0.22 and 0.40
  cycles a second, chosen from the hexagon's index with the golden
  ratio. The caller passes an energy from 0 to 1. At 1 the size
  changes by up to ten percent, and at 0 the mark does not move. The
  caller also passes the phase, the animation clock in seconds, and
  slows the pulse at low energy by advancing the phase more slowly.
* **The fonts.** `fonts/` contains Source Sans 3, the typeface of
  every `liken` page and screen, as two static files for programs
  (upright and italic) and two variable files for the web. The crate
  embeds the two static files and loads them into the font system
  of `iced` when the program asks, so a screen draws the typeface
  from its own binary and does not depend on the fonts installed on
  the machine.

The crate copies no value from `liken.svg` or `liken.css` into its
source. It embeds both files when it compiles, so an edit to either
file reaches the crate with no second edit.

The crate defines no type scale, margins, or layout. Those belong to
the program that draws the mark, because a television screen and a
web page need different ones.

`iced/Cargo.toml` pins an exact version of `iced_widget`, and each
program that uses the crate pins the same version. Two versions of
`iced` in one binary do not compile, so a bump changes every one of
those manifests in one commit.

`make test-rust` runs the crate's checks: formatting, lints, and the
tests with a minimum line coverage. It needs the Rust toolchain that
`iced/rust-toolchain.toml` pins, and `cargo-llvm-cov`.

## The files

`liken.svg` is the original, and every other image in this list is
made from it. `make` writes the other files, and the repository
commits them too, so anyone can take a favicon or an avatar without
installing a rasterizer, and a site that uses the theme serves them
with no build step:

* `liken.svg`: the original, for any use at any size.
* `favicon.ico`: a raster image at 16, 32, and 48 pixels, for the
  browser tab, for browsers that cannot use the SVG file that the
  sites also serve.
* `liken.png`: a transparent raster image of 1024 pixels. Use it for
  a GitHub organization avatar or anywhere else that needs a raster
  image.
* `static/` and `assets/`: the copies that the theme serves, at the
  URLs that the pages link to. The comments in the `Makefile`
  explain each one.

To rebuild these files you need `rsvg-convert` (from librsvg) and
ImageMagick. Edit `liken.svg`, run `make`, and commit the files that
change.

## Sources

The biology in this document comes from standard lichenology sources:

* Irwin M. Brodo, Sylvia Duran Sharnoff, and Stephen Sharnoff,
  *Lichens of North America* (Yale University Press, 2001), the
  standard field reference for the symbiosis and for growth forms.
* [British Lichen Society: Lichen
  Morphology](https://britishlichensociety.org.uk/learning/lichen-morphology),
  for areoles and the areolate crustose thallus.
* [Crustose lichen](https://en.wikipedia.org/wiki/Crustose_lichen) and
  [Lichen](https://en.wikipedia.org/wiki/Lichen), Wikipedia, for the
  mycobiont/photobiont symbiosis and the pioneer role in primary
  succession, both with citations to the primary literature.
