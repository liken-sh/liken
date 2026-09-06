# 11, A lean image

## The problem

The release image `2026.09.06-001` is 263 MB on disk. It is three
layers:

| Layer | Size |
|---|---|
| `debian:trixie-slim` | 79 MB |
| `git`, `openssh-client`, `ca-certificates` and what apt pulls with them | 115 MB |
| The driver binary | 69 MB |

Every node pulls the image on every release, and a `liken` machine
holds its images in RAM or on a small disk. The driver runs two
programs from that image, `git` and `ssh`, and reads one file, the
CA bundle. The rest is a Debian userland nothing runs.

## The design

### The binary is stripped

`go build -ldflags "-s -w"` drops the symbol table and the DWARF
data. Measured on the current tree with Go 1.27:

| Build | Size |
|---|---|
| As released | 68,995,731 bytes |
| With `-s -w` | 48,255,136 bytes |

The driver reports its version through `--version` and its log, and
a panic's stack trace keeps its function names without the symbol
table, so nothing the project reads is lost.

### The image is a closure on scratch

The final stage is `FROM scratch` with a directory tree that holds
`git`, `ssh`, `/bin/sh`, every library the three of them load, the
CA bundle, and nothing else. A closure script, `git-closure.sh`,
collects the tree in a Debian builder stage the way `audio-closure.sh`
does for `audio-operator`. The script walks `ldd` for each seed
program and copies every file the walk names, with every link of a
symlink chain.

What the walk does not find, and the script names by hand, each with
the driver command that reaches it:

- **The git on the exec path.** `git` re-execs `/usr/lib/git-core/git`
  as `git <subcommand>` for the work a command farms out. `git gc` runs
  maintenance, pack-objects, and rev-list that way, a fetch runs
  index-pack and rev-list, a push runs pack-objects, and a rebase runs
  reset and notes copy. `git gc` runs no `git-repack`, `git-pack-refs`,
  or `git-prune` program, which the low-fidelity plan named: git 2.47
  farms every step out through its own binary. Debian ships that
  binary as a second 4 MB copy, byte for byte the `/usr/bin` one, so
  the closure replaces the copy with a link.
- **`git-remote-https`**, for any https URL, a link to
  `git-remote-http`. **`git-upload-pack` and `git-receive-pack`**, for
  a URL that is a local path, both links to git on the exec path.
- **`/bin/sh`.** The low-fidelity plan said the image holds no shell,
  and that was wrong. git runs three things through `/bin/sh`: the
  credential helper the driver writes, which is a `#!/bin/sh` script,
  `GIT_SSH_COMMAND`, which git always passes to a shell, and the
  local-path transport's `git-upload-pack '<path>'` line. Without a
  shell, no token push, no deploy-key push, and no local-path fetch
  works. The image holds dash for that reason, and `kubectl exec` into
  the driver pod gets it too, with no other program beside git and
  ssh.
- **`/etc/passwd` and `/etc/group`.** `ssh` refuses to run for a uid
  with no passwd entry. The manifests in `deploy/` set no `runAsUser`,
  so the pods run as root, and the file also covers 65534.
- **The CA bundle** at `/etc/ssl/certs/ca-certificates.crt`. Debian
  builds that path into libcurl as its default, so git needs no
  `GIT_SSL_CAINFO`, and the image sets none.
- **The git templates directory**, 88 kB, because `git init` prints a
  warning without it, and the driver runs `git init` for the store and
  for every work tree.
- **The loader's cache**, from `ldconfig`, so the loader finds the
  multiarch directory.

Not shipped, and confirmed unnecessary: `/tmp`, because the driver's
one temporary file goes in its own store, and the name service files
and modules, because glibc 2.41 resolves files and DNS without them.

### The measurements

| | Bytes |
|---|---|
| The closure tree | 31.6 MB |
| The binary with `-s -w` | 48,259,232 |
| The image, `docker image ls` | 79.9 MB |
| The image before | 263 MB |

The largest files in the closure are `libcrypto.so.3` at 6.5 MB,
`git` at 4.1 MB, `git-remote-http` at 2.4 MB, and `libgnutls`,
`libunistring`, and `libc` at about 2 MB each.

### The release gate

The `Prove the closure` step in `release.yaml` runs after the image is
built and before anything is pushed. It lists the image's files and
asserts by name every program and file above, runs `--version`,
`git --version`, and `ssh -V`, fetches `HEAD` of the public repository
over https, which runs `git-remote-https` and verifies GitHub's
certificate against the copied bundle, and runs `git gc` in a fresh
repository through `/bin/sh`, which runs the git on the exec path.

## The drill

Every drill in the lab runs against the closure image, because the
lab is the runtime trace that finds a missing program. A local trace
before the first push ran every git command in the driver's call
graph inside the image, as root and as uid 65534, under the driver's
own environment: init, fetch over https with and without depth,
worktree add and remove, rebase and abort, read-tree, commit-tree,
gc, for-each-ref, update-ref, and a credential helper script. `ssh`
against the public forge with no key failed on the host key and the
key, and not on a missing user or library.

## What is done when

- The binary is built with `-s -w`, and `--version` still names the
  release.
- The image is `FROM scratch` with the closure, and `docker image ls`
  reports it under 80 MB.
- The release gate asserts the programs and files by name and runs
  git over https and `git gc` in the image.
- Drills 03, 06, 07, and 08 pass in the lab on the closure image.
