# The plan drops the error of a `.dockerignore` match

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19. A review of the comments against the code found it.

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
expression. The reproduction below found such a pattern.

## The reproduction

The range `[z-a]` reaches the error. `filepath.Match` reads a range
without checking its order, so `patternmatcher.New` accepts it, and
the match then fails when patternmatcher v0.6.1 compiles the pattern
to a regular expression. The test wrote `app/.dockerignore` with the
one pattern `[z-a]` and a file `app/main.go`, called `contextSends`
for `app/main.go`, and asserted an error. It failed with:

```
contextSends returned "app/main.go" and no error, want the error of the pattern [z-a]
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When a pattern in a `.dockerignore` cannot be matched, should
`ci plan` stop with the error, or build the image and print a warning?

Whether the plan returns the error, as `Recipe` does, or keeps the
choice to run the build and prints a warning.
