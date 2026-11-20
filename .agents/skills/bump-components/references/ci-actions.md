# The GitHub Actions, prek, and the hook revisions

`ci/latest.sh --bump` moves one repository of GitHub Actions, prek,
or one hook revision.

**A repository of actions.** `actionPins` in `ci/actions.go` lists
each repository of actions once, with its release and the commit of
the release's tag. Every `uses:` has the form
`<action>@<commit> # <release>`.

`--bump <repository>`, such as `--bump actions/checkout`, stops
unless GitHub publishes the newest release as a release that is not
a pre-release. It then writes the commit of the tag into the table,
and into each composite action under `.github/actions`. For an
annotated tag, that is the commit that the tag points to. Run
`make workflows` after it.

A major version of an action can change its inputs, so read its
release notes before you take it.

**flux2.** The `fluxcd/flux2` line follows `liken/flux/VERSION`, and
`liken/flux/latest.sh --bump` moves it.

**prek.** prek-action runs a prek version that is not in its own
checksum table without checking it. So `--bump prek` also writes
`prekSHA256` in `ci/workflow.go`, which is the digest of the archive
that the action downloads. The bump gets the digest from the
release's `.sha256` file, and verifies the GitHub attestation of the
archive with `gh`, so it needs `gh`.
