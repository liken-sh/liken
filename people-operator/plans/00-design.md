# The people-operator design

## The problem

Kubernetes has no user object. A user is a name on a certificate or a
token, and RBAC binds to that name. A `liken` cluster in a house has
people in it: someone picks up a remote, someone is halfway through a
series, someone picks the film. Every operator that wants to say "for
this person" has nowhere to point.

## The `Person`

`Person` is a cluster-scoped CRD in `people.liken.sh/v1alpha1`. It
holds a display name, a picture, a Linux uid, and a link to an
outside login. It says nothing about age or what a person may watch;
that is a later plan's, if the house wants it. That is the whole resource. No controller ships
with it.

The value is in the references. A `Person` is a subject the way a
`ServiceAccount` is: other operators name it and hang their own facts
on the name. The first three references are:

- `Watch` in `library-operator`, a set of people on one item, whose
  progress belongs to the set.
- `Play` in `media-operator`, which carries the people who watched it
  as owner references, so the garbage collector removes a person's
  plays with the person.
- `Player.spec.people` in `media-operator`, the people who usually
  watch on one unit.

## The uid

A uid is the one identity Linux and the storage under `liken` key
on. A share on a NAS grants access by uid, and a file written for a
person is owned by a uid. A `Person` that holds one from the start
owns every file written for them. A person states their own in
`spec.uid` when their files already exist under it. A later controller
assigns one from a reserved range when none is stated. No machine
resolves the name: `liken` hosts have no logins, so there is no NSS
and no `/etc/passwd` entry.

## What comes later

A controller that assigns uids. A signed client certificate per
`Person`, through the `CertificateSigningRequest` API, so `kubectl`
as a person is real and a `RoleBinding` to a `Person` means something.
An OIDC link through `spec.identity`, so a login at an outside
provider maps to the same name. The operator does not become an
identity provider; that is another project's job.

## Dependencies point one way

Nothing in this repository reads another operator's resources. Every
other operator that names a `Person` depends on this CRD and on
nothing else here.
