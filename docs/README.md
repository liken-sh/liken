# The docs domain

This domain is the website: the front page of liken.sh and the
manual under /docs/. This document explains the content model, the
parallel output trees, the build, and how the site reaches the
cluster that serves it.

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
output format, and `layouts/all.markdown.md` is its whole template:
the page's raw content.

The site root also serves the llms.txt convention
(<https://llmstxt.org>): `/llms.txt` is an index of the Markdown
twins, and `/llms-full.txt` is the whole manual in one file.

The Markdown twin is the authored file without changes. Each page
therefore carries its own top-level heading in its content, and the
HTML templates add no heading. A title that is only in front matter
would not appear in the twin.

## The build

Hugo builds the HTML ahead of time, on the machine that runs the
build. The pin is in `VERSION`, and `fetch.sh` downloads and
verifies the binary, the same arrangement every vendored domain
uses. A release never redistributes the bytes of Hugo, so the
licensing domain carries no entry for it.

There is no theme. The few files in `layouts/` are the whole
presentation, and the stylesheet inline in `layouts/baseof.html` is
the whole stylesheet. The built tree contains no JavaScript.

    make -C docs build     build the site into dist/site/
    make -C docs serve     the authoring loop, with live reload
    make -C docs test      test the crdref generator
    make -C docs image     bake dist/site/ into a local nginx image

To get an exact preview of production, build the image and run it:

    make -C docs image
    docker run --rm -p 8080:80 liken-website:dev

## The deploy path

A push to main that changes this domain publishes the site. CI
builds `dist/site/`, bakes it into the nginx image (`Dockerfile`),
pushes the image to ghcr.io, and points the website Deployment on
the liken.sh cluster at the new tag. The Deployment and its
credentials are in `liken.sh/website/manifests/`, and the workflow
is `.github/workflows/docs.yaml`.

`dist/site/release.txt` carries the commit that the site was built
from. CI reads it back over https://liken.sh/release.txt to prove
that the deploy landed.
