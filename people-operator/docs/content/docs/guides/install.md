---
title: Install
weight: 10
---

The CRD installs from the kustomize base in the repository's `deploy/`
directory. You need `kubectl` with cluster-admin rights. The resource
is cluster-scoped, so the base names no namespace.

Take the base into your own kustomization and pin `<tag>` to a
release, so the install is the same every time it is applied.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/people-operator//deploy?ref=<tag>
```

Then declare each person. The name is what other operators reference,
so choose it once. The display name is what a screen shows.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: thora
spec:
  displayName: Thora
  child: true
```

`kubectl get people` lists them. No controller runs, so a `Person` has
no status until a later plan adds one.
