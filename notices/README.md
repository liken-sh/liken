# notices

Every image that `liken` publishes holds third-party software: the Go
standard library and the Go modules that an operator links, the Rust
crates that a screen client links, and the files of Debian packages
that a closure copies. Each license of that software permits
redistribution on conditions. The MIT, BSD, and Apache licenses
require that the copyright notice and the license text go with each
copy. The GPL and the LGPL also require that the source be available.
This directory holds the scripts that put those notices into each
image, under `/usr/share/doc`, and the check that proves they are
there.

## The scripts

The images of the operators take this directory as the build context
`notices`, and each one names `notices` in the `[depends]` of its
`package.toml`.

- `go.sh` runs in a Go build stage after the build. It copies the
  license files of the Go standard library to `/usr/share/doc/go/`,
  and those of each module that the binary links to
  `/usr/share/doc/<module>/`. A module with no license file fails the
  build.
- `cargo.sh` runs in a Rust build stage, in the same `RUN` as the
  build, because only that `RUN` mounts the crate registry. It copies
  the standard library's notices to `/usr/share/doc/rust/`, and the
  license files of each crate that the binary links to
  `/usr/share/doc/<crate>-<version>/`. A crate that publishes no
  license file has its `Cargo.toml` there instead, which names the
  license and the authors.
- `debian.sh` runs in a closure's Debian builder after the closure. It
  copies the copyright file of each package that the closure took a
  file from to `/usr/share/doc/<package>/copyright`, and the license
  texts of `/usr/share/common-licenses`. It writes
  `/usr/share/doc/liken/<image>.packages`, which names each package,
  its version, and its source package. snapshot.debian.org serves the
  source of every version that Debian published.

Each script writes to a directory that the final stage copies whole,
as in `COPY --from=build /notices /`.

Code of this repository is `liken`'s own, under its MIT license, and
the scripts leave it out: the module itself, the modules that `go.mod`
replaces with a directory of the repository, and the crates that a
build takes by path.

## The pinned components

The bases, `vulkan` and the four on it, and the INDI images run the
steps of `debian.sh` from their own `closure.sh`, in a function named
`notices`. A pinned component's recipe covers every file of its build
contexts, so a base that read this directory would need a new revision
for each change here. The INDI images also carry the license file of
each vendor SDK in them, from `indi/sdk-licenses/`, because the
copyright file of the PPA's `indi-3rdparty-libs` names none of the
SDKs.

## The check

`check.sh <image> [<path>...]` reads the image from the local Docker
daemon. It fails when a package that a list in
`/usr/share/doc/liken/` names has no copyright file in the image, or
when a path named after the image is missing. Each component's smoke
check runs it with the files that its image must hold, such as
`usr/share/doc/go/LICENSE` for an image with a Go binary.
