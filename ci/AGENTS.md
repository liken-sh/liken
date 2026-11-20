# Working on ci

`ci/` is a Go program in `package main`. `README.md` explains what it
is for and describes its commands.

## How it runs

The generated workflows run `ci`. Read `.github/workflows/ci.yaml`,
`scan.yaml`, and a component's workflow to see which job runs which
command. The `plan` job runs `plan` and `bases`, and every later job
of the run uses their outputs. Every command reads the tree, and some
also query ghcr.io and the release channel. Only `publish` and
`record` write outside the tree.

## Design

The workflows, `.github/dependabot.yml`, and `docker-bake.hcl` are
generated. Their sources are each component's `package.toml`, the
templates in `templates/`, and the generator's code. To change a
generated file, change one of those sources and run `make workflows`
at the top of the repository. Then commit the source and the
generated files together.

The workflows are static files, not a matrix that a script computes.
So GitHub draws the dependency graph of each run, and a skipped job
shows which component did not change.

A pinned component's published tag is never overwritten. `bases`
treats the tag as current when the tag's recipe label equals the
recipe of the tree. The recipe is a hash of the files that the
component's images are built from, and it does not cover the
generator. So when you change the generator in a way that changes
what a build makes, or that changes the labels that `publish` writes
on a pinned image, raise the recipe format. A new recipe format
changes the recipe of every pinned component.

## Finding things

- The comment at `recipeFormat` in `recipe.go` says when to raise it.
  A test in `imagemeta_test.go` fails when the labels of a pinned
  image change and the format does not.
- Root plan 69 describes the design: what a tag releases, and which
  change runs which job.
- When a component's `[depends]` leaves out a dependency, the
  component does not rebuild when that dependency changes. So `deps`
  fails when `[depends]` leaves out a dependency that `go list -deps`,
  `cargo metadata`, or an image's `FROM` lines and bake contexts find.
- The `releases` skill describes how an image is published, and the
  `sbom-and-scans` skill describes how it is signed and verified.
