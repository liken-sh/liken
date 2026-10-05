# The `Secret` does not copy the adapter's `identity` file

Open problem. The files the `Secret` holds were chosen by reading BlueZ's
source. One adapter file that holds a key is not among them, and the
operator has not decided whether privacy is supported.

[Plan 03](../completed/03-a-secret-for-each-adapter.md) decides which files
the `Secret` copies. It weighs the cost of leaving out `settings` and
`attributes`, and leaves both out. It does not weigh the cost of the
`identity` file.

The operator copies each paired device's `info` file and the matching
`cache` entry. It copies no file that belongs to the adapter itself,
and one adapter file holds a key: `<adapter>/identity`, which holds
`[General] IdentityResolvingKey`. That is the adapter's own IRK, the
key a peer uses to resolve this radio's rotating address back to one
identity. `load_irk` in `src/adapter.c` reads it, and writes a new
one when the file has no key.

This has no effect today. `load_irk` has one caller, `set_privacy`,
and `set_privacy` asks for the local IRK only when privacy is on. The
pod's `main.conf` sets no `Privacy` key, so `parse_privacy` in
`src/main.c` leaves `btd_opts.privacy` at `0x00` and bluetoothd never
reads or writes the file. The lab machine has no `identity` file at
all.

The problem starts when somebody turns privacy on. The adapter then
generates a new IRK after every restore, because the `Secret` holds no
old one to restore. So it presents a new identity to each peer that
had resolved the previous one.

A *peer* device's IRK is safe. `store_irk` writes it into that
device's own `info` file, which the `Secret` already copies.

**What has to be decided.** Either add `identity` to the set, or state
that the operator supports privacy off only and write `Privacy = off`
into `main.conf`. The second choice records the current behaviour as a
decision, not as the absence of one.
