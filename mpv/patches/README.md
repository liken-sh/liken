# mpv patches

The `mpv` build applies each `.patch` file in this directory to the
release tarball, in the order of the file names, and the build fails
when a patch does not apply exactly. Each file is an upstream `mpv`
commit that a later release holds and the pinned release does not, as
`git format-patch -1 <commit>` writes it from mpv's repository, with
its author, date, subject, and commit id. Nothing edits a patch by
hand.

`pin.toml` names each commit in `backports`, so the image's SBOM
states the release with those commits as its pedigree. A patch and its
`backports` URL are added and removed together.

| Patch | Upstream commit | First release that holds it | Why |
| --- | --- | --- | --- |

`latest.sh --bump` refuses while this directory holds a patch, because
the newest release may hold the commit and the patch then fails to
apply. The steps of that bump are in the `mpv` README.
