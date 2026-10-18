# 80, TheIntroDB API v4

This plan, written on 2026-10-18, moves the marks fact's asks to
TheIntroDB from `/v3/media` to `/v4/media`. It asks for a series by
its TheTVDB id where the catalog holds one, and it paces a keyed
account at the faster rate that v4 gives a key. Built on 2026-10-18.
The drill on `liken-1` has not run.

## The problem

TheIntroDB released API v4 on 2026-10-17. It removes v2 and v3 on
2027-01-18 at 00:00 GMT, and the marks fact asks `/v3/media`, so the
fact stops finding spans on that date.

v4 also changes how TheIntroDB stores a work. A v3 answer was keyed on
the TMDb id. A v4 answer is keyed on the TheTVDB id, and every span is
stored against an episode in TheTVDB's default order. TheIntroDB
converted all of its submissions to that order when it released v4.

v4 reads the season and the episode of a request in the numbering of
the id that the request names:

* With `tmdb_id`, it reads TMDb's numbering.
* With `imdb_id`, it reads IMDb's numbering.
* With `tvdb_id`, it reads the TheTVDB order that `mapping` names. When
  `mapping` is omitted, it reads TheTVDB's default order.

The fact asks with `tmdb_id` and the season and episode that the
catalog holds. The catalog reads those numbers from the episode's
`.nfo` file or from the `s01e02` marker in the file's name. Sonarr
names files in TheTVDB's order and writes the same numbers into its
`.nfo` files. For most series, TMDb's numbering and TheTVDB's default
order are the same. For a series where they differ, such as an anime
series or a series that TMDb splits into different seasons, the fact
asks TheIntroDB about a different episode than the one in the file.

v4 also raises the limits:

| Limit | v3 | v4 |
|---|---|---|
| Asks a day with no key, for each address | 500 | 5,000 |
| Asks a day with a key, for the account | 1,000 | 100,000 |
| Asks for one title a day | none stated | 2,000 |
| Asks every 10 seconds with no key | 30 | 30 |
| Asks every 10 seconds with a key | 30 | 120 |

With v3 and a key, a library of 10,000 episodes took ten days to ask
about once. With v4 and a key, the 10-second window limits the fact
before the daily allowance does.

## The design

### The path and the answer

The fact asks `/v4/media`. The answer holds the same four lists,
`intro`, `recap`, `credits`, and `preview`, and each span still holds
`start_ms` and `end_ms` with `null` for an open end. The answer names
`tvdb_id` where v3 named `tmdb_id`, and the fact reads neither.

The check still reads `/health` at the root. It spends no allowance.

### The id an ask names

An episode's numbers match TheTVDB's default order more often than
TMDb's, because the files and their `.nfo` files usually come from
Sonarr. So the fact names the first id that the work holds,
in this order:

* An episode: `tvdb_id`, then `tmdb_id`, then `imdb_id`. Each is the
  series' id, with the season and the episode the catalog holds.
* A movie: `tmdb_id`, then `tvdb_id`, then `imdb_id`. A movie has no
  numbering, so the order only follows how often each id is present.

The fact sends no `mapping`, so TheIntroDB reads a `tvdb_id` request in
the default order. A work with none of the three ids gets no ask.

v4 also accepts `mapping`, `episode_name`, and `tvdb_episode_id`. The
catalog does not record which TheTVDB order a library uses, so the fact
cannot name a `mapping` value with any confidence. A wrong value
returns 400, or the spans of a different episode. The fact sends none
of the three.

### The pace

The table paces TheIntroDB at one ask every 350 milliseconds, under 30
asks every 10 seconds. A client whose account names a key paces at one
ask every 90 milliseconds, under 120 every 10 seconds. The cooldown rule
for a 429 does not change. Every v4 429 carries `Retry-After`, and the
rule reads that header first. A 429 for a spent daily allowance or a
spent title allowance names a reset longer than a minute, so the ask
fails and the file waits for a later run.

### The pages

The `theintrodb` field description in the CRD, the enrichment guide,
and the generated reference and skill state v4's allowances and the
order of ids.

## Considered and set aside

**A `mapping` field on the `Library`.** A person could state the
TheTVDB order of each series library, such as `dvd` or `absolute`, and
the fact would send it with `tvdb_id`. Most libraries use the default
order, and no library has shown a wrong span from another order yet.
The field can be added when one does.

**Keep `tmdb_id` first for episodes.** This makes the fewest changes,
and v4 accepts it. But TMDb's numbering is the order that the files
match less often, and every span is now stored in TheTVDB's order, so
each ask by TMDb numbers depends on TheIntroDB's conversion.

## Proof

The tests run each ask against a fake TheIntroDB in a `synctest`
bubble:

* An episode with a TheTVDB id is asked at `/v4/media` with `tvdb_id`,
  its two numbers, and its length, and every candidate comes back.
* An episode with only a TMDb id is asked with `tmdb_id`, and one with
  only an IMDb id is asked with `imdb_id`.
* A movie with a TMDb id and a TheTVDB id is asked with `tmdb_id`.
* A work with no id gets no ask.
* A keyed client sends its asks 90 milliseconds apart, and an anonymous
  client sends its asks 350 milliseconds apart.

A probe of the live API on 2026-10-18 asked
`/v4/media?tmdb_id=1396&season=1&episode=1` with no key. It answered
200 with `tvdb_id`, `season`, `episode`, `intro`, and `credits`, and
the headers `X-RateLimit-Limit: 30`, `X-UsageLimit-Limit: 5000`, and
`X-UsageLimit-SpecificMedia-Limit: 2000`.

A drill on `liken-1` runs the marks fact over a series library and
checks that the files that held spans with v3 still hold spans.
