# Comments that the rewrite did not reach

Open problem, recorded on 2026-11-18.

The rewrite of this component's text, which
[root plan 93](../../../plans/93-finish-the-plain-words-rewrite.md)
describes, left comments that still use "holds", "takes", "states",
and "the one" in senses that the code does not define. The agent that
cleaned up after the rewrite reported them and did not list each one.

## What would confirm it

Search the Go files for those words, and read each hit against the
code under it. Many hits are the plain sense of the word, such as a
claim that holds a device.

## What a fix has to decide

Nothing. Each comment is written again from the code, or deleted when
it repeats the code.
