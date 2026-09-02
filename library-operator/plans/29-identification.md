# Identification

Plan 29. The first enrichment build from [plan 27](27-enrichment.md).
It answers one question for a fresh piece of media: what is it? A
title with a sidecar answers from the sidecar. A title with none gets
its answer from a provider search over the clues the volume holds, or
a list of candidates for a person to choose from. The plan also brings
the machinery every later concern runs on: the enricher `Job`, the
write package, `MetadataProvider`, and the gap loop.

## The problem

About a fifth of the lab's movies have no sidecar. Their id in the
catalog rests on the folder's path, so a move of the folder loses the
item, and no enricher can ask a provider about a title it cannot name.
Every other concern in plan 27 waits on an id. Identification is also
the one step with a person in the loop, because a search can be wrong,
and a wrong id sends the wrong film's plot and art to every concern
downstream.

## The clues

The scanner already reads the clues that cost nothing. `names.go`
cuts a title and a year out of a *arr release name, and reads the
season and episode markers off a file. `nfo.go` reads every
`uniqueid` a sidecar carries. Jellyfin also reads an id out of a name,
as in `Jellyfin Documentary (2030) [imdbid-tt00000000].mkv`, and the
scanner learns that form in this plan. In order from free to costly:

1. An id in the sidecar or in the name. No search.
2. The `Library`'s kind and the path: movie or series, and the season
   and episode. These narrow the search before any call.
3. The title and the year from the name. Enough for a search.
4. The runtime from the `probe` concern, which opens the file once.
   The tie breaker when the title and year match more than one result.

Tags inside a video container are not a clue in this plan. Video files
rarely carry a useful one.

## The contract

- **`MetadataProvider`.** The resource from plan 27, with a `tmdb`
  block first, because TMDb serves movies, series, and people. It
  lives in the `Library`'s namespace and names a `Secret` there. The
  operator mounts the key into the enricher `Job` as an environment
  variable, the normal way, so a worker never reads the API server.
  The operator checks the provider once per pass with one cheap call,
  and the `Ready` condition says `Reachable`, `NoSecret`, or
  `Refused`. A `429` from a provider is a cooldown inside the
  container, and nothing more. TMDb's limit is about 40 requests a
  second, checked on 2026-09-02, and this plan never comes near it.
- **The enricher `Job`.** One `Job` per `Library`, named
  `<library>-enrich`, on one claim per `Library` with one Corrosion
  native sidecar, the shape of the scan `Job` from plan 28. Concerns
  that must run in order are init containers, and concerns that may
  run at once are regular containers. This plan has two init
  containers, `probe` then `identity`, and no regular ones. Plan 30
  adds containers to this `Job` and never a second `Job`. The `Job`
  takes an optional `SCAN_PATH`, so a webhook's folder runs through
  the same containers as a full walk. Every container runs its gap
  query against the local copy, does its work, and exits zero. The
  last container writes the `runs` row for worker `enrich` and waits
  for the echo from plan 28.
- **The `probe` concern.** The first file concern. It opens a video
  file once with `ffprobe`, reads the container's duration, codecs,
  resolution, and audio layout, and writes them as `<streamdetails>`
  into the `.nfo`, creating a minimal `.nfo` where none exists. Its
  gap is a file with no `<streamdetails>` in the catalog. It needs no
  id.
- **The `identity` concern.** For a title with no provider id, a
  ladder of exact tests, and no score:
  1. Search TMDb by the title and the year. Keep results whose
     normalized title matches the name's title, or whose
     `original_title` does, and whose release year matches.
  2. If one remains, write it, with the reason `title and year`.
  3. If none remains, search again with the year on either side,
     because TMDb's `release_date` is the first release anywhere, and
     a December opening abroad carries a different year from the *arr
     name. The reason says so.
  4. If several remain and the probe has run, fetch each one's
     runtime and keep those within a few minutes of the file's. If
     one remains, write it, with the reason `title, year, and
     runtime`.
  5. Anything else is a candidate list.
  A sure answer is written as `<uniqueid type="tmdb">` into the
  `.nfo`, creating a minimal one where none exists, and the reason
  goes in `identity.yaml`. A candidate list goes in `identity.yaml`
  with each candidate's receipt: what matched and what did not. A
  person confirms a candidate by putting its `uniqueid` into the
  `.nfo`, or its `[tmdbid-<id>]` on the folder. The next scan reads
  either one, and the `aliases` table gains the id. `identity.yaml`
  is a ledger and never the truth.
- **Attempts.** Every container appends one attempt per item to its
  own file in `.liken/`, with the time and the kind: `found`,
  `candidates`, `nothing`, or `error`. Only `found`, `candidates`, and
  `nothing` are facts with a date, and the retry interval applies to
  them. An `error` is a provider that was down, a key that was
  refused, or a file that would not open, and the next run tries
  again.
- **The write package.** One package holds every write to the volume:
  create through a temporary file and a rename, an edit of a `.nfo`
  that inserts or replaces one element and leaves every other byte as
  it was, and the one remove, for a name that carries the
  `.liken-tmp-` mark. A test fails the build on any other remove,
  truncate, or rename in the enricher code. `liken` may edit any
  `.nfo`, whoever wrote it, because a title with no `uniqueid` is not
  one a person kept by hand. Enricher `Job`s mount the volume
  read-write, and scan `Job`s stay read-only.
