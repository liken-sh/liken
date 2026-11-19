# The machine permission tests fail as root

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Several tests of `liken/machine` make a read or a write fail by
removing permission bits with `chmod`. Root bypasses those bits, so
when the tests run as root the read or write succeeds and each test
that expects an error fails. CI runs the tests as a user that is not
root, so CI does not show it. A run inside a container as root does.

## The evidence

The helpers in `machine/helpers_test.go` remove permissions:

- `readOnlyDir` sets a directory to `0o555`.
- `sealedStore` sets the manifests directory to `0o555`.
- `unreadableFile` sets a file to `0o000`.

Their callers expect an error: `machine/staging_test.go` (lines 341,
348, 355, 392, and 399), `machine/machine_test.go:178`,
`machine/imports_test.go:125`, and `machine/reboot_test.go:183`. None
of these tests checks the effective user. `init/rootimage_test.go:364`
in the same component checks `os.Geteuid() == 0` for this reason.

## What would confirm it

Run `go test ./machine/` as root, for example in the `golang` image of
`toolchains/images.toml`. If the tests that use these helpers fail,
the bug is confirmed.

## What a fix has to decide

Whether each test skips as root, as `init/rootimage_test.go` does, or
whether the helpers make the failure in a way that root cannot bypass,
such as a directory where a file is expected, so the tests run for
every user.
