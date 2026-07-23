# The engine seed

The build copies `gotk-components.yaml` here from the flux domain's
`dist/`, and the operator binary embeds this whole directory
(`flux.go`). The file is a build product, so it is gitignored, and
this README keeps the directory present so a plain `go build` works
before the flux domain has fetched anything. A binary built that way
carries no seed, and the planter says so at runtime instead of
planting nothing silently. Every shipped operator builds through the
Makefiles, which always supply the seed.
