# 93, Studios as first-class records

This plan, written on 2026-11-09, records an idea for later: make a
studio or a network a record of the catalog with its own identity,
page, logo, and history, in place of a name that each title's body
spells on its own.

Status on 2026-11-09: a stub, not designed. Nothing is built. The
questions below need a design session before anyone builds it.

## What a person gains

A studio becomes a thing a person can browse, the way a person or a
franchise is. Its page shows its logo, its name, and its titles in the
catalog, and it can state when the company was founded, renamed, or
bought. The browser joins the names that one company carried, so a
person who opens Searchlight Pictures sees the films that the catalog
spells `Fox Searchlight Pictures` too. The names that are the same
company under another spelling join as well:

- `Fox Searchlight Pictures` and `Searchlight Pictures`, one company
  before and after the rename of 2020.
- `20th Century Fox` and `20th Century Studios`, the same.
- `SYFY` and `Syfy`, names that differ only in case.

A studio's logo also gives the foot of a title's page and the More
from strip something to draw in place of a line of text.

## What `liken` has today

- A studio is a name in a title's body and nothing more. `tmdbfacts.go`
  writes the production companies of a TMDb title into the `studio`
  elements of the `.nfo` file, or its networks where TMDb names no
  company, and `tvmazefacts.go` writes the network or the web channel
  of a TVmaze show. The scanner copies the names into the body's
  `studios` array. No table and no index holds them, and no provider's
  company id is kept.
- The browser's studio wall (`Query::Studio`) reads every title whose
  body names one exact name, across the libraries, by walking both item
  tables with `json_each`. Two spellings of one company are two walls.
- The foot of a title's page draws the More from strip for the first
  studio that has at least 4 other titles, and names the rest after
  `Produced by`. The strip reads the titles of the page's studios in one
  statement that walks both item tables.
- The counts on a home catalog on 2026-11-09: 712 distinct studio and
  network names across 1,513 films and 206 series, and 30 of those
  names with 8 or more titles. 1,499 of the 1,513 films name exactly
  one studio.

## What first-class would mean

These are the questions a design session must answer. None is answered
here.

- **Identity.** TMDb gives each company and each network an id. Where
  the id is stored: in the `.nfo` file beside the name, in a file of
  the library's own under `.liken/`, or only in the catalog. A title
  enriched from TVmaze alone has a network with a TVmaze id, and the
  two ids would need a join.
- **Where the record lives.** A `studios` table in the catalog, filled
  by a fact, or files beside the media in the open formats that the
  project keeps to, which the scanner reads into a table the way it
  reads a franchise file. The second keeps the record with the library
  when the catalog is rebuilt.
- **Renames and related names.** TMDb records a company's parent
  company and, for some, its earlier names. Whether a rename joins two
  names into one page, and whether a parent company's page also lists
  its children's titles, as Searchlight under Disney would.
- **Case and spelling.** Whether names that differ only in case join
  without a provider's id.
- **Logos.** TMDb serves company and network logos. Whether Fanart.tv
  serves any for a studio or a network is not checked. The terms of
  each for a cached copy on the volume must be read, as `fanart.go` and
  `tmdbart.go` did for title art. The logo's format and where its file
  lives are open.
- **Networks and studios.** Whether a series' network is the same kind
  of record as a film's production company, or a different one with a
  page of its own. The foot of a series page names both today.
- **The home page.** A studio strip on the home page was asked for on
  2026-11-09: the day's draw picks a studio at random, as it picks a
  genre, a person, and a set. That strip does not need the rest of
  this plan, because a studio matches by its exact name today, and on
  a home library that day 30 names had 8 or more titles. What weight a
  studio carries in the draw is open.
- **History.** Where the founding date, the renames, and a short
  description come from, and whether a person can correct them.

## What is not decided

The storage, the provider of the ids, the join rule for renames and
spellings, the source and the file of a logo, whether networks are
studios, and the weight of a studio in the home page's draw. This stub
chooses none of them.
