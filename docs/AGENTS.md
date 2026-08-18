# Writing in the docs domain

@themes/brand/voice.md

Everything this domain publishes follows those rules and ASD-STE100:
short sentences, one instruction per sentence, no metaphor. Scan a
page against the voice rules before you publish it.

The guides in `content/docs/guides/` are written by hand. The
reference pages for the two CRDs are generated at build time: crdref
reads the schemas in `machine/manifests/machines-crd.yaml` and
`cluster/manifests/clusters-crd.yaml`, so the schema descriptions are
where those pages are edited. `README.md` explains the content model,
the build, and the deploy path.
