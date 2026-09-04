# A franchises folder from a git checkout

Open problem, raised on 2026-09-03 when the first franchise files
were written and narrowed on 2026-09-04 when they moved to a public
repository, `tangled.org/guid.foo/fiction-franchises`. The files are
research, not scanning, and nothing in them depends on the library
that holds them, so they live outside this operator with their own
schema and README.

What is undesigned is how a `Library` of kind `franchises` takes its
folder from that repository. Today the kind names a folder on a
volume, and a person keeps that folder a checkout by hand. A
`Library` that names a git URL, with a fetch on a schedule or on a
webhook, would remove the hand step, and it is the first library kind
whose truth is a repository and not a volume.

Two smaller questions wait with it. The art stays out of the
repository, so a checkout holds no art and the enrichers of plan 30
fill it from the members. And the scheduled agent of plan 31 would
write to that repository by pull request instead of to a share.
