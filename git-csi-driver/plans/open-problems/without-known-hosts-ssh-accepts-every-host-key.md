# Without `known_hosts`, ssh accepts every host key

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test used credentials with a private key and no `known_hosts`. It
wrote the credential files, wrote a host key into the `known_hosts`
file as `StrictHostKeyChecking=accept-new` does, removed the files as
the end of a git command does, and wrote the credential files again.
It wanted the second `known_hosts` file to hold the first host key,
and failed with:

```
known_hosts for the next command holds "", want "[git.example.com]:22 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFirstKeyOfTheHost\n" because the manual says a later change of the first host key is refused
```

The test ran no ssh process.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

For a credential with no `known_hosts`, should the driver keep the
first host key it sees, refuse the `Secret`, or should the manual say
that the host key is not checked?

Whether the driver keeps the first host key, and where. A file in the
volume's directory outlives the command and the driver restart, and
the remote's key change then needs a way for a person to clear it.
The other choice is to change the manual to say that without
`known_hosts` the host key is not checked, or to refuse an
`ssh-privatekey` without `known_hosts`.
