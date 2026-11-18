## No status

A `Keymap` has no status, so `kubectl get keymaps` shows only each
`Keymap`'s age. On each pass, the operator builds the key table of each
`Remote` from the base table and the `Remote`'s `Keymap`. When a
`press` or `key` name is not one the operator knows, or a repeat time
does not parse, the operator logs the error and posts a
`KeymapRefused` Warning `Event` on each `Remote` that uses the
`Keymap`. Those `Remote`s keep their last good table.

## On the bus

A `Keymap` has no topic of its own. The operator publishes each
`Remote`'s key table, retained, on that `Remote`'s `keys` topic, in the
[`Remote`'s topic tree](/docs/reference/remotes/). When a `Keymap` has
an error, the operator publishes nothing, so the broker keeps the last
good table. When you delete a `Keymap`, the operator publishes the base
table alone to each `Remote` that used it, on the next pass.
