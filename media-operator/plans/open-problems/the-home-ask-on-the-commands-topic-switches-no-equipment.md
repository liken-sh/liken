# The home ask on the commands topic switches no equipment

A program can publish `{"action":"home"}` on a `Player`'s `commands`
topic. The idle client reads it as a press of the home key, but
`media-operator` writes an `inputAsk` `show` on the `Receiver` only
for a press it reads from a remote's events topic (`ensure.go`). So
the ask brings the client to its home page and leaves the TV and the
receiver on whatever input they show.

The remote's home key and the ask should do the same thing. One fix is
for `media-operator` to read the ask and write the same `show`, the way
it reads a home press.
