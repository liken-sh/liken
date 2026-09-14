# 62, Intro and credits marks

A stub from a 2026-09-14 conversation. Nothing here is built, and this
plan proposes nothing. It states two problems that share one missing
fact.

## The problem

Nothing in the catalog says where a work's story starts or where it
ends. A video file carries a runtime and little else. Every part of
`liken` that needs those two points guesses them from the runtime.

**A series makes a person skip by hand.** An episode opens with a
recap of earlier episodes, then a title sequence, and the story
follows. A person who watches a season skips the same two blocks in
every episode. Nothing tells the player where the story starts, so
nothing can offer the skip.

**The credits are guessed from a percentage.** The up-next card rises
when three percent of the runtime or less remains. A work counts as
watched when a twentieth of the runtime or less remains, and five
minutes or less. Both numbers estimate where the credits start. Credits do not scale with
the runtime. A feature's credits run about five to ten minutes
whatever its length, and an episode's run about thirty to sixty
seconds. So one percentage is too late for a long film and too early
for a short one.

The two problems need the same kind of fact: a point in the file that
comes from the work itself, and not from its runtime.

The fact has two consumers in two repositories. This operator would
hold it and the player in
[`media-operator`](https://github.com/liken-sh/media-operator) would
act on it, so the plan that answers this one spans both.
