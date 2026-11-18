# Without `known_hosts`, ssh accepts every host key

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

The manual says that a credential `Secret` with `ssh-privatekey` and
no `known_hosts` makes the driver accept the first host key that it
sees and refuse a later change of that key. `guides/read-only.md`
says it under "Private repositories", and `reference/attributes.md`
says it in the row for `known_hosts`.

The code does not keep a host key. `credentials.use` in
`credentials.go` writes `known_hosts` from the `Secret` before each git
command, which is an empty file when the `Secret` has no key, and
deletes it after the command. `sshCommand` passes
`StrictHostKeyChecking=accept-new` with that file. ssh accepts the
host key and writes it to the file, and the driver then deletes the
file. The next fetch or push starts with an empty file again. So a
changed host key is accepted on every command, and nothing records
the first one.

## What would confirm it

A test in `credentials_test.go` that runs two git commands against the
SSH server of the existing tests, with a different host key for the
second. Both succeed.

## What a fix has to decide

Whether the driver keeps the first host key, and where. A file in the
volume's directory outlives the command and the driver restart, and
the remote's key change then needs a way for a person to clear it.
The other choice is to change the manual to say that without
`known_hosts` the host key is not checked, or to refuse an
`ssh-privatekey` without `known_hosts`.
