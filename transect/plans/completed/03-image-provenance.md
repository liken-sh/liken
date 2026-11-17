# 03: Image provenance

A card links to the directory its manifests are declared in, from
Flux.  This plan adds the other half from the design's "Code" section:
the repository and commit that each running image was built from.

## The labels

Every image the cluster builds carries two OCI labels:

- `org.opencontainers.image.source`: the repository's URL
- `org.opencontainers.image.revision`: the commit the image was built
  from

Most public images already carry both.  First-party images published
to public registries carry them from every build.  Images built for
the cluster's own private registry do not yet; their builds take the
same labels the same way the public builds do, and until then the
finding for images with no source label lists them by registry.

Three more of the standard labels are stored text transect can show
without breaking the deterministic runtime:

- `org.opencontainers.image.description`: a line of what the image
  is, for a card that has no `<prefix>/why` annotation
- `org.opencontainers.image.documentation`: where to read more
- `org.opencontainers.image.version`: the release, beside the commit

## Where transect reads them

A label lives in the image's config blob in the registry, not in the
API.  The API gives the digest each container runs, in
`status.containerStatuses[].imageID`.  A digest names immutable
content, so its labels can be fetched once and cached for as long as
transect runs.

transect reads a registry's manifest and config for each digest it
has not seen.  Public registries answer anonymously.  A private
registry needs a credential, and transect never reads Secrets, so the
credential is given to transect itself (a flag or a mounted file),
never borrowed from a pod's `imagePullSecrets`.

## What it shows

- The container block on the pod page and the workload card link to
  the commit: `<source>/commit/<revision>` on forges whose URL shape
  is known, the URL and revision as text otherwise.
- Beside the Flux link, the card can say when the declared directory
  and the running commit come from the same repository, and how far
  apart they are when the forge's API answers.
- A finding counts the running images with no source label, so a gap
  in the builds shows on the map.

## Decisions

- transect reads labels from each registry's API directly, over the
  OCI distribution API, not from a vulnerability scanner's reports.
  It fetches the manifest by the digest in
  `status.containerStatuses[].imageID`, with any runtime prefix such as
  `docker-pullable://` stripped.  When that manifest is an index, it
  picks the manifest for the platform of the pod's machine, from the
  Node's `nodeInfo`, or `linux/amd64` when the Node reports none.  The
  labels are `config.Labels` in the config blob that manifest names.
- Every request goes out anonymously first, answering the registry's
  `WWW-Authenticate` challenge with an anonymous bearer token from its
  realm.  `--registry-auth <path>` names a Docker `config.json`, and
  transect sends a registry the credential from its `auths` entry only
  when the registry refuses the anonymous request.  transect reads only
  `auths` entries with an `auth` field; it ignores `credsStore` and
  `credHelpers`, which run programs, and says so when it starts.  It
  never reads a pod's `imagePullSecrets`.
- A digest's labels are kept for as long as transect runs, since a
  digest names immutable content.  The reads run in a background task,
  four at a time, with a 15-second timeout on each request, and a
  render never waits on one.  Each read that lands marks the cluster
  changed, so open pages redraw.
- A failed read is tried again after a wait that starts at a minute
  and doubles after each failure, up to an hour.  A failure of the
  whole registry, when it is unreachable, does not answer as a
  registry, or answers 429, holds back every image it serves for a
  wait that starts at five minutes, and the map shows one line for the
  registry, not one per image.
- A registry that serves blobs by a redirect to object storage is
  followed there, and the HTTP client drops the Authorization header
  on any redirect to another host.
- Label text is untrusted: each label is cut to a length a card can
  hold and stripped of control characters, a documentation URL is kept
  only when it is plain `http` or `https`, and the templates escape
  every value.
- The commit link reads the same forge hosts and clone URL shapes as
  the Flux directory link, so both live in the `forge` module.
