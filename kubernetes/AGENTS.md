# Working on the shared Kubernetes module

This directory is the Go module `github.com/liken-sh/liken/kubernetes`.
The operators import it through a `replace` directive, so a change here
reaches every operator that imports it in the same commit. Run the
tests of each operator that imports the package you change, as well as
`make test` here. The source files are the documentation, and the
comments teach how the system works.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included.

## What belongs here

A type or a function comes here when two operators need it in the same
form. Code that only one operator uses stays in that operator, with one
exception: a hook that cannot live outside its package, because it is
an option of a shared function or it reads a shared type's private
state. Such a hook comes here even for one operator, and the commit
that adds it names the operator. The hooks of that kind now are:

- `informer.Options.ListFailed` and `Options.Transform`, and
  `memo.Versions.Forget` and `Versions.Noted`, for `library-operator`.
- `apiclient.ErrThrottled`, `RetryAfterSeconds`, and
  `Client.WithWaitContext`, for `equipment-operator`.
- `apiclient.InClusterOptions.Server` and `Timeout`,
  `Client.WithWriteGuard`, `informer.InClusterAt`, `Options.Indexers`,
  `Options.UnreadyOnWatchError`, `memo.Versions.ForgetAt`, and the
  alias `memo.Meta`, for `liken`'s two operators.

The code the hook runs, such as the transform itself, stays in the
operator.

`apiclient` and `memo` import nothing from `k8s.io`, because some
programs must not link client-go at all: the pod build of
`library-operator` checks for it in its `Makefile`. Keep every
client-go import in `informer`, and keep `informer` to `tools/cache`,
`dynamic`, and `rest`. The pod build of `media-operator` and the node
build of `equipment-operator` link those three, and their `Makefile`s
fail when a build links the typed clientset, the informer factories,
or leader election.

## Watches

Load the `operators` skill in `.agents/skills` before you change a
watch, the copy a pass reads, or the memo. It holds the three guards of
a watch, the scenarios a watch must pass, and the reasons the watches
run on client-go's reflector.
