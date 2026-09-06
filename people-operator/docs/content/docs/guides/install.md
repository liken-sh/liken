---
title: Install
weight: 10
---

Install the resource definition from the kustomize base in the
repository's `deploy/` directory. You need `kubectl` with
cluster-admin rights. A `Person` is cluster-scoped, so the base sets
no namespace.

Add the base to your own kustomization and pin `<ref>` to a release
tag. Until the first release, pin the full commit sha instead. A
pinned ref installs the same definition every time it is applied.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/people-operator//deploy?ref=<ref>
```

Then declare each person. The object's name is the name every other
resource uses to refer to this person, so choose it once. A change
to it later breaks every reference. The display name is what a
screen shows, and the nickname is a one-word form of it.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: thora
spec:
  displayName: Thora
  nickname: Thora
```

`kubectl get people` lists them. Nothing writes a `Person`'s status
yet.
