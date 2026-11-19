# The embedded Flux manifest

When a `Cluster` enables the flux feature, the operator creates the
Flux controllers in the `flux-system` namespace from
`gotk-components.yaml`, the pinned Flux install manifest. `flux.go`
embeds this directory in the operator binary, and the Makefile copies
`gotk-components.yaml` into it from the `flux` component's `dist/`. The copied file is a build
product, so git ignores it.

This README keeps the directory in the tree, so `go build` works
before the `flux` component has fetched anything. A binary built that
way has no `gotk-components.yaml`. When the flux feature is enabled,
the operator logs that the manifest is missing and creates no Flux
objects. Every shipped operator is built through the Makefiles, which
always copy the file.
