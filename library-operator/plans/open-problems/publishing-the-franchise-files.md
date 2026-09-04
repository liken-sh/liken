# Publishing the franchise files

Open problem, raised on 2026-09-03 when the first franchise files
were written. A franchise file is research, not scanning: a person or
an agent reads the timelines, verifies every provider id, and writes
the story order with its judgment calls beside it. Thirty-two of them
exist today, with a context file each, and none of that work depends
on the library that holds them. Another library would draw the same
order from the same file.

The files have no public home. The `Franchises` folder is one
volume on one cluster, and this repository holds the schema and the
manual but not the files, because a franchise is an opinion about a
story and not a part of the operator. A public repository of its own,
outside the `liken` organization, is the likely shape: one directory
per franchise, the same `franchise.yaml` and `AGENTS.md`, validated
in CI against the published schema, with the scheduled agent of plan
31 writing to it by pull request instead of to a share.

Three questions wait on that repository. How a `Library` of kind
`franchises` takes its folder from a git checkout, which the kind does
not do today. What the license on a file of provider ids and story
order should be. And whether the art stays out, since the folder's art
is the one part a public repository cannot carry.
