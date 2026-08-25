# The docs domain

This domain is the website: the front page of liken.sh and the
manual under /docs/. This document explains the content model, the
parallel output trees, the build, and how the site publishes.

## The content model

The manual has two halves, and each half gets its content in a
different way.

The guides are written here, by hand, in `content/docs/guides/`. A
reading order for a newcomer cannot be extracted from a repository,
because a person must decide what comes first. The guides follow
ASD-STE100, plain technical English: short sentences, one instruction
per sentence, no metaphor.

The reference for the two CRDs is generated, not written. The
schemas in `machine/manifests/machines-crd.yaml` and
`cluster/manifests/clusters-crd.yaml` describe every field, because
the schemas are written to be read. The `crdref` program reads each
schema and arranges those descriptions into a page, so the reference
stays the same as what the API server enforces. The generated pages
are gitignored, because they are build products.

The other reference pages are hand-written: the release channel, from
`releases/versioning.md`; the `liken` command, from the CLI's own usage
text; and the device reference, from the DRA driver in the machine
operator.

## The parallel trees

Every page is built twice: as HTML for people, and as the authored
Markdown for agents and scripts. The two land side by side, so the
Markdown twin of `/docs/guides/install/` is
`/docs/guides/install/index.md`. `hugo.yaml` declares the extra
output format, and the theme's `layouts/all.markdown.md` is its
whole template: the page's raw content.

The site root also serves the llms.txt convention
(<https://llmstxt.org>): `/llms.txt` is an index of the Markdown
twins, and `/llms-full.txt` is the whole manual in one file.

The Markdown twin is the authored file without changes. Each page
therefore has its own top-level heading in its content, and the
HTML templates add no heading. A title that is only in front matter
would not appear in the twin.

## The build

Hugo builds the HTML ahead of time, on the machine that runs the
build. Hugo is a tool dependency of this domain's own Go module:
`go.mod` pins the version, `go.sum` pins the digest of every module
in Hugo's graph, and `go tool hugo` compiles and runs it from the
module cache. The module is nested, apart from the repository's root
module, so Hugo's large dependency graph stays out of the root
`go.sum`. A release never redistributes the bytes of Hugo, so the
licensing domain has no entry for it. `latest.sh --bump` moves
the pin; build the site after a bump before trusting it, because a
Hugo release can change the template lookup rules the layouts depend
on.

The presentation is the brand theme, the git submodule at
`themes/brand` (<https://github.com/liken-sh/brand>). The theme
supplies the page shell, the shared stylesheet that every page
inlines, the nav that every liken site renders from its
`data/nav.yaml`, and the public brand files: `/favicon.ico`,
`/icon.svg`, and the mark under `/brand/`. The theme's `voice.md` is
the voice rules for everything the sites publish; `AGENTS.md` here
imports it. The built tree contains no JavaScript. The only layouts
kept in this site are the two llms.txt templates, because their
prose is about this manual, and Hugo gives a site's `layouts/`
precedence over the theme's. A fresh checkout needs
`git submodule update --init` before the site builds. To bump the
theme, check out the new commit inside `themes/brand` and commit the
moved submodule pointer.

    make -C docs build     build the site into dist/site/
    make -C docs serve     the authoring loop, with live reload
    make -C docs test      check the links and the generated reference

`dist/site/` is exactly what production serves, so a static file
server pointed at it is an exact preview.

## The deploy path

A push to main that changes this domain publishes the site. CI
builds `dist/site/` and deploys the tree to GitHub Pages, which
serves it at liken.sh. The workflow is
`.github/workflows/docs.yaml`.

The name reaches Pages through DNS: the apex records in
`liken.sh/terraform.tf` point liken.sh at GitHub's published Pages
addresses, and www CNAMEs to the Pages hostname. GitHub issues and
renews the site's TLS certificate. The Pages configuration itself
(the workflow source and the custom domain) is in the
repository's settings, set once by hand, the same class of one-time
act as delegating the zone.

`dist/site/release.txt` holds the commit that the site was built
from. CI reads it back over https://liken.sh/release.txt to prove
that the deploy landed.
