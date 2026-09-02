## No status

Nothing reports on a `Keymap`, so it has no status subresource, and
`kubectl get keymaps` shows each table's age. The operator compiles
each table on every pass. A `press`, `axis`, or `key` name that is not
an evdev name fails the compile, the operator logs the failure, and
every `Remote` that names the `Keymap` keeps the last good table.

## On the bus

A `Keymap` owns no topic of its own. The operator folds the base table
with the `Keymap` and publishes the result retained on the `keys`
topic of each `Remote` that names it, under the
[`Remote`'s tree](/docs/reference/remotes/). A `Keymap` that does not
compile publishes nothing and leaves the last good table on each of
those topics. A deleted `Keymap` leaves each of its `Remote`s on the
base alone, and the operator republishes their tables on the next
pass.
