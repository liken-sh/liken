# kubectl does not complete the operator plugins

Open bug, recorded on 2026-11-20. A unit test reproduced it on
2026-11-20.

After you install the operator plugins the way the manual says,
`kubectl liken audio <TAB>` completes nothing. The same is true for
every other plugin and for `kubectl liken <TAB>` itself.

kubectl completes a plugin by running an executable named
`kubectl_complete-<plugin>` that it finds on `PATH`. Each plugin has
that file in its source: `kubectl_complete-liken-<domain>` in the
`cli/` of each operator that has a plugin, and `kubectl_complete-liken`
in `liken/cli/`. Each one is a short script that runs the plugin's
hidden `__complete` verb. Nothing puts any of them on a workstation.

## Evidence

- No `Dockerfile.cli` copies the script into the `-cli` image. Each
  one copies only the binary `kubectl-liken-<domain>`.
- `liken plugins sync` installs one file for each operator. `Sync` in
  `liken/plugins/sync.go` extracts the file that the image's
  entrypoint names and writes it to the plugin directory.
- "Tab completion" in `liken/docs/content/docs/reference/cli.md` says
  that `plugins sync` does not install completion, and tells the
  reader to write each plugin's `completion bash` output into the
  shell's completion directory. That script completes
  `kubectl-liken-audio <TAB>`, the direct form. The page then says "A
  `kubectl_complete-liken-<domain>` executable on `PATH` enables
  `kubectl liken <domain> <TAB>`", and gives no step that creates the
  executable.
- The reason that the page gives for the manual step is that a shell
  reads completion scripts from its own directories. That reason does
  not apply to the `kubectl_complete-*` files, which kubectl finds on
  `PATH`. The plugin directory is already on `PATH`.

No release package or `Makefile` target in `liken/` names
`kubectl_complete`, so the `liken` command's own file is not installed
either. I did not check how a person installs `liken` from a package
outside this repository.

## The reproduction

A test in `liken/plugins` ran `Sync` for one operator, with the fake
pull that `sync_test.go` has, and then looked in the plugin directory
for `kubectl_complete-liken-audio`. It failed:

```
after a sync, the plugin directory holds [kubectl-liken-audio], want kubectl_complete-liken-audio too: stat .../bin/kubectl_complete-liken-audio: no such file or directory
```

The test is not in the tree.

## What a fix has to decide

- **Who installs the file.** `plugins sync` can write it beside each
  binary, because the plugin directory is on `PATH` and the file's
  text depends only on the domain. Or each `-cli` image can ship it,
  and `Sync` can extract a second file.
- **Whether the file stays in each component.** The files differ only
  in the domain. If `plugins sync` writes them, the copies in each
  `cli/` have no reader and can go.
- **The `liken` command's own file.** A person installs `liken` before
  they can reach a cluster, so `plugins sync` is one place that can
  write `kubectl_complete-liken`. A release package is another.
- **The shell scripts.** The manual step for `completion bash` stays
  unless the fix also covers the direct form.
- **The manual.** "Tab completion" needs the step, or needs to say
  that `plugins sync` does it.
