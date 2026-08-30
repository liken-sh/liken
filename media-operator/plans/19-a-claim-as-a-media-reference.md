# A claim as a media reference

Plan 19. A third URI scheme, `claim://`, names a `PersistentVolumeClaim`
in the `Play`'s namespace and a path inside it. At the end of this plan
a `Play` plays a file from any volume the cluster can mount, and its
spec names no server.

## The problem

A `Play` names its media by URI, and the resolver serves two schemes:
`https://`, which mpv streams, and `nfs://`, which the kubelet mounts
with the kernel's NFS client. Both name an address. A Longhorn volume
or a disk on one node has no address, so a file on one has no URI a
`Play` can carry.

The library layer above this operator makes the gap concrete. A
`Library` binds to a claim, and its scanner mounts that claim and
records paths under it, so the catalog knows a file by its path on a
volume and never by a server. To build an `nfs://` reference, the
library operator reads the `PersistentVolume` behind the claim and
copies the server and the export into every `Play`. That works for NFS
alone, and it puts the storage server's name into every `Play` on the
cluster.

## The contract

**The scheme.** `claim://<claim>/<path>` names the claim `<claim>` in
the `Play`'s namespace and the file at `<path>` inside it. The `logo`,
`art`, and `trickplay` fields take the same form. The `Play` spec
changes no shape: an item is still `{uri}`, and the description of
`uri` gains the third scheme.

**The mount.** The resolver mounts each claim a playlist names once,
read-only, at a numbered path under `/media/`, the way it mounts each
NFS server's common ancestor once, and rewrites every path under that
mount. A film, its logo, and its trickplay tiles on one claim cost one
mount. The pod's volume is `persistentVolumeClaim` with `readOnly:
true`, so a playback pod cannot write to a library volume whatever mpv
does.

**Same namespace, as every reference.** The claim must exist in the
`Play`'s namespace. The resolver checks nothing; a claim that is absent
or unbound parks the pod as `Pending`, and the `Play`'s status carries
the kubelet's own message, which names the claim. A `Player` therefore
plays from any claim beside it, and the library layer creates a `Play`
that names the library's own claim.

**No `PersistentVolume` is read.** The operator reads no claim and no
volume. The kubelet resolves the claim when it starts the pod, so the
operator's RBAC gains no rule, and the pod is the only object that
names the claim.

**The two schemes stay.** `https://` is a stream, and `nfs://` is a
path a person types by hand for a one-off. A library `Play` uses
`claim://`, and nothing rewrites an existing `Play`.

## What was set aside

An item object with `claim` and `path` fields beside `uri`. It would
carry the same two facts in a second shape, and every reader of an
item would grow a branch. The resolver is the extension point the
design names, and a scheme is one entry in it.

A reference that names the `Library`. `media-operator` reads nothing of
the library layer's, and a `Play` that named a `Library` would reverse
that. The claim is the object both layers already share.

A `ReadWriteOnce` volume on two nodes. A Longhorn volume mounts on one
node at a time, so a scanner on one node and a screen on another cannot
both hold it. No reference form fixes that; it is the volume's own
rule, and `ReadOnlyMany` and NFS volumes are unaffected.

## Proof

On `liken-1`: a `PersistentVolume` over the lab's movie export and a
`ReadOnlyMany` claim on it in the `Play`'s namespace. A `Play` whose
item, logo, and trickplay are `claim://` URIs on that claim starts
within the same time an `nfs://` `Play` starts, draws the logo, and
scrubs with the thumbnails, and the pod carries one read-only claim
mount and no `nfs` volume. A `Play` that names a claim that does not
exist parks `Pending` and its status names the claim. The existing
`nfs://` plays in `liken-1/plays` still run.
