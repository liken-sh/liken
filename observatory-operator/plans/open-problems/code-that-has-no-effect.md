# Code that has no effect

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two pieces of code run, or are declared, but change nothing. Neither
gives a wrong result. Each one makes a reader look for a case that
does not exist.

- `procedure.go`, `runAction`: in the `case ctx.Err() != nil:`
  branch, the code sets `cause := err` and then runs
  `if ctx.Err() != nil { cause = context.Cause(ctx) }`. The case
  already requires `ctx.Err() != nil`, so the `if` is always true and
  `cause` is always `context.Cause(ctx)`. The first assignment has no
  effect.
- `cli/completion.go`, `completeFlagValue`: the parameter
  `toComplete` is not read. The function returns a directive by the
  flag's name alone. The same function is in `cli/completion.go` of
  `audio-operator`, `bluetooth-operator`, `display-operator`,
  `library-operator`, `media-operator`, and `liken`.

## What would confirm it

Read the two functions. For `runAction`, a test that ends a run's
context during an action already reaches the branch, and its summary
shows the context's cause.

## What a fix has to decide

- For `runAction`, whether the branch was meant to fall back to the
  action's error when the context has no cause, or whether
  `cause := context.Cause(ctx)` is the whole intent.
- For `completeFlagValue`, whether the parameter goes from every copy
  in one commit, or stays as the place where a future flag completes
  its value from a prefix.
