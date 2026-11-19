# A comment on TeardownAddon contradicts the restart code

Open problem, recorded on 2026-11-18. Nobody has established which
of the two is correct.

A comment in `cluster/features.go`, near line 302, states something
about `TeardownAddon` that the restart code of `init` contradicts.
The agent that cleaned up `init` after its rewrite reported the
contradiction and could not change `cluster/`, which was outside its
scope.

## What would confirm it

Read the comment, then read what `init/restart.go` does with a
feature's addon when the feature is removed.

## What a fix has to decide

Whether the comment or the code states the intended behavior.
