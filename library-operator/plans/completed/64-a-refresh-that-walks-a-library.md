# 64, A refresh that walks a library

Built on 2026-09-19. `spec.refresh` takes one more key, `scan`, which
asks for one full walk of the library. `kubectl liken library rescan`
writes it, and the operator stands one walk `Job` while the request is
newer than the last walk. A test reads the CLI's copy of the fact list
against the operator's, so the two cannot drift.

## The problem

A Library's full walk runs from the `CronJob` the operator stands on
`spec.scan.schedule`, and a webhook runs a `Job` for one folder. Neither
is a field a person can set on the `Library`, so `kubectl liken library
rescan` was a stub with no server side to write.

## The design

`spec.refresh` becomes the one vocabulary for "run this again". Its keys
are refresh targets. A key named for a fact keeps its meaning: an attempt
older than the time no longer closes that fact's gap. The key `scan` is
new, and it means one full walk. A walk that starts at or after the time
answers it.

Two lists carry the names. `factVocabulary` is unchanged: the facts a
`MetadataProvider` may serve and a container runs. `refreshVocabulary`
is the facts plus `scan`, and it is the list the CRD's refresh rule
validates against. `isContainerFact` separates the two, so the
enrichment cause and the enricher pod's `LIBRARY_REFRESH` see the facts
alone.

The operator reads the request in `walkRequested`: the time under
`scan` is after the reporter's `workerScan` run's start, and no scan
`Job` of the `Library` is unfinished. A walk that began before the
request does not answer it, because that walk may have read the volume
before the person asked.

`serveRequestedWalk` stands the walk as a `Job` built from
`scanJobSpec(library, "", ...)`, which is the full walk the `CronJob`
runs. The `Job`'s name comes from the request time, so two passes over
one request build the same name and the pass that loses the create reads
the conflict as success. The `Job` carries the scan worker's label and
no chain marks, because it is a walk and not the webhook's scan, enrich,
and rescan chain.

The CLI writes the request through `patchLibraryRefresh`, which is the
version check and the write `reenrich` already used. `rescan` builds
only its patch.

## What was set aside

* A field on the scan block, `spec.scan.now`. It is a smaller change,
  and it leaves `spec.refresh` a name that covers facts alone.
* A list of request objects under `spec.refresh` instead of a map. More
  room for a target's own fields, and more schema for one timestamp.
* A pseudo-attempt row for the walk, so that one comparison would answer
  both kinds of target. A walk has no title to key an attempt, and its
  record is already the `runs` row.

## The name

The unified vocabulary puts three words for the same work close
together. The key is `scan`, the CLI verb is `rescan`, and the runs
worker for a single folder is also `rescan`. So `kubectl liken library
rescan` writes `refresh.scan` and produces a `scan` row, while the runs
table already uses `rescan` for one folder. This plan does not fix that.
It records it.

## How the work is proved

The tests hold the vocabulary, the request rule, the `Job`'s shape, and
the create-once behavior. `TestTheCLIFactListIsTheOperators` reads
`cli/facts.go` and compares its list to `factVocabulary`, which is the
drift guard the open problem asked for. The CRD is read back and its
refresh rule compared to `refreshVocabulary`.

The drill runs on `liken-1` after the development build rolls there:
`kubectl liken library rescan franchises`, then `movies`, and the walk
`Job` appears and its run reaches `status.runs`. That measurement goes
here when it runs.
