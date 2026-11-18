# The version warning names `plugins sync` without its argument

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When the plugin's version differs from the operator's, or is older
than the operator's minimum, `decideVersionAction` in `cli/version.go`
tells the user to run `syncCommand`, which is
`kubectl liken plugins sync`. The `liken` toolkit's `pluginsSync` in
`liken/cli/plugins.go` requires one argument, the deployment
directory, and returns "usage: liken plugins sync [-server URL]
<deployment-dir>" when the argument is missing. So the command that
the warning gives fails as written. The manual's guides give the
command with its argument: `liken plugins sync <deployment-dir>` in
`docs/content/docs/guides/set-up-a-portable-telescope.md`.

The same constant is in `cli/version.go` of `audio-operator`,
`bluetooth-operator`, `display-operator`, `library-operator`, and
`media-operator`, and the package comment of `liken/plugins/sync.go`
names the command in the same form.

## Evidence

- `cli/version.go`: `const syncCommand = "kubectl liken plugins sync"`.
- `liken/cli/plugins.go`, `pluginsSync`: `if fs.NArg() != 1` returns
  the usage error.
- `liken/docs/content/docs/reference/cli.md`: the reference gives
  `liken plugins sync [-server URL] <deployment-dir>`.

## What would confirm it

Run `kubectl liken plugins sync` with no argument on a workstation
that has the toolkit installed as `kubectl-liken`, and read the
result.

## What a fix has to decide

- Whether the warning names the argument, as in
  `kubectl liken plugins sync <deployment-dir>`, or whether
  `plugins sync` reads a default deployment directory when none is
  given.
- Whether every component's copy changes in one commit, so that the
  copies stay the same.
