# Working on indi

This directory builds the INDI images: a pinned component, published
under `<version>-<revision>`, that CI builds only when that tag is new.
`README.md` explains the images, the shim, the versioning, and the
bump. Load the `releases` and `bump-components` skills under
`.agents/skills` before you change the version or the revision.

A change to anything that Docker sends the build, the `Dockerfile`,
the scripts, `images/`, or the shim's source, changes the recipe and
needs the next revision. That rebuilds all 17 images, so batch such
changes into one revision.

`shim/` is a Go module with its own tests: `make -C shim test`. The
smoke checks under `smoke/` need the images in the local Docker
daemon, built through `docker-bake.hcl` at the top of the repository.
