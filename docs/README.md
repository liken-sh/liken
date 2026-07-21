# The docs domain

This domain is the website: the front page of liken.sh and the
manual under /docs/. This document explains the content model, the
parallel output trees, the build, and how the site reaches the
cluster that serves it.

## The content model

The manual has two halves, and they get their words in two different
ways.

The guides are written here, by hand, in `content/docs/guides/`.
They are written fresh because a reading order for a newcomer cannot
be extracted from a repository: someone has to decide what comes
first. They follow ASD-STE100, plain technical English: short
sentences, one instruction per sentence, no metaphor.

The reference for the two CRDs is generated, never written. The
schemas in `machine/manifests/machines-crd.yaml` and
`cluster/manifests/clusters-crd.yaml` already describe every field,
because the schemas are written to be read. The `crdref` program
walks each schema and arranges those descriptions into a page, so
the reference cannot drift from what the API server enforces. The
generated pages are gitignored: they are build products.

The remaining reference pages (the release channel, the `liken`
command) are hand-written, from `releases/versioning.md` and the
CLI's own usage text.

## The parallel trees

Every page is built twice: as HTML for people, and as the authored
Markdown for agents and scripts. The two land side by side, so the
Markdown twin of `/docs/guides/install/` is
`/docs/guides/install/index.md`. `hugo.yaml` declares the extra
output format, and `layouts/all.markdown.md` is its whole template:
the page's raw content.

The site root also serves the llms.txt convention
(<https://llmstxt.org>): `/llms.txt` is an index of the Markdown
twins, and `/llms-full.txt` is the whole manual in one file.

Because the Markdown twin is the authored file verbatim, each page
carries its own top-level heading in its content, and the HTML
templates add no heading of their own. A title that lived only in
front matter would vanish from the twin.

## The build

Hugo builds the HTML, ahead of time, on the machine that runs the
build. The pin lives in `VERSION` and `fetch.sh` downloads and
verifies the binary, the same arrangement every vendored domain
uses. Hugo is a build tool in the same sense a compiler is: a
release never redistributes its bytes, so the licensing domain
carries no entry for it.

There is no theme. The few files in `layouts/` are the whole
presentation, and the stylesheet inline in `layouts/baseof.html` is
the whole stylesheet. The built tree contains no JavaScript.

    make -C docs build     build the site into dist/site/
    make -C docs serve     the authoring loop, with live reload
    make -C docs test      test the crdref generator
    make -C docs image     bake dist/site/ into a local nginx image

For an exact production preview, build the image and run it:

    make -C docs image
    docker run --rm -p 8080:80 liken-website:dev

## The deploy path

A push to main that touches this domain publishes the site. CI
builds `dist/site/`, bakes it into the nginx image (`Dockerfile`),
pushes the image to ghcr.io, and points the website Deployment on
the liken.sh cluster at the new tag. The Deployment and its
credentials live in `liken.sh/website/manifests/`, and the workflow
is `.github/workflows/docs.yaml`.

`dist/site/release.txt` carries the commit the site was built from.
CI reads it back over https://liken.sh/release.txt to prove the
deploy landed.
