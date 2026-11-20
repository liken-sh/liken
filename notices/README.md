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
of its `package.toml`, such as each operator and each CSI driver.
Each script
writes to a directory that the final stage of the build copies whole,
as in `COPY --from=build /notices /`.

## The scripts

- `go.sh` runs in a Go build stage after the build, and writes the
  license files of the Go standard library and of each module that the
  program links. When a module has no license file, the build fails.
- `cargo.sh` runs in a Rust build stage, in the same `RUN` as the
  build, because only that `RUN` mounts the crate registry. It writes
  the license files of the Rust standard library and of each crate
  that the program links. When a crate publishes no license file, the
  script copies its `Cargo.toml`, because that file names the license
  and the authors.
- `check.sh` reads an image from the local Docker daemon, and fails
  when a notice that the image must contain is missing. Each
  component's smoke check runs it.

The comment at the top of each script gives its usage, the paths it
writes, and what it leaves out.

The INDI images also contain the license file of each vendor SDK in
them, copied from `indi/sdk-licenses/`. They need these files because
the copyright file of the `indi-3rdparty-libs` package from the INDI
PPA does not mention any of the SDKs.

MIT. See [LICENSE](LICENSE).
