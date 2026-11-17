# The node password is minted after a step that can fail

Open bug. When init cannot write the `/etc/rancher/node` symlink, it
skips the mint of the node password, so k3s writes its own password
with a torn-write window. A power cut in that window locks the machine
out of its cluster.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`persistNodePassword` (`init/k3s.go:603`) returns early when the
symlink write fails (`init/k3s.go:616`), and `mintNodePassword` never
runs. That mint exists to stop k3s from writing its own password with
a torn-write window that locks a machine out of its cluster.

## The fix

Minting first does not help. When the symlink fails, k3s reads its
password on the RAM root, not the minted file, and on a first join the
next boot would present a password that the leader never recorded. The
symlink fails only when its path already exists, so the fix accepts a
symlink that already names the same directory, and treats any other
failure as a boot fault.
