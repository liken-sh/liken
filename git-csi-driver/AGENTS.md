# Working on the git CSI driver

This repository is a Kubernetes CSI driver that mounts git repositories
as volumes on a [`liken`](https://liken.sh/) cluster. A read-only volume is a checkout that
follows a ref. A writeable volume is a checkout the driver commits and
pushes for the application that writes it. The Go files, the manifests,
and the workflows are the documentation, and the comments teach how the
system works.

@docs/themes/brand/voice.md

The voice rules in that file govern all prose in this repository,
comments included. They arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it. A plan
states contracts and leaves the shape of the code to the person or
agent who builds it.

## Errors include their source's text

An error that wraps a tool, a daemon socket, a bus answer, or a provider
includes that source's own text word for word: its `stderr`, its
response body, or its error string. The wrapped error and the status
field or record that the failure writes both include it, so a person
reads the cause from the log or the status without opening a shell.

## The lab

Nothing in this repository is installed on a real cluster until a person
has seen it work. `lab/` boots one `liken` machine in QEMU from the
public release channel, and serves a git repository to it from the host.
Every drill runs there.

## Releases and development builds

A pushed tag is a release. The tag names a version in `liken`'s calendar
scheme, for example `2026.09.03-007`. `release.yaml` builds every image
in the repository beside the `ci.yaml` run of the same commit, waits for
that run to pass, and pushes the images under the version tag and under
`:latest`.

A push to `main` is a development build. `release.yaml` builds the
images the same way and pushes them under a version from `git describe`:
the most recent release tag, the number of commits since it, and the
first eight characters of the commit. For example,
`2026.09.03-007-dev-003-abcdef01` is three commits past
`2026.09.03-007`, at commit `abcdef01`. A development build never moves
`:latest`, so a cluster that pulls a release keeps pulling releases.
The suffix sorts after its release and before the next one, and the tag
check in `release.yaml` does not accept it as a release version.

To run a development build, pin the manifests to the full commit sha
and the image to the build's version:

    resources:
      - https://github.com/liken-sh/git-csi-driver//deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/git-csi-driver
        newTag: 2026.09.03-007-dev-003-abcdef01

Use all forty characters of the commit sha in `ref=`. A `git fetch` by
sha requires all forty, and the eight characters inside the version are
not enough. The CI run's step summary prints these lines for the commit.
