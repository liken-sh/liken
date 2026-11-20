# Working on brand

`brand` is the theme, the voice rules, and the site tools: a Go
module, a Hugo theme, and a Rust crate. `README.md` says what it is
for and describes its parts.

## How it runs

Nothing here runs on a cluster. Every manual's build reads this
directory from the tree. Hugo reads the theme, and the docs module
runs the Go tools through a `replace` directive. The page builder of
the release channel, in `liken/releases`, imports the `brand`
package. The images of the screens of `media-operator` and
`library-operator` use this directory as the build context `brand`.
For those builds, `.dockerignore` sends only the paths that
`liken-iced` compiles from.

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

The reasons for the mark and the theme are in `mark.md` and
`theme.md`. The design of the generated pages is in `plans/`.
