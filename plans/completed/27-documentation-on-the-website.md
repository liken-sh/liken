# Documentation on the website

Milestone 27 — Completed. The website serves a user manual at
liken.sh/docs/, built by Hugo and shipped as an nginx image.

The website meets people who do not yet read the repository. This
milestone gives them a user manual at liken.sh/docs/. The manual has
five guides (install, adopt an existing k3s cluster, add machines,
upgrade, and roll back) and a reference (the two CRDs, the release
channel, and the `liken` command). The docs domain at the repository
root owns the whole site, including the front page.

Part of the site is extracted from the repository's own text, and part
is written fresh. The CRD reference is extracted: a small program
(`docs/crdref`) reads the schemas in
`machine/manifests/machines-crd.yaml` and
`cluster/manifests/clusters-crd.yaml` and arranges their field
descriptions into pages, with linked types and nested headings. The
reference therefore cannot differ from what the API server enforces. The
guides are written fresh, because a reading order for a newcomer is a
decision and not an extraction. The guides use ASD-STE100, plain
technical English. AGENTS.md now tells every change to evaluate whether
the manual must change with it.

Every page builds twice: as HTML for people, and as the authored
Markdown beside it for agents (`/docs/guides/install/` and
`/docs/guides/install/index.md`). The site root serves `llms.txt` and
`llms-full.txt`, the convention that agents read first. Hugo builds the
HTML ahead of time, and Hugo is vendored with a digest-pinned fetch,
like every other vendored tool. The layouts are a few hand-written
templates with one inline stylesheet, and the built tree has no
JavaScript. Hugo is a build tool and is never redistributed, so the
licensing domain has no entry for it.

The site became too large for the ConfigMap that served the first page.
ConfigMap keys cannot hold nested paths, and each map has a limit of one
megabyte. The site now ships as an nginx image,
`ghcr.io/liken-sh/website`. `.github/workflows/docs.yaml` builds and
pushes the image on every push that touches the docs domain or a CRD
schema. The workflow then patches the website Deployment to the pushed
commit's tag and verifies the served site. The verification ends with
`release.txt`, which names the commit that the site was built from. The
deployer credential became narrower with this change: it can patch the
Deployment's image and watch the rollout, and nothing else.

Versioned documentation is deferred, on purpose. The manual documents
the latest release, which is also the only release that the project
supports, because every release takes over from the release before it.
If versioned docs become necessary, `release.txt` and the Markdown twins
are the hooks that a scheme can build on.
