# Documentation on the website

Milestone 27 — Done

The website meets people who do not yet read the repository. This
milestone gave them a user manual at liken.sh/docs/: five guides
(install, adopt an existing k3s cluster, add machines, upgrade, roll
back) and a reference (the two CRDs, the release channel, the `liken`
command). The docs domain at the repository root owns the whole site,
including the front page.

The original sketch of this milestone asked whether the site could be
extracted from the repository's own text. The answer split in two.
The CRD reference is extracted: a small program (`docs/crdref`) walks
the schemas in `machine/manifests/machines-crd.yaml` and
`cluster/manifests/clusters-crd.yaml` and arranges their own field
descriptions into pages, with linked types and nested headings, so
the reference cannot drift from what the API server enforces. The
guides are written fresh, because a reading order for a newcomer is a
decision, not an extraction. They are written in ASD-STE100, plain
technical English, and AGENTS.md now tells every change to evaluate
whether the manual must change with it.

Every page builds twice: as HTML for people, and as the authored
Markdown beside it for agents (`/docs/guides/install/` and
`/docs/guides/install/index.md`). The site root serves `llms.txt` and
`llms-full.txt`, the convention agents check first. Hugo builds the
HTML ahead of time, vendored with a digest-pinned fetch like every
other vendored tool; the layouts are a few hand-written templates
with one inline stylesheet, and the built tree carries no JavaScript.
Hugo is a build tool, never redistributed, so the licensing domain
carries no entry for it.

The site outgrew the ConfigMap that served the first page: ConfigMap
keys cannot hold nested paths, and each map caps at one megabyte. The
site now ships as an nginx image, `ghcr.io/liken-sh/website`, built
and pushed by `.github/workflows/docs.yaml` on every push that
touches the docs domain or a CRD schema. The workflow then patches
the website Deployment to the pushed commit's tag and verifies the
served site, ending with `release.txt`, which carries the commit the
site was built from. The deployer credential shrank with the change:
it can patch the Deployment's image and watch the rollout, and
nothing else.

Versioned documentation is deferred, deliberately. The manual
documents the latest release, which is also the only release the
project supports: every release expects to take over from the one
before it. If versioned docs become necessary, `release.txt` and the
Markdown twins are the hooks a scheme would build on.
