# Names and page copy that the rewrite did not reach

Open problem, recorded on 2026-11-18.

The rewrite of this component's text, which
[root plan 93](../../../plans/93-finish-the-plain-words-rewrite.md)
describes, renamed the private names inside each crate. Some exported
names that other crates of the workspace use still read as private
words, and some copy on the pages was reported as unclear and not
changed. The cleanup agent's report listed both, and the report is
not in the repository.

## What would confirm it

Read the `pub` names of each crate and the templates of the pages
with a person who has not worked on `transect`.

## What a fix has to decide

The new name of each exported type, because a rename reaches every
crate that uses it, and the golden files of the pages.
