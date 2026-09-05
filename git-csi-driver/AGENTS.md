# Working on the git CSI driver

This repository is a Kubernetes CSI driver that mounts git repositories
as volumes on a [`liken`](https://liken.sh/) cluster. A read-only
volume is a checkout that follows a ref. A writeable volume is a
checkout the driver commits and pushes for the application that
writes it. Like the rest of the `liken` project, it is written to be
read: the Go files, manifests, and workflows are the documentation.

@docs/themes/brand/voice.md

The voice rules imported above govern all prose in this repository,
comments included, and they arrive with the brand theme submodule at
`docs/themes/brand`.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it. A
plan states contracts and leaves the shape of the code to the person
or agent who builds it.

## The lab

Nothing in this repository is installed on a real cluster until a
person has seen it work. `lab/` boots one `liken` machine in QEMU from
the public release channel and serves a git repository to it from the
host. Every drill runs there.

## Releases and development builds

A pushed tag is a release. It names a version in liken's calendar
scheme, `2026.09.03-007`, and `release.yaml` builds the image and
pushes it under that tag and `:latest`.

A push to main is a development build. `release.yaml` builds the same
image beside the `ci.yaml` run of the same commit, waits for that run
to pass before it pushes anything, and pushes the image under the most
recent release tag, from `git describe`, plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past that release,
at commit `abcdef01`. `:latest` never moves on a development build,
and the tag shape check in `release.yaml` never accepts the suffix.

To run a development build, pin the manifests to the full sha of the
commit and the image to the version:

    resources:
      - https://github.com/liken-sh/git-csi-driver//deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/git-csi-driver
        newTag: 2026.09.03-007-dev-003-abcdef01

A git fetch by sha needs all forty characters, so the short sha in the
version is not enough for `ref=`. The CI run's step summary prints
both lines for that commit.
