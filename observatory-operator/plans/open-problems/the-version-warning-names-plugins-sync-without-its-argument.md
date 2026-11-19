# The version warning names `plugins sync` without its argument

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test calls `decideVersionAction` with a CLI version older than the
operator's and wants the warning to name `plugins sync
<deployment-dir>`. It failed with:

```
the warning is "this CLI is 2026.11.01-001 and the operator is 2026.11.18-001; run kubectl liken plugins sync", want the command plugins sync <deployment-dir>
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

The command that the version warning gives fails with a usage error,
because `plugins sync` needs the deployment directory.

- Does the warning print `kubectl liken plugins sync <deployment-dir>`
  in every component's CLI?
- Or does `plugins sync` use a default deployment directory when you
  give none?
