---
title: library.liken.sh
---

# `library.liken.sh`

`library-operator` is a Kubernetes operator for the media libraries
of a cluster. It runs on a [`liken`](https://liken.sh/docs/) cluster
above [`media-operator`](https://media.liken.sh), which owns the
players, the plays, and the remotes. This operator owns what there is
to play: it declares each library as a resource, keeps a catalog of
what the library holds, and puts a media browser on every screen.

This site is a scaffold. The manual arrives with the plan that
documents the first working release.

* [The repository](https://github.com/liken-sh/library-operator)
* [The `liken` manual](https://liken.sh/docs/)
