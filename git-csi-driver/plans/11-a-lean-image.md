# 11, A lean image

Low fidelity. This plan records the measurements and the shape of
the work. It is raised to full fidelity before anyone builds it.

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
`git`, `ssh`, every library the two of them load, and the CA bundle,
and nothing else. A closure script, `git-closure.sh`, collects the
tree in a Debian builder stage the way `audio-closure.sh` does for
`audio-operator`. The script walks `ldd` for the two programs and
the git helper programs the driver's commands run, and copies the
files it names.

What the walk does not find, and the script names by hand:

- **The git helpers.** `git` runs some of its commands as separate
  programs from its exec path, `/usr/lib/git-core`. The driver's
  commands that do so are `git gc`, which runs `git-repack`,
  `git-pack-refs`, and `git-prune`, and a fetch or push over HTTPS,
  which runs `git-remote-https`. The drills are the runtime trace
  that settles the list: a drill that fails on a missing helper names
  it.
- **`/etc/passwd`.** `ssh` refuses to run for a uid with no passwd
  entry, and `git` needs one to name a committer when the driver's
  author variables are unset. The closure writes a one-line file for
  the uid the pod runs as.
- **The CA bundle.** `ca-certificates` builds
  `/etc/ssl/certs/ca-certificates.crt` from many files. The closure
  copies the one bundle and points git at it with `GIT_SSL_CAINFO`,
  or ships the certificate directory git's libcurl reads by default.
- **`/tmp`.** The driver writes a key file in the volume's own
  directory for the life of one git call, and nothing under `/tmp`,
  so the closure ships no `/tmp`. `git` and `ssh` may still open one:
  the drills say.

What the closure gives up: no shell in the image, so `kubectl exec`
into the driver pod runs `git` by name and nothing else. The lab and
the drills never exec into the driver pod today.

### The measurement that decides it

The plan is done when the image is measured, and the target is under
80 MB: about 48 MB for the binary, about 25 MB for git, ssh, and
their libraries, and the bundle. If the closure lands over 100 MB,
the walk pulled a tree it should not have, and the script's comment
says which package to look at.

## The drill

Every drill in the lab runs against the closure image, because the
lab is the runtime trace that finds a missing helper. Drill 06 pushes
over the git protocol, so a drill or a unit test that fetches over
HTTPS from a public forge and pushes over ssh with a deploy key is
added, or the release gate asserts `git-remote-https` and `ssh` by
name the way `audio-operator`'s gate asserts its plugin.

## What is done when

- The binary is built with `-s -w`, and `--version` still names the
  release.
- The image is `FROM scratch` with the closure, and `docker image ls`
  reports it under 80 MB.
- A release gate asserts the helpers by name.
- Drills 03, 06, 07, and 08 pass in the lab on the closure image.
- The `Dockerfile` header no longer promises the closure as a later
  plan.
