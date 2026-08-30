# The documentation site

Plan 10. The operator's site, on its own subdomain like the other
operators', written after plan 09 has proved the design, so the site
documents what runs.

## The problem

Every `liken` operator has a site: a manual, the generated reference
pages for its resources, and the release notes. Hugo builds it from
`docs/` on the brand theme, and GitHub Pages serves it on a subdomain of
`liken.sh`. A site written before the drill would describe contracts the
build may still change. Plan 01 lays the scaffold so the reference pages
generate from the CRD schemas from the start. This plan writes the site.

## The contract

- The manual: what a `Library` is, how to bind one to a volume, what the
  scanners read, how the catalog reaches screens, how to put the media
  browser on a `Player`, and what to expect on a one-gigabyte box, with
  the numbers from plan 09.
- The reference: generated from the `Library` schema and, as they land,
  from every later resource, with the shared generator the brand
  repository provides and the link checker the other sites run.
- The pages for what a person runs beside the operator: the webhooks to
  configure in Radarr, Sonarr, and Jellyfin, and the Jellyfin setting
  that keeps the sidecars written.
- The subdomain, its DNS, and its Pages configuration, done as the other
  operators' were.
- Prose in the voice the brand's `voice.md` sets, for a reader who has
  never seen this repository. No repository idiom on a site page.

## Proof

The site builds in CI, every link resolves, the reference matches the
released schema, and it serves on its subdomain from a tagged release.