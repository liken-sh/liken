# library-operator

`library-operator` keeps a catalog of the films and series on your
volumes, and draws a media browser on the screen of each `Player` that
you give it. You declare each directory of media as a `Library`. A
person picks a title on the screen with the remote, and the browser
starts it playing on that `Player`. To turn the browser on for a
`Player`, set the `Player`'s `spec.idle.controller` to
`library.liken.sh/media-browser`. A `Player` without that setting
keeps `media-operator`'s idle screen.

The operator scans each `Library` into the namespace's catalog. It
fetches titles, plots, ratings, art, and people from the metadata
providers that you list, and writes them beside the media as the
`.nfo` files and art files that Kodi and Jellyfin read. It also makes
the images for the seek bar, finds the credited people on screen,
finds where the end titles start, and records each person's progress
in each title. A webhook, such as one from Sonarr, makes the operator
rescan the folder that the webhook names. The files on the
volume are the source of truth. The operator reads and writes the
media, the `.nfo` files, and the art. The catalog is an index built
from those files, so the operator can rebuild it from them at any
time. The operator does not play media or control the equipment. It
also does not create the claim that stores your media.

Install [`media-operator`](https://liken.sh/media/) first. Each
`Play` that this operator creates is a `media-operator` kind, and the
operator, its `Job`s, and its screens use `media-operator`'s message
bus. [Corrosion](https://github.com/liken-sh/corrosion), from
`liken`'s fork, copies the catalog to each screen and each `Job`.
A `Library` can mount any `PersistentVolumeClaim` that the cluster
can mount. A `StorageClass` of
[`per-node-csi-driver`](https://liken.sh/per-node/) lets the operator
run more than one copy of the catalog. A `Person` of
[`people-operator`](https://liken.sh/people/) identifies who watched
a `Play`. A `Library` can request a GPU through a
`ResourceClaimTemplate`. The GPU decodes video for the seek bar
images, the credited people, and the end titles. Without a GPU, that
work runs on the CPU.

The operator defines these resources:

- A `Library` is one root directory of media on a volume, of one kind,
  such as films, or a checkout of franchise files that show the films
  and series of one story in story order. It lists the providers to ask,
  in order, and the work to run on its videos.
- A `Catalog` is the namespace's shared catalog. It creates the pods
  that store the catalog of record, the copies that every other
  agent syncs from. It sets the size of every claim that stores a
  copy. It can also name a Jellyfin server, and progress then syncs
  with that server in both directions.
- A `MetadataProvider` is one account with one metadata provider, such
  as TMDb, with the `Secret` that contains its key.

This `Library` reads the films under `/media/movies` on the claim
`movies-pvc`:

```yaml
apiVersion: library.liken.sh/v1alpha1
kind: Library
metadata:
  name: movies
  namespace: media
spec:
  storage:
    claim: movies-pvc
    root: /media/movies
  kind: movies
  movies: {}
```

The manual is at [liken.sh/library](https://liken.sh/library/). It
has the install guide, the guides, and a reference for each resource. [`plans/00-design.md`](plans/00-design.md) is the design.

MIT. See [LICENSE](LICENSE).
