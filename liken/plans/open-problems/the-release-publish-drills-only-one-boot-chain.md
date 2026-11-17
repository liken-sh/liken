# The release publish drills only one boot chain

Open problem, and an open design question. The publish of an OS
release boots the build stamped with the release's version only
through the UEFI chain. The BIOS chain is drilled on the development
build of the same commit, not on the build that the release ships.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

The publish job of `.github/workflows/component-liken.yaml` runs
`make release` and `make smoke-uefi`. The `liken-publish` job of
`.github/workflows/ci.yaml` needs the `liken` job, the check stage of
the same workflow, whose `build` job ran both `make smoke-uefi` and
`make smoke-bios` on the same commit. So the BIOS chain is drilled on
the development build of the commit, not on the build stamped with the
release's version. Milestone 66 proposed that the release workflow run
both drills, before that workflow was removed.

## The design question

This needs the maintainer's decision before a fix is built.

**Whether the release publish runs the BIOS drill.** The check stage
drills both boot chains on the development build of the same commit.
Running `make smoke-bios` in the publish job too drills the build
stamped with the release's version.
