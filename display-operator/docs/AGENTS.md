# Authoring this docs domain

@../../brand/voice.md

Every word this site publishes follows the voice rules in that file. The
base under them is ASD-STE100: short sentences, active voice, plain
words, one instruction per sentence. The rules are in the brand theme,
`brand/` at the top of the repository, which also gives the site its
shell.

Build the site with `go tool hugo` from this directory. The output lands
in `dist/site/`, which git ignores.
