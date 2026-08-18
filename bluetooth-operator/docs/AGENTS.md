@themes/brand/voice.md

This directory is the docs domain: the site at bluetooth.liken.sh.
All site prose is Simplified Technical English (ASD-STE100) and
follows the voice rules imported above. Read them before you write a
page, and scan your text against them before you publish.

The look is not authored here. The brand theme, a git submodule at
`themes/brand`, carries the shell, the nav, and the stylesheet that
every liken site shares. `hugo.yaml` names the theme and declares
the Markdown output format the theme's templates render, so every
page publishes twice: as HTML for people, and as the authored
Markdown for agents and scripts.

Build the site from this directory with `go tool hugo --destination
dist`. The `dist/` tree is build output and is not committed.
