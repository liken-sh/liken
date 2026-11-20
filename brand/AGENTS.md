# Working on brand

`brand` is the theme, the voice rules, and the site tools: a Go
module, a Hugo theme, and a Rust crate. `README.md` says what it is
for, how each kind of consumer uses it, and what its parts are.

## Design

The mark has one original, `liken.svg`, and the stylesheet has one
original, `liken.css`. Every other copy is made from one of them. A
site uses this directory as its theme but runs no build step inside
it, so the theme serves copies that `make` writes and that the
repository commits. The release channel embeds the originals. So
when you edit an original, run `make` and commit the copies.
Otherwise the sites show the old look and the release channel shows
the new one. The screens parse the same originals when they compile,
so a screen and a page draw the same shape in the same colors.

The image builds that take this directory as the build context
`brand` receive only the paths that `.dockerignore` keeps. A
Dockerfile that copies another path fails until you add the path
there, and the comment in `.dockerignore` gives the reason.

The reasons for the mark and the theme are in `mark.md` and
`theme.md`. The design of the generated pages is in `plans/`.
