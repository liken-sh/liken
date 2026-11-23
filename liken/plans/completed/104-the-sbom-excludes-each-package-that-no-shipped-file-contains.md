# 104. The SBOM excludes each package that no shipped file contains

Milestone 104. Proposed and built on 2026-11-23. A userland build on
a workstation and the SBOM of a lab release made from it proved it
([As built](#as-built)). No release has published with it yet, so
code scanning has not yet read an SBOM that it wrote.

The SBOM of an OS release states that the release ships every package
that buildroot built for the target. buildroot builds each package
that a selected recipe depends on, and its `utils/generate-cyclonedx`
gives the scope `excluded` only to a host package. So a library that
only the build used is in the SBOM as a part of the release.
`nfs-utils.mk` depends on `libevent` and `sqlite` in every
configuration. In nfs-utils, only the NFS daemons link them, and a
release ships only the static `mount.nfs`, which links neither. Code
scanning reported CVE-2026-63380 against `libevent` 2.1.13 for a
release that does not contain libevent, as the root open problem
[what a vulnerability finding does to a build](../../../plans/open-problems/what-a-vulnerability-finding-does-to-a-build.md)
records.

A list of the packages to exclude, kept by hand, was written and
rejected before it landed. The root open problem
[some SBOM components name the wrong thing](../../../plans/open-problems/some-sbom-components-name-the-wrong-thing.md)
says that the records come from `pin.toml` and from buildroot's SBOM,
never from a list kept by hand. A list is also wrong without a sign
when a recipe starts to link a library that the list names.

## The design

The linker knows what each program contains, so the build keeps the
linker's record, and the merge reads it.

1. **The link map.** `liken/userland/os.config` sets
   `BR2_TARGET_LDFLAGS="-Wl,-Map=$(BASE_DIR)/maps/"`. When `-Map`
   names a directory, GNU ld writes the map into it under the name of
   the output file with `.map` appended. The ld manual of binutils
   2.46.1, the version that the buildroot pin builds, gives this rule
   under `-Map=mapfile`. It has been in ld since 2.36. `build.sh`
   makes the directory before the build, because ld does not. Only
   the OS build sets the flag, so `bluetoothd.config` and the
   bluetoothd image do not change.
2. **The evidence in `dist/`.** `liken/userland/evidence.sh` runs in
   the build container after the build. For each program that a
   release ships, it copies two parts of the program's map into
   `dist/evidence/maps/<program>.map`. The section "Archive member
   included to satisfy reference by file (symbol)" names each archive
   member that the link took, and each `LOAD` line names a file on
   the link's command line, such as musl's `crt1.o`. The rest of a
   map gives section addresses. evidence.sh also copies buildroot's
   `packages-file-list.txt`, `packages-file-list-staging.txt`, and
   `packages-file-list-host.txt`, which name the package that
   installed each file of the target, staging, and host trees.
   `build.sh` writes `dist/evidence/shipped`, with a line for each
   file that it ships and the path in buildroot's output that it
   copied the file from. A program with no map stops the build, so a
   recipe that does not pass `LDFLAGS` to its link cannot ship a
   program with no evidence. The key of `artifact.sh` covers every
   file of `liken/userland/` except the bluetoothd files, so
   `evidence.sh` and the `os.config` change are in it, and a build
   from the registry includes the evidence.
3. **The rule in Go.** `containedPackages` in
   `liken/releases/sbom/contained.go` computes the packages that the
   release contains:
   - the package that installed each shipped file;
   - for each archive member that a shipped program's map names, the
     package that owns the archive;
   - for each object file of a `LOAD` line, the package that owns it.

   An archive under a package's build directory belongs to that
   package. A relative path belongs to the program's own package,
   because buildroot runs each package's build in its build directory.
   A file that several packages installed belongs to the package that
   depends, directly or through others, on each of the others, because
   buildroot installs a package after its dependencies. When no
   package depends on all of the others, the merge stops.
   A `LOAD` line of an archive is not evidence, because ld writes one
   for each archive on the command line whether or not the link took a
   member. The merge gives each package that the release contains no
   scope, which CycloneDX reads as `required`, and every other
   buildroot package the scope `excluded`.
4. **Refusals.** The merge stops when a map names a file that no
   package installed, when a shipped program has no map, and when a
   shipped file has no package. Evidence that the merge cannot explain
   never passes as "not shipped".

### Details that the build showed

The first build showed these facts, and the design follows them:

- **The archive section ends at an empty line.** The ld of binutils
  2.46.1 writes the sections "Merging object attributes" and "Merging
  program properties" after the archive section. Their lines name
  every object of the link, so `evidence.sh` ends the archive section
  at its first empty line after a member, and does not look for the
  heading of the next section.
- **The linker names paths through symlinks.** gcc finds musl as
  `sysroot/lib/../lib64/libc.a` and util-linux as
  `host/bin/../x86_64-buildroot-linux-musl/sysroot/usr/lib/libblkid.a`,
  and `lib64` is a symlink to `lib`. buildroot's `staging` symlink is
  absolute, `/work/output/host/x86_64-buildroot-linux-musl/sysroot`,
  so it resolves only inside the build container. `evidence.sh` runs
  there and writes each absolute path as its real path from the root
  of the output: `/staging/...`, `/target/...`, `/host/...`, or
  `/build/...`.
- **meson writes thin archives.** The map of `iscsid` names a member
  of open-iscsi's `libfwparam.a` by its own path,
  `libfwparam.a.p/usr_fwparam_ibft_fw_entry.c.o`, with no
  `archive(member)` form. The merge takes the first field of an
  archive line, which is the archive or the member itself.
- **wpa_supplicant builds outside its source directory.** Its objects
  are absolute paths under `/build/wpa_supplicant-2.12/build/`, which
  belong to `wpa_supplicant` by its build directory.
- **buildroot's lists are not in install order.** buildroot joins the
  list of each package in the sorted order of the build directories.
  In the host list, 775 paths have a line for both `host-gcc-initial`
  and `host-gcc-final`, among them `libgcc.a`, `crtbeginT.o`, and
  `crtend.o`. The graph in buildroot's SBOM has
  `host-gcc-final -> musl -> host-gcc-initial`, so the copy in the
  tree is `host-gcc-final`'s.
- **The zone files come from the target tree.** `build.sh` copied
  them from the host tree, where `host-tzdata` installed them, and
  buildroot gives that host package the scope `excluded`. buildroot's
  `tzdata` recipe copies the host tree into the target tree with
  `cp -a`, so `build.sh` now copies from the target tree, and
  buildroot's target list names `tzdata` for each file. The 602 files
  in `dist/zoneinfo/` are the same as the host tree's, by `diff -r`.

### What the rule does with headers and with the compiler

A package that installs only headers, such as `linux-headers` and
`musl-compat-headers`, has no file that a map names, so it has the
scope `excluded`. This is correct for a scanner. buildroot gives
`linux-headers` the CPE of the Linux kernel, and the release's kernel
is an entry of `liken/kernel/pin.toml` with the same CPE and the same
version, 7.3.5. With the headers in scope, a scanner that matches by
CPE matches each kernel advisory against the headers as well as
against the kernel. A header can still put code into a program, as a
macro or an inline function. That code compiles into the program's
own objects, so the SBOM lists it under the program's package and not
under the header's package. The scope does not change what
`liken/licensing/sources.sh` mirrors, because it reads buildroot's
legal-info, which names every package of the build.

No shipped program takes a member of `libgcc.a`. Each one loads
`crtbeginT.o` and `crtend.o`, which `host-gcc-final` installed, so
`host-gcc-final` is in scope. The target package `gcc-final` installs
only `libatomic.a` into the staging tree, which no program links, so
it has the scope `excluded`. Both have the CPE
`cpe:2.3:a:gnu:gcc:15.3.0`, so a scanner matches gcc as before.

## Alternatives that were set aside

- **A list kept by hand**, as above.
- **Reading the shipped programs.** A stripped static program records
  nothing about its libraries, and syft finds nothing in one, as the
  `sbom-and-scans` skill says.
- **buildroot's dependency graph.** It records that nfs-utils depends
  on libevent, which is true of the build and says nothing about
  which program links what.
- **A change to `nfs-utils.mk`** that drops the dependency. It is a
  patch of buildroot's recipe for one package, and the next recipe
  with the same shape has the same fault.

## What the plan leaves open

The bluetoothd image builds from the same BR2_EXTERNAL tree with
`bluetoothd.config`, and `image-sbom` in `toolchains/manifest` writes
its record from buildroot's SBOM. That record keeps every target
package, so it has the same fault. The same rule applies there. It is
not small: syft ignores a scope, so the record must leave a package
out, not mark it. The rule must move to a package of
`toolchains/manifest` that both commands import, and the image's
Dockerfile must set the linker flag and write the evidence. The root
open problem
[some SBOM components name the wrong thing](../../../plans/open-problems/some-sbom-components-name-the-wrong-thing.md)
records it.

A map is named for the basename of the link's output, so a later link
of another file with the same name replaces it. No shipped program
had such a twin in the build of 2026-11-23, and the merge cannot tell
when one does.

## Proof

- Go tests with fixtures of a trimmed real map and of trimmed real
  file lists. They cover a linked library, a library that only an
  unshipped program links, a data package, a header package, a file
  that two packages installed, and each refusal.
- A userland build of the pin, and the SBOM of the lab release made
  from it, with the scope of each buildroot package.

## As built

The Go tests of `liken/releases/sbom` were written first. Before the
merge read the evidence, 20 cases failed, among them
`buildroot:libevent has the scope <nil>, want excluded` and each
refusal. With the rule, every test passes. The fixtures are a trimmed
copy of the real map of `mke2fs` and real lines of the three lists.

`make userland` at buildroot `cd21c00a15fa`, with binutils 2.46.1,
built every program, and the linker wrote 51 maps, one for each name
that a target link wrote. The evidence in `dist/evidence/` is 1.7 MB. The maps show these
archives in the shipped programs:

| Program | Archives with a member that the link took |
| --- | --- |
| `mke2fs` | e2fsprogs's own, `libblkid.a`, `libuuid.a`, `libc.a` |
| `mount.nfs` | nfs-utils's own, `libtirpc.a`, `libc.a` |
| `iscsid`, `iscsiadm` | open-iscsi's own, `libcrypto.a`, `libkmod.a`, `libmount.a`, `libblkid.a`, `libz.a`, `libc.a` |
| `wpa_supplicant` | `libcrypto.a`, `libnl-3.a`, `libnl-genl-3.a`, `libz.a`, `libc.a` |

Of the 51 maps, only `fsidd`'s names a member of `libevent_core.a` or
`libsqlite3.a`, and a release does not ship `fsidd`.

`releases/sbom/scan.sh` wrote the SBOM of a lab release from that
build. These buildroot packages changed scope:

| Package | Before | After | Evidence |
| --- | --- | --- | --- |
| `libevent` | required | excluded | linked only by `fsidd` |
| `sqlite` | required | excluded | linked only by `fsidd` |
| `linux-headers` | required | excluded | headers only |
| `musl-compat-headers` | required | excluded | headers only |
| `gcc-final` | required | excluded | installs only `libatomic.a`, which no program links |
| `ifupdown-scripts` | required | excluded | two scripts in `/etc/network/` of the target tree, which no release copies |
| `skeleton-init-common` | required | excluded | the skeleton of the target tree, which no release copies |
| `skeleton-init-none` | required | excluded | installs no file |
| `host-gcc-final` | excluded | required | `crtbeginT.o` and `crtend.o` in each program |

A second `make userland`, from a clean `_build/` with the final
scripts, wrote programs with the same SHA-256 digests and evidence
with the same lines. buildroot's lists came out in another order, so
`dist/evidence/` is not the same bytes from build to build. The SBOM
of the lab release from the second build was the same bytes as the
first.

These stayed in scope: `musl`, `util-linux`, `libtirpc`,
`libopenssl`, `libnl`, `libzlib`, `libkmod-static`, `e2fsprogs`,
`nfs-utils`, `open-iscsi-static`, `wpa_supplicant`, and `tzdata`.
Every host package other than `host-gcc-final` stayed `excluded`.
