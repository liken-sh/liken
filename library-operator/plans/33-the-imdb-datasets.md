# The IMDb datasets

Plan 33. A stub from the 2026-09-03 shaping of [plan 30](completed/30-facts-art-and-contributors.md).
IMDb has no free API, but it publishes daily datasets: ratings,
basics, principals, and names, from a few megabytes to several hundred
gzipped, at [datasets.imdbws.com](https://datasets.imdbws.com/). They
serve `rating.imdb` with no daily limit, and `credits` and the
contributor ids for everyone IMDb knows.

## The problem

Every other `MetadataProvider` is a call per title. The datasets are
bulk files. A fact cannot fetch a hundred megabytes per title, and
Corrosion should not hold ninety million principal rows. So this
provider needs a store: something downloads the files on a schedule
onto a claim, and the facts read from that claim instead of the
network. That is a different kind of provider, with a schedule and a
size where the others have a key.

## The shape, not yet decided

A `spec.imdb` block with a `schedule` and a `storage` size. A
`CronJob` per provider that downloads the files onto a claim and
builds an index the facts can read by IMDb id without a full scan,
such as one SQLite file per dataset. The enricher `Job` mounts the
claim read-only where the `Library`'s `sources` name the provider.
`status.facts` is empty until the first download lands.

Until this plan is built, OMDb serves `rating.imdb` at a thousand
calls a day, and plan 30's rate rule leaves the rest as gaps.
