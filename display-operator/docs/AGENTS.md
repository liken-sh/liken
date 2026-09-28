# Authoring this docs domain

@themes/brand/voice.md

Every word this site publishes follows the voice rules in that file. The
base under them is ASD-STE100: short sentences, active voice, plain
words, one instruction per sentence. The rules arrive with the theme
submodule at `themes/brand`, so one pin brings both the site's shell and
its style.

Build the site with `go tool hugo` from this directory. The output lands
in `dist/site/`, which git ignores.
