# Identification

Plan 29. The first enrichment build from [plan 27](27-enrichment.md).
It gives every title its provider ids, from the sidecar where one
exists and from a provider search where none does, and it brings the
machinery every later concern uses.

## The problem

About a fifth of the lab's movies have no sidecar. Their id in the
catalog rests on the folder's path, so a move of the folder loses the
item, and no enricher can ask a provider about a title it cannot name.
Every other concern in plan 27 waits on an id. Identification is also
the one step with a person in the loop, because a search can be wrong,
and a wrong id sends the wrong film's plot and art to every concern
downstream.

## The contract

- **`MetadataProvider`.** The resource from plan 27, with a `tmdb`
  block first, because TMDb serves movies, series, and people. Its
  `Secret`, its concerns, and its status: reachable, and the last
  time a call was refused for rate.
- **The `probe` concern.** The first file concern. It opens a video
  file once, reads the container's duration, codecs, resolution, and
  audio layout, and writes them as `<streamdetails>` into the `.nfo`,
  creating a minimal `.nfo` where none exists. It needs no id, so it
  runs before identity, and identity uses its runtime.
- **The `identity` concern.** For a title with no provider id, it
  searches by name, year, and runtime. One exact match on title and
  year with nothing close is written. Anything else is a candidate
  list with scores in `identity.yaml`, and the concern stops there.
  A person confirms by moving the chosen candidate to `id:`. The next
  scan reads it, and the `aliases` table gains the id.
- **The gap loop.** The operator's subscription on the catalog pod,
  the batch by folder, one `Job` per concern per library at a time,
  and the attempts file per concern. The rate limiter per provider
  key is in the operator, and a `Job` takes a slot before each call.
- **The write package.** Create, temporary-and-rename, and the one
  remove for a `.liken-tmp-` name, with the test that fails the build
  on any other remove or rename in the enricher code. Enricher `Job`s
  mount the volume read-write, and scan `Job`s stay read-only.
- **`Library` status.** The count of titles that wait for a person,
  the count with no id after every provider was asked, and a printer
  column for the first.

## The file

```yaml
# .liken/identity.yaml, in a title's folder
items:
  - path: .
    id: {tmdb: 603, imdb: tt0133093}
    source: sidecar             # or: search, or: person
  - path: .
    candidates:
      - {tmdb: 11, title: Star Wars, year: 1977, score: 0.91}
      - {tmdb: 1893, title: Star Wars, year: 1999, score: 0.42}
    attempted: 2026-09-02T14:00:00Z
```

In a season or album folder the file holds one entry per item under
it, keyed by the item's relative path.

## Proof

On `liken-1`, against the lab's movies library on the NAS. The `probe`
concern fills `<streamdetails>` for the sidecar-less fifth. The
`identity` concern runs over them and the drill records how many it
wrote, how many it left as candidates, and how many it wrote wrong,
by hand check. That number decides whether the guess rule ships as the
default. A confirmed candidate reaches the catalog on the next scan.
The write test fails on a planted `os.Remove`.

## What is not decided

The score and the margin that count as "sure". Whether the probe
should also run on titles that have a sidecar with no
`<streamdetails>`, which is most of the lab today. How a person
confirms from the screen, which is a later browser plan that writes
the same file.