- **The `.liken/` reader.** The scanner reads every file in a title's
  `.liken/` directory the way it reads the `.nfo`, and lifts the
  attempts into an `attempts` table keyed by library, item, and
  concern, and the candidates into a count. The directory is a dot
  name, so the walk skips it as a folder already, and Jellyfin
  ignores every path that matches `**/.*`.
- **The gap loop.** The reporter's report gains a `gaps` map with one
  count per concern, each from one query against the catalog. The
  operator is the only scheduler. Each pass, per `Library`, it
  creates the enricher `Job` when every one of these holds:
  1. No scan run and no enrich run is in flight, by the `runs` list.
  2. A scan has finished since the last enrich run finished, so the
     last run's writes have become rows and the counts are current.
  3. Some concern's count is above zero, and every concern that
     needs a provider has a `Ready` one in the `Library`'s `sources`.
  One enricher `Job` per `Library` at a time, whatever its scope. A
  webhook's folder chains through: folder scan, echo, folder enrich,
  echo, folder rescan.
- **`Library` status.** The count of titles that wait for a person,
  the count with no id after the provider was asked, and a printer
  column for the first.

## The files

```yaml
# .liken/identity.yaml, in a title's folder
items:
  - path: .
    id: {tmdb: 603}
    reason: title and year
    written: 2026-09-02T14:00:00Z
  - path: .
    candidates:
      - id: {tmdb: 11}
        title: Star Wars
        year: 1977
        receipt: {title: match, year: match, runtime: 3 minutes off}
      - id: {tmdb: 1893}
        title: Star Wars
        year: 1999
        receipt: {title: match, year: no match}
```

```yaml
# .liken/identity.yaml is the ledger; the attempts sit beside it
# .liken/probe.yaml
attempts:
  - path: Big Trouble in Little China (1986).mkv
    at: 2026-09-02T14:00:00Z
    result: found
```

In a season or album folder each file holds one entry per item under
it, keyed by the item's relative path.

## The `Job`

```yaml
kind: Job
metadata: {name: movies-enrich, namespace: media}
spec:
  template:
    spec:
      initContainers:
        - name: corrosion            # native sidecar, restartPolicy: Always
        - name: probe
          env: [{name: LIBRARY_ROLE, value: probe}]
        - name: identity
          env:
            - {name: LIBRARY_ROLE, value: identity}
            - name: TMDB_TOKEN
              valueFrom: {secretKeyRef: {name: tmdb-key, key: token}}
      containers:
        - name: enrich                # writes the runs row, waits for the echo
      volumes:
        - name: library               # the Library's claim, read-write
        - name: catalog               # <library>-enrich-catalog
```

The sequence is in the pod, where `kubectl get pod -o yaml` shows it.
The operator holds no order of concerns of its own.

## The worked example

A new folder lands on the NAS with one `.mkv` and nothing else.

1. The hourly scan, or the webhook, walks the folder. It finds no
   `.nfo`, reads the title and year off the name, and writes a movie
   row with no provider id. Its `runs` row echoes. The report now says
   `gaps: {probe: 1, identity: 1}`.
2. The operator sees the counts, no run in flight, and a scan since
   the last enrich, so it creates `movies-enrich`. The sidecar syncs
   the catalog onto the enrich claim.
3. `probe` finds the one file with no `<streamdetails>`, runs
   `ffprobe`, writes a minimal `.nfo` with a 99 minute runtime, and
   appends its attempt. It exits.
4. `identity` finds the one item with no id and no attempt inside the
   window. One TMDb result matches the title and 1986, so it inserts
   the `uniqueid` into the `.nfo` and writes the reason. It exits.
5. `enrich` writes the `runs` row and waits for the echo.
6. The next scan reads the `.nfo`. The movie row gains the id, the
   `aliases` table gains it, and the attempts become rows. The report
   says `gaps: {probe: 0, identity: 0}`.

In the bad case, step 4 finds two results for 1986 and neither
runtime is close. It writes no `uniqueid` and lists both in
`identity.yaml` with their receipts. After the next scan the `Library`
shows one title waiting on a person. The person reads the two
candidates and puts the right `uniqueid` into the `.nfo`, and the
scan after that identifies the title.

## Proof

On `liken-1`, against the lab's movies library on the NAS. The
`probe` concern fills `<streamdetails>` for every file that has none.
The `identity` concern runs over the sidecar-less fifth and the drill
records how many it wrote, how many it left as candidates, and how
many it wrote wrong, by hand check. That number decides whether the
ladder ships as the default. A confirmed candidate reaches the catalog
on the next scan. The write test fails on a planted `os.Remove`. A
`.nfo` edited by the probe keeps every element it held before, by a
diff. A webhook for one folder ends with that folder identified.

## What is not decided

How many minutes count as "close" on the runtime rung. Whether the
probe should read every file in a big series library on its first run
or take them in bounded batches per `Job`. How a person confirms from
the screen, which is a later browser plan that writes the same
`uniqueid`. Whether the `ffprobe` binary rides in the operator image
or in one of its own.
