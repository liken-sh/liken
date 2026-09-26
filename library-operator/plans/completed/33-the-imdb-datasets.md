# 33, The IMDb datasets

Plan 33. A new `imdb` provider block serves `rating.imdb` for movies,
series, and episodes, and `credits` for movies and series, from IMDb's
published datasets. Each cluster downloads the files from IMDb
directly, and a claim in the cluster caches them. An enricher `Job`
reads each file once per run and keeps only the rows of the titles in
its gap list. The provider's status reports when IMDb last published
each file.

Built on 2026-09-25 in e86afd9 (the rating) and ae23b02 (the
credits), and drilled on `liken-1` on 2026-09-26 with library-operator
2026.09.19-002-dev-020-ae23b024 and later. The drill found that a
rating another tool wrote was never read from the datasets, and
e0cccae fixed it. "What the build changed", "The drill on `liken-1`",
and "What is still open" at the end record the results. The
attribution the media browser shows is still open.

The stub of this plan came from the 2026-09-03 design discussion of
[plan 30](30-facts-art-and-contributors.md). This version
replaces that stub's design, and the reasons are under "What was set
aside".

## The problem

IMDb has no free API. It publishes a set of dataset files at
[datasets.imdbws.com](https://datasets.imdbws.com/) and replaces each
file every day. The files are for personal and non-commercial use, so
no `liken` repository or image may carry them, or carry an index built
from them. Each cluster must download the files itself.

Every other `MetadataProvider` answers one call per title. A dataset
is one bulk file for all titles. The sizes on 2026-09-25, gzipped:

| File | Size | Rows | What it holds |
| --- | --- | --- | --- |
| `title.ratings` | 8.7 MB | 1.7 million | average rating and vote count per title |
| `title.episode` | 55 MB | 9.9 million | series id, season, and episode per episode id |
| `title.crew` | 83 MB | | directors and writers per title |
| `title.basics` | 228 MB | | type, titles, year, runtime, genres |
| `name.basics` | 310 MB | 15.7 million | name, dates, and professions per person |
| `title.akas` | 515 MB | | alternate titles |
| `title.principals` | 784 MB | 102 million | principal credits with person ids |

Today OMDb serves `rating.imdb` at a thousand calls a day for each
key, and it spends one call on each title. A movie library of two
thousand titles needs two days. Episodes have no rating fact at all,
and a series library holds tens of thousands of them.

TMDb serves `credits` with person ids today, and its person record
carries the IMDb id. A library with no TMDb key, or a title that TMDb
does not hold, has no credits. IMDb's `title.principals` and
`name.basics` hold the principal credits of every title IMDb knows.
`title.principals` averages 8.8 rows per title and holds at most 69
for one title, so it is the billed cast and the key crew, not the full
cast list that TMDb gives.

This plan reads four of the files: `title.ratings`, `title.episode`,
`title.principals`, and `name.basics`.

## The design

**A dataset fact reads a file once per run, not once per title.** An
enricher container has its full gap list before it asks a provider
for anything. `rating.imdb` and `credits` are facts of the nfo group,
so the nfo container runs them. It reads the gap list of each dataset
fact, then reads each file it needs from start to end through a gzip
reader. It
keeps a row only when the row's id is in its list, and it discards
every other row as it reads. Memory holds the list and the matching
rows, not the file. One read of a file serves every title in the run,
whether the run has one gap or ten thousand. A fact with no gaps in the
run sends no request for its files.

The nfo container starts the dataset reads when it starts, and it runs
its other facts while the reads work. So the 32 seconds of a credits
read do not delay the plot, the certification, or the other ratings.
The container keeps its memory limit of 64Mi. Memory holds the gap
list, the matching rows, and one gzip buffer.

These times were measured from a cloud host on 2026-09-25 with
`curl` and `gunzip`. A home link downloads more slowly. The time to
decompress and read every row is the same on a machine of similar
speed:

| File | Download | Decompress and read every row |
| --- | --- | --- |
| `title.ratings` | 0.4 s | 0.3 s, 30 MB |
| `title.episode` | 2.2 s | 1.7 s, 261 MB |
| `name.basics` | 3.5 s | 7.2 s, 969 MB |
| `title.principals` | 7.2 s | 24.3 s, 4.6 GB |

So a run that fills ratings takes about one second of reading, and a
run that fills credits takes about 32 seconds of reading, however
many titles it fills.

**The cluster caches each file.** Every run needs the whole file. A
standing enricher run, a webhook's folder enrich, and a `spec.refresh`
each start a `Job`, and several libraries can each start one on the
same day. Without a cache, each of those `Job`s downloads the files
again, and a webhook for one new movie downloads 1.1 GB for its
credits. With a cache, a node downloads each file once for each
version IMDb publishes, and every other run on that node reads the
file from disk.

**The provider's status reports what IMDb publishes.** The check that
tells the operator whether the provider is `Ready` also reads the
headers of each file the block's facts need. The status reports when
IMDb last replaced each file, so a person can see when IMDb stops
publishing. The gap query also reads these times when it decides
whether a rating is old enough to read again.

## The contract

### The provider block

- **`spec.imdb` is an empty block,** like `tvmaze`. IMDb requires no
  account for the datasets. The block alone says that the operator may
  download them.

  ```yaml
  apiVersion: library.liken.sh/v1alpha1
  kind: MetadataProvider
  metadata:
    name: imdb
    namespace: media
  spec:
    imdb: {}
  ```

- **The block serves `rating.imdb` and `credits`.** The `Library`'s
  source order decides which provider answers, as it does for every
  fact. A `Library` that puts `imdb` before `omdb` takes the rating
  from the datasets. OMDb continues to serve the Rotten Tomatoes and
  Metacritic ratings, the certification, and the plot, and its
  thousand calls now go to those facts.
- **The datasets are a fallback for credits.** `title.principals` has
  about nine people for each title, and TMDb gives the full cast. So a
  `Library` puts `tmdb` before `imdb`, and the datasets answer
  `credits` only for a title that TMDb does not have. The manual's
  examples use that order.
- **The block does not serve the contributor facts.**
  `contributor.ids` keys on the TMDb id, and `name.basics` has no TMDb
  id, no biography, and no image. TMDb continues to serve all three.
- **The pace is one request per file per run.** The table row's pace
  is the interval between two file requests in one container. The
  fact makes one request for each file it reads, not one request for
  each title.

### The check and the status

- **The check sends one `HEAD` request for each file the served facts
  read.** `rating.imdb` reads `title.ratings` and `title.episode`.
  `credits` reads `title.principals` and `name.basics`. A provider
  that `spec.facts` narrows to one fact checks only that fact's files.
  A `200` for every file is `Reachable`. Any other status is
  `Unavailable`, and the message names the file and the status code.
  No answer is `Unreachable`, and the message is the error text. IMDb
  has no key, so the check never writes `Refused` or `NoSecret`.
- **The check keeps the cadence of every other provider.** It calls
  when the operator starts and when the provider's generation changes.
  After that it calls once an hour, or every five minutes after an
  `Unreachable` or `Unavailable` answer. That is at most four `HEAD`
  requests an hour. A `HEAD` request transfers no file.
- **`status.imdb.datasets` lists one entry for each file the check
  read,** with the headers IMDb returned. The block's status is under
  `status.imdb`, as its settings are under `spec.imdb`:

  ```yaml
  status:
    provider: imdb
    facts: [rating.imdb, credits]
    imdb:
      datasets:
      - name: title.ratings
        lastModified: "2026-09-25T00:40:02Z"
        etag: '"e03c00cbebf3c4fda2d847d0f6d16b7e-2"'
        size: 8657835
      - name: title.episode
        lastModified: "2026-09-25T00:39:26Z"
        etag: '"03737bcd3a8c870a1243090aeb70a9d5-7"'
        size: 54779154
      - name: title.principals
        lastModified: "2026-09-25T00:40:24Z"
        etag: '"c7e7651414623759eea719c945546b9a-94"'
        size: 783966700
      - name: name.basics
        lastModified: "2026-09-24T12:43:08Z"
        etag: '"83cec581eaa50d30776a32f09d435bc6-37"'
        size: 310309697
    conditions:
    - type: Ready
      status: "True"
      reason: Reachable
  ```

  A failed check leaves the previous entries in place, because the
  last version IMDb published is still a fact. An `UPDATED` printer
  column shows the oldest `lastModified` in the list.
- **An old file does not make the provider unready.** IMDb replaces
  each file daily. When a file's `lastModified` is more than three days
  old, the check writes a `Stale` condition with status `True`, and its
  message names the file and its date. `Ready` does not change, because
  a file from last week still gives correct ratings and credits. A
  person reads `Stale` as a sign that IMDb stopped publishing, not that
  the cluster failed.

### The cache

- **One claim per `imdb` provider, `<provider>-datasets`.** The claim
  is in the provider's namespace, and the `MetadataProvider` owns it,
  so a deleted provider takes its cache with it. Its size is a fixed
  3Gi. The four
  files take 1.2 GB, and the replacement of one file needs room for a
  second copy of it, at most 784 MB for `title.principals`. The rest
  leaves room for the files to grow. The claim follows the create-once
  rule of plan 43.
- **The claim is on a per-node class, whatever class the libraries
  use.** The claim is `ReadWriteMany` on a volume from plan 44. Any
  number of `Job`s on any node mount it, and each node's directory
  fills the first time a run on that node needs a file. The operator
  uses the class whose provisioner is `per-node.liken.sh`, the test
  that `classIsPerNode` makes today. It does not match a class by name.
- **With no per-node class, there is no cache.** A claim on any other
  class is `ReadWriteOnce`. A `Job` that mounts it must run on the node
  that has the volume, and each `Library` also mounts its own catalog
  claim. When those two claims are on different nodes, the `Job` never
  starts. So on a cluster with no per-node class, the operator makes no
  claim. The nfo container reads each file from IMDb directly into the
  gzip reader, and the check writes a `Cached` condition with status
  `False` and the reason `NoPerNodeClass`. `Ready` does not change.
- **The enricher `Job` mounts the claim** read-write in the nfo
  container alone, and only when the `Library`'s sources name a `Ready`
  `imdb` provider as the answerer of a fact that has gaps.
- **The layout is one directory per file.** `title.ratings/` holds the
  gzipped file exactly as IMDb served it, named by a hash of its ETag,
  and a record `current.json` that gives the ETag, the `Last-Modified`
  time, the size, and the time the enricher downloaded it. The cache
  holds the gzipped form and not a decompressed or indexed form, for
  the reasons under "What was set aside".
- **A run reads only the files its gaps need.** A run with rating gaps
  and no credit gaps does not download or read `title.principals` or
  `name.basics`. A run reads `title.episode` only when an episode gap
  has no IMDb id.
- **Each run asks IMDb whether its copy is current.** The container
  sends a `GET` with `If-None-Match` set to the ETag in `current.json`.
  IMDb returns `304 Not Modified` with no body when the file has not
  changed, and the container reads its cached copy. IMDb returns `200`
  and the new file when the file has changed. The status's ETag is not
  used for this decision, because it can be an hour old.
- **A download replaces the cached copy only when it is complete.** The
  container writes the new file to a temporary name in the same
  directory. It checks that the byte count equals `Content-Length` and
  that the gzip stream ends with a valid checksum. Then it renames the
  file into place, writes `current.json` the same way, and deletes the
  previous copy. A container that already has the previous copy open
  can finish reading it, because a deleted file stays readable until
  every open handle closes.
- **One download at a time for each file on each node.** The container
  takes an exclusive `flock` on the file's directory before it
  downloads. A second container on the same node waits for the lock,
  then reads `current.json` again and uses the new copy. It does not
  download the file a second time.
- **The cache can fail, and the fact still runs.** When the claim is
  full, read-only, or not mounted, the container reads the file from
  IMDb directly into the gzip reader and writes nothing to disk. The
  container logs the cache error with the error text of the
  filesystem. A cache error is not an attempt result, because the fact
  still got its answer.

### The rating

- **A movie or a series** is looked up by the IMDb id that identity
  wrote in its `.liken/` ids. A title with no IMDb id is not in the
  gap list of the `imdb` block. A title that IMDb has no rating for
  records a miss, as any other provider's miss does.
- **An episode gets `rating.imdb` too.** The `episodes` table gains
  the `nfo_facts` column that `movies` and `series` have, and the
  scanner fills it from the episode `.nfo`. The `rating.imdb` gap
  query includes episodes, and only the `imdb` block serves the
  episode gap. OMDb would spend one call on each episode.
- **An episode's IMDb id comes from `title.episode`** when its `.nfo`
  does not already give one. The container reads `title.episode` once
  and keeps each row whose `parentTconst` is the IMDb id of a series in
  the gap list, and matches season and episode numbers to the
  episode's own. The container writes the id it found to the episode's
  `.liken/` ids, so a later run does not read `title.episode` again for
  that episode.
- **The rating is written as OMDb writes it today,** as the `imdb`
  rating in the `.nfo`, with the vote count and a maximum of ten.
- **The container writes a `.nfo` only when the rating changed.**
  IMDb gives `averageRating` with one decimal, which is the value the
  media browser shows. The container compares that value with the
  rating the `.nfo` already has. When the two are the same, the
  container records the attempt and does not write the `.nfo`. When
  they differ, it writes the new rating with the current vote count.
  A change in the vote count alone does not write the `.nfo`, because
  the vote count of almost every title changes each month. This keeps
  a refresh from rewriting every `.nfo` in a library and starting a
  rescan of every folder.
- **The attempt records the version of the file it read.** The
  attempt of `rating.imdb` gains the `Last-Modified` time of the
  `title.ratings` copy that answered it. An attempt that another
  block answered has no such time.

### The credits

- **The container reads the two files in sequence.** It reads
  `title.principals` once and keeps the rows whose `tconst` is in the
  gap list, and it collects the `nconst` of each row it keeps. Then it
  reads `name.basics` once and keeps the rows whose `nconst` it
  collected. It skips `name.basics` when every person it collected
  already has a `.contributors/` entry.
- **A row becomes a credit as TMDb's credits do.** The categories
  `actor`, `actress`, and `self` are cast, in IMDb's `ordering`, with
  the `characters` list as the role. `director`, `writer`, `producer`,
  `composer`, `cinematographer`, `editor`, `production_designer`, and
  `casting_director` are crew, with the category as the job and the
  `job` column where IMDb gives one. `archive_footage` and
  `archive_sound` are not credits.
- **A person is one `.contributors/` entry, whichever provider named
  them.** A credit from the datasets names the person by `nconst`
  alone. [Plan 66](66-one-entry-for-each-person.md) finds the entry by
  that IMDb id first, and it asks TMDb's find endpoint for the TMDb id
  of a person that no entry holds. `contributor.ids` fills an entry that
  has only an IMDb id the same way, so a person that IMDb named gets a
  biography and a headshot. When two entries turn out to be one person,
  plan 66 merges them. This plan's credits depend on plan 66, and the
  rating does not.
- **Credits are not refreshed on a timer.** A title's principal
  credits change rarely after its release, and a credits run costs 32
  seconds of reading. `spec.refresh` with the key `credits` opens them
  again, as it does for every fact.

### Refresh of the rating

- **A rating from the datasets becomes a gap again after 30 days.** The
  `rating.imdb` gap includes a title when the `imdb` block is the
  answerer for the fact, the title's attempt is more than 30 days old,
  and the attempt's file time is older than the `lastModified` of
  `title.ratings` in `status.imdb.datasets`. IMDb publishes the file
  daily, so the second test fails only when IMDb has stopped
  publishing. Then no run reads the same file again for nothing. An
  attempt with no file time, such as one that OMDb answered, counts as
  older. So a library that moves from OMDb to the datasets moves each
  title's rating over within 30 days, and no title is read more often
  than once in 30 days.
- **The operator passes the file time to the enricher's gap query.**
  The gap query runs as SQL in two places: in the enricher `Job`,
  against its local copy of the catalog (`enricher.gaps`,
  `enrichworker.go:130`), and in the reporter in the catalog pod, which
  counts the gaps (`gapCounts`, `attempts.go:279`). Neither of them
  reads the `MetadataProvider`. The operator writes the `lastModified`
  of `title.ratings` into the `Job`'s environment, next to the provider
  settings that `providerEnv` writes today. With no time, as before the
  first check, the query uses the 30 days alone.
- **The reporter counts with the 30 days alone.** The reporter reads no
  `Library` and no `MetadataProvider`, and it counts every gap with no
  refresh time today. So when IMDb stops publishing, the report counts
  a gap that the `Job` does not find, and every report would start a
  `Job` that ends with nothing to do. To prevent that, the operator does
  not start a gap-mode `Job` for `rating.imdb` alone while the
  provider's `Stale` condition is `True`. A walk still starts its `Job`,
  so a new title still gets its rating from the newest file.
- **The period is a constant,** not a field. A field can be added when
  a person needs a different period. `spec.refresh` with the key
  `rating.imdb` still opens every rating at once, as it does for every
  fact.

### The order of the build

The builder may land the rating first and the credits after it, in
separate commits. The plan moves to `plans/completed/` when both are
built and drilled.

## What was set aside

- **A full index of every dataset, which the stub proposed.** The
  stub had a `CronJob` that downloads every file on a schedule and
  builds an SQLite index per file, on a claim that each enricher
  mounts read-only. That download is about 2 GB a day, and the index
  is more than 10 GB. A library of two thousand titles reads about 0.1
  percent of `title.ratings` and about 0.02 percent of
  `title.principals`. The stub also needed a `schedule` field, a
  `storage` field, and a `ReadOnlyMany` volume that a per-node class
  cannot share between nodes. The design above downloads only when a
  run has gaps, and it stores 1.2 GB.
- **A cache claim on the libraries' class.** On a class that is not
  per-node, the claim is `ReadWriteOnce`. A `Job` that mounts it and
  its own catalog claim on a different node never starts.
- **A cache on each `Library`'s own enricher claim.** That claim is
  sized for the catalog, and every library in the namespace would
  download its own copy of each file. One claim per provider downloads
  once per node.
- **A decompressed or indexed copy in the cache.** All four files are
  sorted by their id, so a decompressed copy would allow a binary
  search with no index. For `title.ratings` the saving is less than a
  second. For the credits it is about 32 seconds a run, and the cost
  is 5.8 GB of decompressed files, about five times the gzipped
  cache, and the code that keeps a second format current. A credits
  run happens when new titles arrive, not on every enricher pass, so
  the plan keeps the gzipped form. The drill records the real read time
  on `liken-1`, and a later plan can add the copy if that time is a
  problem.
- **A `Job` that fills the cache as soon as the check reads a new
  ETag.** It would download 1.2 GB every day, also in a namespace that
  has no gaps. The cache fills only when a run needs a file.
- **`title.crew`.** It holds directors and writers only, and
  `title.principals` already holds both with the rest of the key crew.
- **Offline identification from `title.basics`.** It would identify a
  title with no TMDb key. The design gives the TMDb id priority over
  other ids, and TMDb identity is free, so this stays out of scope.

## Attribution

IMDb's terms (IMDb Help, "Can I use IMDb data in my software?", read
on 2026-09-25) allow limited non-commercial use of the datasets. The
data "must not be altered/republished/resold/repurposed to create any
kind of online/offline database of movie information (except for
individual personal use)". Each cluster downloads the files for the
people who own it, and no `liken` repository or image carries them.
Every use must include the statement "Information courtesy of IMDb
(https://www.imdb.com). Used with permission." The terms do not say
where.

The manual's enrichment guide shows the statement. The media browser
does not show it yet. Where it shows it is open: on an about screen,
or beside each IMDb rating and IMDb credit.

## What the build changed

- **The reporter counts only the ratings that are missing.** It reads
  no `MetadataProvider`, so it binds no dataset time. The operator
  finds the ratings to read again from the oldest `rating.imdb`
  attempt, 30 days after it, and not while `Stale` is `True`. A
  missing rating still starts a `Job` while `Stale` is `True`.
- **A rating that another tool wrote is read once.** Such a title has
  the rating in its `.nfo` file and no `rating.imdb` attempt, so
  neither the 30 days nor `spec.refresh` opened it. The drill found
  this. The `Job`'s gap now includes it when an `imdb` provider
  answers the rating, and the reporter does not count it.
- **Episodes are in the gap only where `imdb` answers the rating.**
  The report carries the episode count apart, and the operator takes
  it out of the gap of a Library whose rating another block answers.
  A file that holds two episodes is not in the gap, because the season
  ledger keys an attempt on the file.
- **The nfo container mounts the cache whenever a `Ready`, cached
  `imdb` provider is in the sources.** The builder of the `Job` reads
  no report, so it cannot test for gaps. A run with no gaps still
  sends no request.
- **A title with no IMDb id records a miss,** as OMDb does, and stays
  out of the next runs for the attempt's window.
- **`UPDATED` reads `status.imdb.updated`,** a field with the oldest
  `lastModified`, because a printer column cannot compute a minimum.
- **Credits are a fallback fact of the block.** The provider table
  marks `credits` as `fallback` for `imdb`, so the datasets add nothing
  to a title that an earlier source in the Library's order credited.
- **The credits hold the cast, the directors, and the writers.** The
  credit model has only those parts, which is all that TMDb's credits
  carry, so the other crew categories are dropped.
- **A pass that finds new titles reads the two credits files again.**
  Identity can name titles over several passes of one `Job`, and each
  of those passes costs about 32 seconds of reading.

## The drill on `liken-1`

- **The provider.** A new `imdb` provider was `Ready` in under a
  minute, with four entries in `status.imdb.datasets`, `Stale` of
  `False`, `Cached` of `True`, and the claim `imdb-datasets` bound
  `ReadWriteMany` on the per-node class.
- **Episodes.** With `imdb` after `tmdb` and before `omdb` in the
  sources of both Libraries, one gap `Job` of the series wrote
  `rating.imdb` for 6,277 of the 6,343 episodes that had none, in
  2 min 45 s. It read `title.episode` in 5.3 s, which gave the IMDb id
  of 6,288 episodes, and `title.ratings` in 0.8 s. The writes of the
  `.nfo` files took most of the time.
- **The cache.** A later run of the movies got `304` for
  `title.ratings` and read the cached copy in 0.58 s.
- **The movies.** After the fix, a `spec.refresh` of `rating.imdb`
  answered 1,436 movies from the cached file. Only 29 `movie.nfo`
  files changed, because the rest already had the same rating at one
  decimal.

## What is still open

- **The attribution in the media browser.**
- **Credits from the datasets are not drilled** on a title that TMDb
  does not have.
- **The credits files can be read more than once in one `Job`.**
- **`status.gaps` of a Library whose rating OMDb answers still counts
  episodes.** Only the operator's scheduling takes them out.
