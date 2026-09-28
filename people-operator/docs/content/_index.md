---
title: people.liken.sh
---

A `Person` names one person who uses a `liken` cluster. It is a
cluster-scoped resource with a display name, a short name, a picture,
a Linux uid, and a link to an outside login. `people-operator` is the
definition of that resource and nothing more. No program runs.

Other operators refer to a `Person` by name and attach their own
facts to it. The library operator's `Watch` names the people who
watch a series together, and a `Play` names the people who watched
it. This site describes the `Person` alone.

Start with the [manual](docs/). The design and the plans are in the
[repository](https://github.com/liken-sh/people-operator).
