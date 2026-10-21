# Authoring this docs domain

@../../brand/voice.md

Everything this domain publishes follows the voice rules in that file and
ASD-STE100: short sentences, one instruction per sentence, no metaphor.
Scan a page against the rules before you publish it.

The guides in `content/docs/guides/` are written by hand. The reference
pages for the four CRDs are generated at build time: `crdref` reads the
schemas in `machine/manifests/machines-crd.yaml`,
`cluster/manifests/clusters-crd.yaml`, and the two files in
`disruption/manifests/`, so the schema descriptions are where those
pages are edited. `README.md` explains the content model, the
build, and the deploy path.
