# Working on notices

`notices` is a set of shell scripts that the image builds of other
components run. `README.md` says what each script writes and why.

## How it runs

`go.sh` and `cargo.sh` run inside a build stage of each image that
ships a Go or a Rust program. That build gets this directory as the
build context `notices`. `.dockerignore` sends only those two scripts
to the build, so a change to `check.sh` or to the documentation
rebuilds no image. `check.sh` runs outside every image. Each
component's smoke check runs it against an image in the local Docker
daemon.

## Design

The build scripts run inside the builds of other components. So a
change to one of them affects every image with a Go or a Rust
program. To see the effect of a change, build such an image and run
its smoke check.

The notices of Debian and Ubuntu packages are the job of the closure
library in `vulkan`, which writes them for every closure. `check.sh`
reads the list of packages that the library writes.

The `images` skill describes the structure of each image that runs
these scripts.
