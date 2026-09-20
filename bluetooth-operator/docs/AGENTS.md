# Authoring this docs domain

@themes/brand/voice.md

This directory is the source of https://bluetooth.liken.sh/. Write all
site prose in Simplified Technical English (ASD-STE100): short sentences,
active voice, one instruction per sentence. The voice rules in that file
add what the standard does not state. Read them before you write a page,
and scan your text against them before you publish it.

The look is not authored here. The brand theme, a git submodule at
`themes/brand`, provides the shell, the nav, and the stylesheet that
every `liken` site shares. `hugo.yaml` names the theme and declares the
Markdown output format that the theme's templates render, so every page
publishes twice: as HTML for people, and as the authored Markdown for
agents and scripts.

Build the site from this directory with `make build`. It generates the
CRD reference pages with `crdref`, then runs Hugo into `dist/site`. The
`dist/` tree is build output and is not committed.
