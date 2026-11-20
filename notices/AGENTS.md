# Working on notices

`notices` is a set of shell scripts that the image builds of other
components run. `README.md` says what each script writes and why.

## Design

`go.sh` and `cargo.sh` run inside the build stage of every image
that ships a Go or a Rust program. So a change to one of them changes
every such image. To see the effect of a change, build one such image
and run its smoke check, which runs `check.sh` against the image in
the local Docker daemon. `.dockerignore` sends only `go.sh` and
`cargo.sh` to a build, so a change to `check.sh` or to the
documentation rebuilds no image.

The `images` skill describes the structure of each image that runs
these scripts.
