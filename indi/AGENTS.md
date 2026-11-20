# Working on indi

`indi` is a pinned set of images, built from dated snapshots of
Ubuntu, the INDI PPA, and the PHD2 PPA, and published under
`<version>-<revision>`. `README.md` describes the images. Load the
`releases` and `bump-components` skills under `.agents/skills` before
you change the version or the revision.

## Design

The recipe is a hash of `package.toml` and of every file that Docker
sends to the build. So a change to any of those files, even to a
comment, needs the next revision. `.dockerignore` lists the files
that Docker leaves out. A new revision rebuilds every image, so put
related changes into one revision. When only `observatory-operator`'s
tests need a file, keep the file in that component, not here.

`observatory-operator` reads this component's files and runs its
images. It generates its map of drivers from `images/` and the tag in
`package.toml`, and it copies `libqhy`'s udev rules out of
`indi-qhy`. The constants at the top of
`observatory-operator/guiderpod.go` must match the paths and the
environment of `indi-phd2`. When you change one, change the other in
the same commit.

## SDK licenses

`sdk-licenses/` is a copy of the license file of each SDK directory
of [indi-3rdparty](https://github.com/indilib/indi-3rdparty) at
commit `9d8aff3711efa824137123b007b97978aeac2688`. That was the newest
commit of its master branch when the PPA built the packages of
2026-10-04, which are the packages that the snapshot installs. The
copyright file of the PPA's `indi-3rdparty-libs` package mentions
none of the SDKs. So the `license` entries of the lists copy these
files into the images. The directories of the QHY, Atik, Astroasis,
and SVBONY SDKs, and of `libflipro`, have no license file. When a
bump copies new license files, write the new commit here.

## Finding things

- The smoke checks under `smoke/` need the images in the local Docker
  daemon, built through `docker-bake.hcl` at the top of the
  repository. Each check runs `notices/check.sh`, and compares the
  packages in the image with `[package.upstream]`.
- After an INDI bump, `check-lists.sh` fails the build until each
  third-party driver that `indi-3rdparty-drivers` installs is in
  exactly one list under `images/`, or in `images/unpublished`, and
  until each driver in a list is one that the package installs.
