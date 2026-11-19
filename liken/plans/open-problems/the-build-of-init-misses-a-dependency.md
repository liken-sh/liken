# The build of init misses a dependency

Open bug, recorded on 2026-11-18. The claim was checked on
2026-11-19: `go list -deps` in `init/` lists
`github.com/liken-sh/liken/kubernetes/conditions`, and the rule for
`init/dist/liken` (`Makefile:207`) lists no file under
`../kubernetes`. No unit test can show a make rule. The `make -n` run
in "What would confirm it" can.

`liken` links `kubernetes/conditions`, but neither make rule that
builds it lists that package's files. After a change to
`kubernetes/conditions`, `make` on a workstation keeps the old binary.
CI builds from a clean tree, so it is not affected. Separately,
`make -C init clean` removes `dist`, not `$(DIST)`, so it leaves a
binary that a build with another `DIST` wrote.

## The evidence

`go list -deps` in `init/` lists
`github.com/liken-sh/liken/kubernetes/conditions`, and `go.mod`
replaces that module with `../kubernetes`. The rule for
`$(DIST)/liken` in `init/Makefile` and the rule for `init/dist/liken`
in `Makefile` (line 207) list the files of `api`, `cluster`,
`machine`, `disks`, and `hardware`, and no file under `../kubernetes`.
The rule for the machine-operator image in the same `Makefile`
(line 238) lists `$(wildcard ../kubernetes/*/*.go)`.

The `clean` rule in `init/Makefile` runs `rm -rf dist`, and the
comment above `DIST ?= dist` says that the releases build passes its
own `DIST`.

## What would confirm it

Build `init/dist/liken`, touch a file in `kubernetes/conditions`, and
run `make -n init/dist/liken`. make prints no command.

## What a fix has to decide

Whether each rule lists the Go files that the binary links by hand, or
reads them from `go list -deps`, so that a new import cannot leave the
list behind. And whether `clean` removes `$(DIST)`, or the releases
build cleans its own tree.
