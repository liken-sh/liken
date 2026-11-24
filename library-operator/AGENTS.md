# Working on library-operator

This directory contains `library-operator`, a Go Kubernetes operator
with a media browser and video tools in Rust. `README.md` says what
it is for and describes its kinds.

## How it runs

The operator is a `Deployment`, and a Lease makes sure that only one
replica acts at a time. The operator is the only process of this
component that has a Kubernetes credential. It writes every status
and creates every pod, `Job`, and `Play`. It serves the webhook that
Radarr, Sonarr, and Jellyfin post to, and it starts a scan of the
folder that each post names. The pods and `Job`s run the pod build of
this program, which is the same code built with the tag `pod`.

Each namespace has a Corrosion cluster for the catalog and another
for the progress store, the database of each person's progress in
each title. Each Corrosion agent stores a full SQLite copy of its
database. The catalog pods publish a report of each `Library` on the
bus, and the operator writes the report into the `Library`'s status.

Each `Library` runs one `Job` at a time. The `Job` scans the library
and then enriches it, with one container for each phase, and it
writes the `.nfo` and art files to the media volume. Some facts about
a video need heavy processing, such as its trickplay images. For each
of those, the `Job` publishes a list of the videos that still need
it, and the operator starts a worker `Job` for the list. A worker
writes only files on the volume, and the next scan of the library
reads them into the catalog.

For each `Player` that hands its idle screen to this operator, a
screen pod runs the media browser from `media-browser/`, beside one
Corrosion agent for each cluster. The browser reads its own copy of
the catalog and reads presses from `media-operator`'s bus. When a
person picks a title, the browser publishes it, and the operator
creates the `Play`.

## Design

The files on the media volume are the source of truth. The catalog is
an index built from them, so if the catalog is lost, a rescan
rebuilds it. Each phase of a `Job` and each worker writes to the
volume, not to the catalog. The pods and `Job`s have no Kubernetes
credential. So each fact that they report goes over the bus, and the
operator writes it to the API server.

A log line identifies a title only by its catalog id. It never
contains a title, a person from the credits, a path, or the size of a
library. A media library's titles are private, and people copy log
lines into the issues of a public repository.

This operator reads `media-operator`'s kinds and uses its bus and its
contract for handing over the idle screen. `media-operator` reads
nothing from this operator. When this operator needs a change in
`media-operator`, a plan here describes the change and says which
component makes it.

## Finding things

- `logcontent_test.go` checks the log line rule against the lines that
  the `Job`s and the operator write.
- `corrosion/schema/` and `corrosion/progress-schema/` contain the
  schemas that every agent loads, and the Corrosion image includes
  them.
- `cli/facts.go` has a copy of the operator's `factVocabulary`, so
  when you add a fact to the operator, add it there too.
- The browser and `media-operator`'s command sidecar read the cases of
  the subtitle rule from `media-operator/testdata/subtitle-rule.json`.
  The browser's `tracks/` has the choice of a track. The roles, the
  language names, and the format words that the browser draws are in
  `media-screen`, which `media-operator`'s display also links.
