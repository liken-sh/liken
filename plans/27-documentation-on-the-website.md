# Documentation on the website

Milestone 27 — Not started

The repo is the documentation. That has been the rule since the first
commit, and this milestone does not change it. But "read the repo" is
an answer for someone who already cares, and the website (milestone
25) will meet people who do not yet care. They need a reading order
that the repo itself cannot provide: what liken is, in one page; how
to produce a cluster of your own from a public release (the utilities
that milestone 22 still owes); and where the deeper explanations live.

The design question concerns provenance. Documentation on the web
becomes outdated the moment it diverges from the code, and this repo's
writing already lives beside what it describes. So the site should
extract and arrange existing text, rather than restate it: it should
render the plans, the domain documents, and the literate comments into
pages, and keep anything written only for the web to a minimum, used
only to connect these pieces together. If a page cannot be traced back
to a file in the repo, that is a sign the page needs to change.

This document leaves some questions open, deliberately. What does the
extraction look like in practice? Rendering markdown is easy; showing
the teaching comments inside shell and YAML files is the harder part.
What does the getting-started path assume about the reader's
hardware? QEMU is the likely first choice, since that is how the repo
itself teaches. And how should the documentation handle versioning,
since documentation for a release should describe that release?
