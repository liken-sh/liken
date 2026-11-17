# Intro and credits each name two things

The mark kinds `intro` and `credits` name the title sequences at the
start and at the end of a video. In the catalog, `credits` also names
the list of a title's people, and the `credits` fact fetches that list.
So a reader of the code, the catalog, or the manual must tell from the
context which one a `credits` is.

[Plan 96](../completed/96-end-titles-from-the-picture.md#the-words)
chose the film trade's words: the main titles open a film and the end
titles close it. The mark kinds `intro` and `credits` become
`main-titles` and `end-titles`, and `recap`, `preview`, and
`post-credits` keep their names. The `titles` fact, its tool, and its
status already use the new words, and the placement row has the kind
`credits-placement` until the rename.

The rename is not built. It changes the kinds in `library-operator`,
in `media-operator`'s display, which ignores a kind it does not know,
in the media browser, in the marks ledgers that are stored beside the
media, and in the manuals. A release that writes the new kinds meets
ledgers, catalogs, and screens that hold the old ones, so the order of
the change across the components is the part to design.
