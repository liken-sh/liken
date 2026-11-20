---
name: install
description: "Install the Person resource definition and people-operator from its kustomize base, and declare the people of a cluster. Use when a cluster needs Person objects for other operators to refer to, or when a running operator must be upgraded or removed."
---

This skill is the guide at https://liken.sh/people/docs/guides/install/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Install the operator

You install the resource definition and the operator from the
kustomize base in the operator's `deploy/` directory. You need
`kubectl` with cluster-admin rights. The base puts the operator in the
`liken-system` namespace.

Add the base to your own kustomization, and pin `<ref>` to a release
tag so that every apply installs the same definition and the same
operator image:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/liken//people-operator/deploy?ref=<ref>
```

Apply it with `kubectl apply -k` and the directory of your
kustomization. The base contains:

- the cluster-scoped `Person` resource definition;
- the `people-operator` `ServiceAccount` and its `ClusterRole`, which
  let the operator read every `Person`, write each `Person`'s status,
  create, read, and delete the pods that read a picture from NFS or a
  claim, and post `Event`s about each `Person` in the `default`
  namespace;
- a `Role` in `liken-system` that lets the operator create, read, and
  renew the `Lease` named `people-operator`;
- the `people-operator` `Deployment`, with one replica.

Only the copy of the operator that holds the `Lease` does any work,
so you can run more than one replica, and a rollout does not start
two copies that act at the same time. The other copies wait and take
the `Lease` when the leader stops. To see which pod leads, run
`kubectl -n liken-system get lease people-operator`.

Watch the operator start:

```sh
kubectl -n liken-system rollout status deployment/people-operator
```

## Declare the people

Other resources refer to a person by the name of the `Person` object,
so choose the name carefully. Renaming a `Person` later breaks every
reference to it. A screen shows `displayName`, and `nickname` is a
short form of it for a screen with little room.

```yaml
apiVersion: people.liken.sh/v1alpha1
kind: Person
metadata:
  name: ada
spec:
  displayName: Ada Lovelace
  nickname: Ada
```

`kubectl get people` lists them. Within a few seconds the `Picture`
column shows `Initials`, which means the operator has drawn the
person's initials into `status.thumbnail`. The
[pictures](https://liken.sh/people/docs/guides/pictures/) guide shows how to give a person a picture
instead.

## Run a development build

Every push to `main` publishes a development build of the image. Set
`<ref>` in the base URL to the commit, and set the image tag to the
build's tag with an `images` entry in your kustomization:

```yaml
images:
  - name: ghcr.io/liken-sh/people-operator
    newTag: <tag>
```

The operator starts the pods that read pictures from its own image,
so this entry sets the image for both.

## Remove the operator

Delete the `Deployment` to stop the operator. Each `Person` keeps its
status, so the screens keep showing the thumbnails in it, but a
picture that changes after that does not reach the screens.

```sh
kubectl -n liken-system delete deployment/people-operator
```

Delete the base to remove the definition too. That deletes every
`Person`, and the garbage collector then deletes every `Play` that has
only `Person`s as its owners.

The operator creates its `Lease` itself, so the base does not include
it, and it stays after you delete the base. Delete it with
`kubectl -n liken-system delete lease people-operator`.
