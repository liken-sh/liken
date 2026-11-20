# notices

`notices` copies the license notices of the third-party software in
an image into that image. This is how each image that `liken`
publishes meets the conditions of the licenses of the software it
redistributes. The MIT, BSD, and Apache licenses require the
copyright notice and the license text to go with each copy of a
binary. A static Go or Rust binary contains neither. The GPL and the
LGPL also require the source to be available.

The scripts here write notices under `/usr/share/doc` in the image
for these parts of a program:

- the Go standard library and the Go modules that the program links
- the Rust standard library and the crates that the program links

A check proves that an image contains them. The code of this
repository is `liken`'s own, under its MIT license, so the scripts
leave it out.

These scripts do not write the notices of Debian and Ubuntu packages.
The `notices` function of `vulkan/closure.sh`, the closure library,
writes those for every closure. It also writes the list of packages
and the `deb-src` entries that download their sources.

Every image that ships a Go or a Rust program uses this directory as
the build context `notices`, and lists `notices` in the `[depends]`
of its `package.toml`. Those images are the operators, the CSI
drivers, the kubectl plugins, and the screen clients. Each script
writes to a directory that the final stage of the build copies whole,
as in `COPY --from=build /notices /`.

## The scripts

- `go.sh` runs in a Go build stage after the build, with the packages
  that the build compiled. It copies the license files of the Go
  standard library to `/usr/share/doc/go/`, and the license files of
  each module that the program links to `/usr/share/doc/<module>/`.
  When a module has no license file, the build fails. The script
  leaves out the main module and each module that `go.mod` replaces
  with a directory of the repository.
- `cargo.sh` runs in a Rust build stage. It must run in the same
  `RUN` as the build, because only that `RUN` mounts the crate
  registry. It copies the notices of the standard library to
  `/usr/share/doc/rust/`, and the license files of each crate that
  the program links to `/usr/share/doc/<crate>-<version>/`. When a
  crate publishes no license file, the script copies its `Cargo.toml`
  there instead, because that file names the license and the authors.
  The script leaves out each crate that the build uses by path.
- `check.sh <image> [<path>...]` reads an image from the local Docker
  daemon. It fails in two cases: when a package listed in a file in
  `/usr/share/doc/liken/` has no copyright file in the image, and when
  a path given after the image name is missing. Each component's
  smoke check runs it with the files that its image must contain,
  such as `usr/share/doc/go/LICENSE` for an image with a Go program.

The usage of each script is in the comment at its top.

The INDI images also contain the license file of each vendor SDK in
them, copied from `indi/sdk-licenses/`. They need these files because
the copyright file of the `indi-3rdparty-libs` package from the INDI
PPA does not mention any of the SDKs.

MIT. See [LICENSE](LICENSE).
