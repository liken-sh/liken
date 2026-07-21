---
title: liken
---

# `liken`

`liken` is a small operating system that boots a machine straight
into Kubernetes.

The whole thing is one repository: a Go init written for this
project, plus the Linux kernel and k3s from their upstream releases,
assembled into a bootable image. You can read it and see exactly what
your machine runs.

[The repository is on GitHub.](https://github.com/liken-sh/liken)
This page is served by a `liken` cluster built from it.

Releases live at [releases.liken.sh](https://releases.liken.sh/).
[The manual](/docs/) walks the path from a release to a running
cluster of your own.
