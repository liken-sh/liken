# 89, The status of a series

This plan, written on 2026-11-08, records whether a series still runs
or has ended, so a person's page can draw a series' run to its true
end. Plan 88 built the career axis that draws the run.

Status on 2026-11-08: a future plan. Nothing is built.

## The problem

The catalog holds no status for a series. TVmaze's show answers
`status`, as `Running` or `Ended`, and TMDb's TV details answer
`status` and `in_production`, but the facts of `tvmazefacts.go` and
`tmdbfacts.go` decode neither field. So the career axis of plan 88
guesses: a series runs when its newest episode in the library aired in
the last 2 years, and only a running series draws its run, as a bar
from its first year to now. An ended series draws no run, and a series
whose library is missing its newest seasons reads as ended.

## The proposal

- The TVmaze and TMDb facts decode the status, and the scanner writes
  it to a column of `series`, with an empty value where no provider
  answered one.
- The browser's works read of a person carries the column on each
  series slot.
- On the career axis, a running series draws its run from its first
  year to now, as it does today, and an ended series draws its run from
  its first year to the year of its last episode.
- The rule of plan 88, an episode in the last 2 years, stays as the
  fallback for a series whose status is empty.

## Open questions

- Which provider wins when TVmaze and TMDb disagree, and whether a
  `<status>` element of `tvshow.nfo` counts as a third source.
- Whether the last episode's year comes from the provider or from the
  episodes the library holds.
