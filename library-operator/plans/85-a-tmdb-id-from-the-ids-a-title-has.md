# 85, A TMDb id from the ids a title has

This plan, written on 2026-11-01, proposes that the identity fact find
a title's TMDb id from the IMDb or TheTVDB id its `.nfo` file already
names. It is not built.

## The problem

Every TMDb fact keys on the title's TMDb id: the poster, the backdrop,
the logo, the season posters, the episode stills, the overview, the
certification, the TMDb rating, the full credits, and the trailers
TMDb holds. The identity fact is the one writer of that id, and it
works only on a title whose canonical id is its folder key. The gap
query in `enrich.go` selects `id LIKE 'movie:path:%' OR id LIKE
'series:path:%'`.

A title whose `.nfo` file names an IMDb or TheTVDB id and no TMDb id
has a provider id, so it is never in the identity gap. Sonarr writes
such a file whenever its metadata service has no TMDb mapping for the
show: the series record holds `tmdbId: 0`, and the Kodi metadata
writes `tvdb`, `imdb`, and `tvmaze` uniqueids alone. A movie whose
`.nfo` file names only `imdb` is the same case.

Nothing reports the title as missing anything:

* The art gaps in `artfacts.go` join each title to its `tmdb` alias,
  so a title with no `tmdb` alias has no row in them. On a house
  library the poster gap read 0 while a series folder held no poster.
* The TMDb rating records `nothing`, the credits fall back to `imdb`
  and `tvmaze`, and the attempts look like a title TMDb does not hold.
* `status.unresolved` counts only path-keyed titles, so it does not
  count these.

TMDb holds every one of these titles. Its `find` call answers on an
IMDb or a TheTVDB id. For the 1990 miniseries `It` (TheTVDB `113981`,
IMDb `tt0099864`), both `find/113981?external_source=tvdb_id` and
`find/tt0099864?external_source=imdb_id` return TMDb TV `19614`. On
the house series library, 3 of 194 series had this shape. One of them
had no art at all, and the other two showed art that Jellyfin wrote
before the operator took over.

## The design

The identity gap holds every movie and series that has no `tmdb`
alias. The path-keyed titles are in it, as they are today, and so is
every title whose `.nfo` names other ids and no TMDb id.

For a title with an IMDb or a TheTVDB id, the fact asks TMDb's `find`
first, before the ladder:

1. Ask `find` with each id the title holds: `imdb_id`, then
   `tvdb_id`. Keep the results of the library's kind, `tv_results`
   for a series and `movie_results` for a movie.
2. If every answer names the same single TMDb id, write it into the
   `.nfo` as `<uniqueid type="tmdb">`, fill in the other ids through
   `externalIDs` as today, and record a `found` attempt whose reason
   names the call, such as `find by tvdb 113981`.
3. If the answers name two different TMDb ids, record the ids as
   candidates in `.liken/identity.yaml`. The title then waits for a
   person, as an ambiguous ladder answer does today.
4. If no answer names a title, record `nothing`. The fact does not go
   on to the ladder. The ids already say which work this is, and a
   title search could match a different show with the same name.

A path-keyed title climbs the ladder as before.

The write keeps the `.nfo` file's own default. `writeUniqueID` marks
`tmdb` as `default="true"` today, and a Sonarr file already marks
`tvdb` as the default. The fact marks the TMDb id as the default only
where no other uniqueid carries the mark, so a file never holds two.

### What does not change

The canonical id of a series does not change. `providerOrder` puts
`tvdb` first for a series, so `series:tvdb:113981` stays the id, the
new `tmdb` id becomes an alias, and the progress and the marks keyed
on the title stay where they are.

A movie is different. `providerOrder` puts `tmdb` first for a movie,
so a movie that was `movie:imdb:tt…` becomes `movie:tmdb:…` on the
next walk. See the open questions.

## Tests

Each test runs the identity fact against a fixture library folder and
a fake TMDb server:

* A series `.nfo` with `tvdb` and `imdb` ids and no TMDb id gets the
  TMDb id the fake's `find` returns. The file has exactly one
  `default="true"`, still on `tvdb`. The ledger records `found` with
  the find reason.
* A movie `.nfo` with only an `imdb` id gets its TMDb id the same way.
* A `find` that returns nothing records `nothing`, and the fake sees
  no search call.
* Two `find` calls that name different TMDb ids record both as
  candidates.
* The gap query selects a title with a `tvdb` alias and no `tmdb`
  alias, and it skips a title that has a `tmdb` alias.

## Docs

The Identification section of the enrichment guide gets the `find`
step, and it states that a title whose ids TMDb does not hold counts
in `status.unresolved`.

## Open questions

* A movie's canonical id changes when it gains a TMDb id. Does the
  progress store follow the aliases, or does a played movie lose its
  position? If it loses it, the walk could keep the earlier canonical
  id while an alias of the new one exists, or the fact could limit
  `find` to series.
* Sonarr rewrites `tvshow.nfo` when it refreshes a series, and that
  rewrite drops the TMDb uniqueid again. The gap then reopens. Should
  the fact read the id it found from its own ledger and write it back
  without asking TMDb, and should a rewrite like that count as a
  fight?
* Should `status.unresolved` count a provider-keyed title whose
  `find` answered nothing, or does that need a count of its own?
