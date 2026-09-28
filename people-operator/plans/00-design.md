# The people-operator design

## The problem

Kubernetes has no user object. A user is a name on a certificate or a
token, and RBAC binds to that name. The people who use a `liken`
cluster's screens are not users in that sense: someone picks up a
remote, someone is halfway through a series, someone picks the film.
An operator that wants to record a fact about one of them has no
resource to attach it to.

## The `Person`

`Person` is a cluster-scoped CRD in `people.liken.sh/v1alpha1`. It
holds a display name, a nickname, a picture, a Linux uid, and a link
to a login at an outside identity provider. It says nothing about a
person's age or what they may watch. No controller ships with it.

Other operators refer to a `Person` by its name and attach their own
facts to it, the way workloads refer to a `ServiceAccount`. The first
two references are in `library-operator`:

- A `Watch` names a set of people on one item, and progress belongs
  to that set.
- A `Play` carries the people who watched it as owner references, so
  the garbage collector removes a person's plays when the person is
  deleted.

## The uid

Linux and the storage under `liken` identify a person by uid. A share
on a NAS grants access by uid, and every file is owned by one. A
`Person` that has a uid from the start owns every file written for
them. A person sets their own in `spec.uid` when their files already
exist under it. Nothing assigns one yet. No machine resolves a
`Person`'s name to a uid: `liken` hosts have no logins, so there is no
NSS and no `/etc/passwd` entry.

## What comes later

Each of these is a plan of its own:

- A controller that assigns a uid from a reserved range to every
  `Person` that states none.
- A client certificate per `Person`, signed through the
  `CertificateSigningRequest` API, so a person can run `kubectl` under
  their own name and a `RoleBinding` can name them.
- A use for `spec.identity`, so a login at an outside provider maps to
  the same `Person`.

This repository does not become an identity provider. Issuing logins
is a separate project's job.

## Dependencies point one way

Nothing in this repository reads another operator's resources. Every
operator that names a `Person` depends on this CRD and on nothing
else here.
