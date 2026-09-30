---
name: install
description: "Install the Person resource definition and people-operator from its kustomize base, and declare the people of a cluster. Use when a cluster needs Person objects, which other operators name as the owners of playback progress, or when a running operator must be upgraded or removed."
---

This skill is the guide at https://liken.sh/people/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

Install the resource definition and the operator from the kustomize
base in the operator's `deploy/` directory. You need `kubectl` with
cluster-admin rights. The base puts the operator in the
`liken-system` namespace.

Add the base to your own kustomization and pin `<ref>` to a release
tag. A pinned ref installs the same definition and the same operator
image every time you apply it.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/liken//people-operator/deploy?ref=<ref>
```

The base holds three parts:

- the `Person` resource definition, which is cluster-scoped;
- the `people-operator` `ServiceAccount` and its `ClusterRole`, which
  reads every `Person`, writes each `Person`'s status, and starts,
  reads, and deletes the pods that read a picture from NFS or a
  claim;
- the `people-operator` `Deployment`, with one replica.

Watch the operator start:

```sh
kubectl -n liken-system rollout status deployment/people-operator
```

## Declare the people

The object's name is the name every other resource uses to refer to
this person, so choose it once. A change to it later breaks every
reference. `displayName` is what a screen shows, and `nickname` is a
one-word form of it.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: ada
spec:
  displayName: Ada Lovelace
  nickname: Ada
```

`kubectl get people` lists them. Within a few seconds, the `Avatar`
column shows `True`: the operator drew the person's initials into
`status.thumbnail`. The [pictures](https://liken.sh/people/docs/guides/pictures/) guide shows how to
give a person a picture in place of the initials.

## Run a development build

Every push to `main` publishes a development build of the image. Pin
the base to the commit and the image to the build's tag with an
`images` entry in your kustomization:

```yaml
images:
  - name: ghcr.io/liken-sh/people-operator
    newTag: <tag>
```

The operator reads its own image from its pod, and each pod that
reads a picture runs the same image, so the one entry sets both.

## Remove the operator

Delete the `Deployment` to stop the operator. Each `Person` keeps its
status, and the screens keep the pictures in it. A picture that
changes after that does not reach the screens.

```sh
kubectl -n liken-system delete deployment/people-operator
```

Delete the base to remove the definition too. That deletes every
`Person`, and with them every `Play` that names one as its owner.
