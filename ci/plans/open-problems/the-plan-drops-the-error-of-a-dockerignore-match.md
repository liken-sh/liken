# The plan drops the error of a `.dockerignore` match

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

Two functions of the plan decide whether a build context sends a
changed file, and both read the context's ignore file with
`readIgnore` and then call `MatchesOrParentMatches` of
`github.com/moby/patternmatcher`:

- `contextSends` in `ci/graph.go` writes
  `if err != nil || !skip { sent = rel }` and returns no error. A
  match that fails counts the file as sent.
- `selector.contextChanged` in `ci/selection.go` writes
  `if skip, err := ...; err != nil || !skip { return true }`. A match
  that fails runs the image. A failed `readIgnore` also runs it.

Both choices run more work, not less, so a failed match does not skip
a build. But neither function reports the error, so `ci plan` prints
no sign of a broken pattern. `Recipe` in `ci/recipe.go` does return
the same error, for a pinned component only.

`patternmatcher.New`, which `readIgnore` calls, checks each pattern
with `filepath.Match`, so a pattern with bad syntax fails in
`readIgnore` already. `MatchesOrParentMatches` returns an error only
when a pattern that passed that check fails to compile to a regular
expression. The review did not find a pattern that does that, so this
can be a case that never happens.

## What would confirm it

A pattern that `filepath.Match` accepts and `Pattern.compile` of
patternmatcher v0.6.1 refuses. With one in a `.dockerignore`, a test
of `contextSends` gets the file as sent and no error.

## What a fix has to decide

Whether the plan returns the error, as `Recipe` does, or keeps the
choice to run the build and prints a warning. If no pattern can reach
the error, the fix can instead state that in a comment at both calls.
