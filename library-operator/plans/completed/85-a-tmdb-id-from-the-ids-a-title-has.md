# 85, A TMDb id from the ids a title has

This plan, written on 2026-11-01, has the identity fact find a title's
TMDb id from the IMDb or TheTVDB id its `.nfo` file already names, and
fill every other id the title lacks. Built on 2026-11-02. No drill has
run yet.

## The problem

Every TMDb fact keys on the title's TMDb id: the poster, the backdrop,
the logo, the season posters, the episode stills, the overview, the
certification, the TMDb rating, the full credits, and the trailers
TMDb holds. The identity fact is the one writer of that id, and it
worked only on a title whose canonical id was its folder key. The gap
query in `enrich.go` selected `id LIKE 'movie:path:%' OR id LIKE
'series:path:%'`.

A title whose `.nfo` file names an IMDb or TheTVDB id and no TMDb id
has a provider id, so it was never in the identity gap. Sonarr writes
such a file whenever its metadata service has no TMDb mapping for the
show: the series record holds `tmdbId: 0`, and the Kodi metadata
writes `tvdb`, `imdb`, and `tvmaze` uniqueids alone. A movie whose
`.nfo` file names only `imdb` is the same case.

Nothing reported the title as missing anything:

* The art gaps in `artfacts.go` join each title to its `tmdb` alias,
  so a title with no `tmdb` alias has no row in them. On a house
  library the poster gap read 0 while a series folder held no poster.
* The TMDb rating records `nothing`, the credits fall back to `imdb`
  and `tvmaze`, and the attempts look like a title TMDb does not hold.
* `status.unresolved` counted only path-keyed titles, so it did not
  count these.

TMDb holds every one of these titles. Its `find` call answers on an
IMDb or a TheTVDB id. For the 1990 miniseries `It` (TheTVDB `113981`,
IMDb `tt0099864`), both `find/113981?external_source=tvdb_id` and
`find/tt0099864?external_source=imdb_id` return TMDb TV `19614`. On
the house series library, 3 of 194 series had this shape. One of them
had no art at all, and the other two showed art that Jellyfin wrote
before the operator took over.

The same gap left two more cases behind:

* A movie a person named by its TMDb id, to settle the ladder's
  candidates, never got its IMDb id, so OMDb and the IMDb datasets
  passed it over.
* A failed external ids call recorded the attempt as `found`. The
  title then held its TMDb id, so it left the gap, and no run asked for
  its other ids again.

## The design

The identity gap holds every title that lacks one of the ids its
scope's canonical order in `providerOrder` names: TMDb and IMDb for a
movie, and TheTVDB, TMDb, and IMDb for a series. The query reads the
`aliases` table, so a scan that changes a title's aliases opens the
gap. The path-keyed titles are in it, as before.

A title that holds no TMDb, IMDb, or TheTVDB id climbs the ladder as
before. A title that holds a TMDb id goes straight to the external ids
call. For a title with an IMDb or a TheTVDB id and no TMDb id:

1. If the title's identity ledger holds a TMDb id, and every id the
   ledger shares with the `.nfo` file agrees, the fact writes the
   ledger's ids back without asking TMDb. See the Sonarr rewrite below.
2. Otherwise the fact asks `find` with each id the title holds, in the
   kind's order: `imdb_id` then `tvdb_id` for a movie, and `tvdb_id`
   then `imdb_id` for a series. It keeps the results of the library's
   kind, `movie_results` or `tv_results`.
3. If every answer names the same single TMDb id, the fact writes it
   into the `.nfo` as `<uniqueid type="tmdb">`, fills in the other ids
   through the external ids call, and records a `found` attempt whose
   reason names the calls, such as `find by tvdb 113981 and imdb
   tt0099864`.
4. If the answers name two different TMDb ids, the fact records both
   as candidates in `.liken/identity.yaml`, each with a `find` receipt
   that names the call that named it. The title waits for a person,
   as an ambiguous ladder answer does.
5. If no answer names a title, the fact records `nothing`. The fact
   does not go on to the ladder. The ids already say which work this
   is, and a title search could match a different show with the same
   name.

The external ids call writes only the ids the title lacks. An id the
`.nfo` file already holds stays, whatever TMDb answers for it.

The write keeps the `.nfo` file's own default. The fact marks the TMDb
id as `default="true"` only where no other uniqueid carries the mark,
so a file never holds two, and a Sonarr file keeps `tvdb` as its
default.

