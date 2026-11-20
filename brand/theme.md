# The Hugo theme

Every `liken` site builds with this directory as its Hugo theme, so
each manual has the same page layout, the same top nav, and the same
public brand files. A site declares the theme in its `hugo.yaml`,
together with the Markdown output format:

```yaml
themesDir: ../..
theme: brand

params:
  # The repository this site documents. The nav's "repository"
  # entry links here, and the sidebar's heading names the project,
  # the last path segment of the URL.
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

When the site is in a component's `docs/` directory, `themesDir: ../..`
points to the top of the repository. So Hugo reads the theme from the
same commit as the site.

## Pages as HTML and as Markdown

The theme writes every page twice: as HTML for people, and as the
source Markdown for agents and scripts. Both files go in the same
directory, so the Markdown version of `/docs/guides/install/` is
`/docs/guides/install/index.md`, and every HTML page's footer links
to its Markdown version. The `outputFormats` and `outputs` settings
above turn the Markdown version on. Without them, the site builds
HTML only.

## The files every site serves

The theme's `static/` tree merges into the site's output, so every
site serves the same URLs: `/favicon.ico` and `/icon.svg` for
browsers, and `/brand/liken.svg` and `/brand/liken.png` for anyone
who needs the mark at a stable address. [`mark.md`](mark.md) says
how `make` writes these files from `liken.svg`.

## The stylesheet

`liken.css` is the stylesheet that every `liken` site and
releases.liken.sh share. It sets the colors, the type, and the few
elements that prose and reference tables need, in a light and a dark
scheme that the reader's system setting chooses. Its header comment
says where each color comes from.

Every page includes the stylesheet inline, so the stylesheet has no
URL. The reason is the release channel. Machines upgrade themselves
from it, and they must reach it when the cluster is down, so the
channel stores its pages in object storage outside the cluster. If
those pages loaded a stylesheet from liken.sh, an upgrade would
depend on the website.

The Hugo theme inlines the copy in `assets/`. The pages of the release
channel read the original through the `brand` Go package, because a
Go program can embed only files from its own module. Edit only
`liken.css`, and run `make` to write the copy in `assets/`. Put a
layout rule for one site in that site. For example, the manual's
sidebar is styled in `layouts/baseof.html`, and the digest column of
the release channel in `liken/releases/page.html.tmpl`.

## The nav and the sidebar

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
breadcrumbs show where the page is. The heading of the sidebar is the
name of the project, which is the last path segment of
`params.repository`: "`liken` manual" on liken.sh, and
"`bluetooth-operator` manual" on the site of `bluetooth-operator`.
Without the parameter, the heading is the word "Manual". A site with
no `/docs` section shows no sidebar.

## The coverage link

A site that publishes the test coverage report of its component
serves it at `/coverage.html` and sets this parameter:

```yaml
params:
  coverage: true
```

Every page's footer then links to the report. A site that does not
set the parameter has no link. `coverage/main.go` describes the
report.

## Changing one part of a page

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

Each heading of a route reference page has its HTTP method in a
`data-method` attribute. The theme draws the method as a badge, in
`--warm` for a method that changes state. `apiref/main.go`
describes the heading.
