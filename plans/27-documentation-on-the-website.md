# Documentation on the website

Milestone 27 — Not started

The repo is the documentation — that has been the rule since the
first commit, and this milestone does not soften it. But "read the
repo" is an answer for someone who already cares, and the website
(milestone 25) will meet people who don't yet. They need the reading
order the repo can't impose: what liken is in one page, how to
produce a cluster of your own from a public release (the utilities
milestone 22 owes), and where the deep explanations live.

The design question is one of provenance: documentation on the web
rots the moment it forks from the code, and this repo's writing
already lives beside what it describes. So the site should extract
and arrange rather than restate — rendering the plans, the domain
documents, and the literate comments into pages, with anything
written only for the web kept to the connective tissue. If a page
can't be traced back to a file in the repo, that's a smell.

Open questions, deliberately unanswered here: what the extraction
looks like in practice (rendering markdown is easy; surfacing the
teaching comments inside shell and YAML is the interesting part);
what the getting-started path assumes about the reader's hardware
(QEMU first, surely, since that is how the repo itself teaches); and
versioning, since documentation for a release should describe that
release.
