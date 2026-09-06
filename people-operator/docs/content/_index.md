---
title: people.liken.sh
---

A `Person` is a fact of a `liken` cluster: someone the household
names once, so every operator can name them again. `people-operator`
ships that one cluster-scoped resource and no controller.

The resource is small because its value is in who references it. A
`Watch` in the library operator names the people who share a series.
A `Play` names the people who watched it. Each reference is a plan in
the operator that makes it, and this site documents only the `Person`.

Start with the [manual](docs/). The design and the plans are in the
[repository](https://github.com/liken-sh/people-operator).
