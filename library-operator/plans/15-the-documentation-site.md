# The documentation site

Plan 15. The operator's site, on its own subdomain like the other
operators', built after the first nine plans have proved the design,
so the site documents what is rather than what was hoped.

## The problem

Every `liken` operator has a site: a manual, the generated reference
pages for its resources, and the release notes, built with Hugo from
`docs/` on the brand theme and served from GitHub Pages on a
subdomain of `liken.sh`. This operator will have one, and a site
written before the drill in plan 09 would describe contracts that the
build may still change. Plan 01 lays the scaffold so the reference
pages generate from the CRD schemas from the start; this plan writes
the site.

## The shape

- The manual: what a `Library` is, how to bind one to a share, what
  the scanners read, how the catalog reaches screens, how to put the
  browser on a `Player`, and what to expect on a one-gigabyte box,
  with the numbers from plan 09.
- The reference: generated from the `Library` schema and, as they
  land, from every later resource, with the shared generator the
  brand repository provides and the link checker the other sites run.
- The pages for the pieces a person runs beside the operator: the
  webhooks to point Radarr, Sonarr, and Jellyfin at, and the
  Jellyfin setting that keeps the sidecars written.
- The subdomain, its DNS, and its Pages configuration, done the way
  the other operators' were.
- Prose in the voice the brand's `voice.md` sets, written for a
  reader who has never seen this repository: no repository idiom on a
  site page.

## Proof

The site builds in CI, every link resolves, the reference matches
the released schema, and it serves on its subdomain from a tagged
release.
