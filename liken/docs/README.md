# The docs domain

This domain builds the website: the front page of liken.sh and the
manual under /docs/.

## The content model

The guides are written by hand in `content/docs/guides/`, because a
person must decide the order in which a newcomer reads them. The
guides follow ASD-STE100, plain technical English: short sentences,
one instruction per sentence, no metaphor.

The reference pages of the four CRDs are generated. The `crdref`
program reads the field descriptions in
`machine/manifests/machines-crd.yaml`,
`cluster/manifests/clusters-crd.yaml`, and the files in
`disruption/manifests/`, and writes one page for each schema. So the
reference matches what the API server enforces, and you edit a field's
reference text in its schema. The generated pages are build products,
and git ignores them.

The other reference pages are written by hand. They include the
release channel, from `releases/versioning.md`, the `liken` command,
from the CLI's own usage text, and the device reference, from the DRA
driver in the machine operator.

## The parallel trees

Hugo builds every page twice: as HTML for people, and as the authored
Markdown for agents and scripts. The Markdown copy goes beside the
HTML, so the Markdown of `/docs/guides/install/` is
`/docs/guides/install/index.md`. `hugo.yaml` declares the extra output
format, and its template, the theme's `layouts/all.markdown.md`,
outputs the page's raw content.

The site root also serves the llms.txt convention
(<https://llmstxt.org>): `/llms.txt` is an index of the Markdown
pages, and `/llms-full.txt` is the whole manual in one file.

Because the Markdown output is the authored file unchanged, each page
writes its own top-level heading in its content, and the HTML
templates add no heading. A title that is only in front matter would
not appear in the Markdown.

## The build

Hugo builds the HTML ahead of time, on the machine that runs the
build. Hugo is a tool dependency of this domain's own Go module:
`go.mod` pins the version, `go.sum` pins the digest of every module
in Hugo's graph, and `go tool hugo` compiles and runs it from the
module cache. The module is separate from the repository's root
module, so Hugo's large dependency graph stays out of the root
`go.sum`. A release never redistributes Hugo, so the licensing domain
has no entry for it. `latest.sh --bump` moves the pin. After a bump,
build the site and check it, because a Hugo release can change the
rules that Hugo uses to find the templates of a page.

The presentation is the brand theme, `brand/` at the top of the
repository, and `hugo.yaml` sets `themesDir` to the repository root.
The theme supplies the page shell, the stylesheet that every page
inlines, the nav that every liken site renders from its
`data/nav.yaml`, and the public brand files: `/favicon.ico`,
`/icon.svg`, and the mark under `/brand/`. The theme's `voice.md` has
the voice rules for everything the sites publish, and `AGENTS.md` here
imports it. The built site has no JavaScript. This site keeps only
the two llms.txt templates in its own `layouts/`, because their text
is about this manual, and Hugo uses a site's `layouts/` before the
theme's. A change to the theme reaches this site in the same commit.

    make -C docs build     build the site into dist/site/
    make -C docs serve     the authoring loop, with live reload
    make -C docs test      check the links and the generated reference

`dist/site/` holds the same files that liken.sh serves for this
manual, so a static file server pointed at it gives an exact preview.

## The deploy path

liken.sh serves one tree that holds every component's manual: this
manual at the root, and each other component's manual under its
prefix, such as liken.sh/display/. On every push to `main`, the `site`
job in `.github/workflows/ci.yaml` builds that tree with `make site`
at the repository root and deploys it to GitHub Pages. Each manual
also serves its component's test coverage report at `coverage.html`
under its prefix.

The apex records in `liken.sh/terraform.tf`, at the repository root,
point liken.sh at GitHub's Pages addresses, and www is a CNAME to the
Pages hostname. GitHub issues and renews the site's TLS certificate.
The Pages configuration, the workflow source and the custom domain, is
in the repository's settings, and was set once by hand, like the
delegation of the zone.

`dist/site/release.txt` holds the commit that the site was built
from. After the deploy, CI reads https://liken.sh/release.txt until
it holds the commit of the push.

This build does not make the coverage report. `make site` at the
repository root runs `make coverage-report` in `liken/` and copies
the report into the site tree. So a local `make -C docs build` gives
a site without the report, and the theme's footer link to it is
broken in a local preview.