A failed `find` call, or a failed external ids call for a title that
held its TMDb id, records `error`. A failed external ids call after
this attempt wrote the TMDb id records `partial`. Both take the error
window of a day, so the next day's run asks for the ids this one did
not get. The operator rescans the title after a `found` or a `partial`
attempt, so the phases after identity read the new ids in the same
Job.

### The Sonarr rewrite

Sonarr rewrites `tvshow.nfo` when it refreshes a series, and the
rewrite drops the TMDb uniqueid again. The gap then reopens. The
identity ledger in `.liken/` is not Sonarr's to rewrite, and it holds
the ids the fact found, so the fact writes them back from there without
asking TMDb. The ledger stands only while every id it shares with the
`.nfo` file agrees. A changed id means a person or a tool named a
different title, and the fact asks `find` again.

The rewrite does not count as a fight. A fight is another writer that
holds an element group this operator writes, and the repair is to stop
that writer. Sonarr drops one uniqueid and keeps the rest, and the
write-back costs no provider call, so the operator repairs it and
leaves Sonarr running.

### The counts

`status.waiting` counts the identity attempts that ended in candidates,
and `status.unresolved` the attempts that ended in nothing, for every
title that still has no TMDb id. A path-keyed title has none, so both
counts read as before for those titles, and they now also count a
provider-keyed title whose ids named two titles or none.

### What does not change

The canonical id of a series does not change. `providerOrder` puts
`tvdb` first for a series, so `series:tvdb:113981` stays the id, and
the new `tmdb` id becomes an alias.

A movie is different. `providerOrder` puts `tmdb` first for a movie,
so a movie that was `movie:imdb:tt…` becomes `movie:tmdb:…` on the
rescan. The IMDb id stays an alias of the new id. The progress store
records each Play's work by provider and id in `play_aliases`, and the
media browser resolves those through the catalog's `aliases`, so a
played movie keeps its position.

## Considered and set aside

* **Falling back to the ladder when `find` answers nothing.** The ids
  already say which work this is. A name search that disagreed with
  them would write a second identity into the same `.nfo` file.
* **Limiting `find` to series** to keep a movie's canonical id. The
  aliases carry the progress across the change, so movies get the same
  fix.
* **A TheTVDB id for every movie.** TMDb holds few of them, and no fact
  needs one for a movie, so a movie without one stays out of the gap.
  The fact still writes one when TMDb answers it.
* **Counting the Sonarr rewrite as a fight.** See the Sonarr rewrite.

## Tests

Each test runs the identity fact against a fixture library folder and
a fake TMDb server:

* A series `.nfo` with `tvdb` and `imdb` ids and no TMDb id gets the
  TMDb id both `find` calls return. The file has exactly one
  `default="true"`, still on `tvdb`. The ledger records `found` with
  the reason `find by tvdb 389492 and imdb tt7000007`.
* A movie `.nfo` with only an `imdb` id gets its TMDb id the same way,
  moves to `movie:tmdb:1101`, and its IMDb alias still names it. Where
  the IMDb id carries no default mark, the TMDb id takes it.
* A movie a person named by its TMDb id gains its IMDb id, with no
  `find` and no search.
* A `find` that returns nothing records `nothing`, the fake sees no
  search call, and the title counts in `status.unresolved`.
* Two `find` calls that name different TMDb ids record both as
  candidates with their receipts, and the title counts in
  `status.waiting`.
* A Sonarr rewrite that dropped the TMDb id gets it back from the
  ledger with no `find` call, and a ledger whose TheTVDB id differs
  from the `.nfo` file's asks `find` again.
* A failed `find`, a failed external ids call, and a failed external
  ids call after the ladder wrote the TMDb id each reopen after the
  error window.
* The gap query selects a title missing any of its scope's ids, and
  skips a title that holds them all.

## Docs

The Identification section of the enrichment guide describes the
`find` step, the default rule, the Sonarr write-back, and the counts.
The `waiting` and `unresolved` descriptions in the Library CRD name the
`find` cases.

## Proof

A drill on a home cluster has not run. It would take the three house
series that had this shape and confirm that one Job writes each one's
TMDb id, its poster, and its overview, and that a Sonarr refresh of one
of them is repaired on the next run with no `find` call in the log.
