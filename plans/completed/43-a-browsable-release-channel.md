# A browsable release channel

Milestone 43 — Completed. The release channel has an index: a front
page that lists every published release, and a page for each release
that names its artifacts, its digests, and the catalog entry that
adopts it.

The channel at [releases.liken.sh](https://releases.liken.sh/) held 19
releases and 196 objects, and a person could reach none of them
without being told the path first. The front page described the
layout, so a reader who knew the version grammar could guess a URL. A
reader who wanted to know which versions existed had nowhere to look
but the channel document, which names one version and says nothing
about the rest. The rollback path was the case that hurt most: an
operator who needed the release before the current one had to already
know its name.

## Object storage will not do this for us

The bucket cannot generate a listing, and this is not a setting that
anyone forgot to turn on.

The bucket's own ACL is private, and CI uploads each object as
public-read. A named path downloads anonymously, and the root refuses
to say what exists. This is deliberate (`liken.sh/terraform.tf`). The
name `releases.liken.sh` is a CNAME to the bucket's website hostname
class, and that class answers `GET /` with the bucket's index
document. The plain S3 class treats `GET /` as a listing request, and
this bucket's ACL refuses it. Neither class builds a page. A listing
from the S3 class would be XML in any case, and the CNAME does not
point there.

The lab measured what the website class does today. Every path that
is not an object returns the front page with status 403, because a
private bucket cannot report a missing key without telling an
anonymous reader which keys exist. So `/2026.07.25-002/` and
`/sources/` and `/nope.txt` all answer the same way.

The lab also measured the one behaviour this milestone depends on. A
probe object at `_indexprobe/index.html` answered at all three of
`/_indexprobe/index.html`, `/_indexprobe/`, and `/_indexprobe`. The
website class applies its index document to every prefix, not only to
the root. So an `index.html` inside a release's directory is enough
to make `https://releases.liken.sh/2026.07.25-002/` a page.

## The pages are a view, not a publication

The channel already holds everything a page must show.
`<version>/release.yaml` names every artifact with its sha256 and its
size, and it records the version of each component inside. The
version list is the set of top-level prefixes. So no new fact has to
be produced, recorded, or kept in step. Each page is a render of a
document that the channel already serves.

The index tree is therefore derived. No digest names a page. No
machine reads one. The upgrade path reads `channel.yaml` and
`release.yaml` and nothing else, and it verifies both against a digest
that the Cluster document pins. If every page were deleted, the
channel would lose nothing but its readability, and one command would
rebuild them all.

That property is what pays for the backfill. The 19 published
releases get their pages from the same command that a release
publishes with, because the command reads the channel, not the build
tree. There is one code path, it is idempotent, and a rerun heals a
page that is wrong or missing.

## `liken index`

The renderer lives in the `releases` package, beside `bundle`,
`fetch`, and `serve`, and the CLI carries a fourth channel command.
These are already the commands that operate a channel rather than a
machine, so this is the shelf it belongs on. The command ships to
operators for the same reason `serve` does: a deployment that runs its
own channel, on a private network or with no internet at all, gets the
same pages the public one has.

The command takes the list of the channel's keys on standard input,
one key per line, and the channel's base URL as a flag:

    liken index -source https://releases.liken.sh <out-dir> < keys

It writes a tree of pages into the output directory and nothing else.
It opens no bucket, holds no credential, and signs no request.

The seam is the point. Listing the bucket needs the key that only CI
holds, and CI holds it already. Rendering needs no credential at all.
Splitting them at the key list keeps the S3 protocol out of the
toolkit that ships to users, and it makes the renderer a pure
function that a unit test drives from a fixture. The workflow
supplies the list:

    s3cmd ls --recursive s3://releases.liken.sh/ \
      | awk '{print $4}' | sed 's|^s3://releases.liken.sh/||'

The command fetches each `<version>/release.yaml` over the public
URL, which is also a check: a release that the public channel does
not serve gets no page.

## What each page says

The front page keeps the prose it has and gains a list of every
version, newest first, with the version that `channel.yaml` names
marked as the latest. It lists them all. If that list ever grows too
long to read, the correction is to group it by month, never to
truncate it. A channel that hides its old releases makes a rollback
undiscoverable, which is the problem this milestone exists to fix.

A release's page gives, in this order:

* the catalog entry to paste into a Cluster's `spec.releases.catalog`,
  which is the version and the sha256 of the release document. This
  is first because it is the reason to open the page. Before this, an
  operator had to download `release.yaml` and run `sha256sum` on it,
  or find the GitHub release that prints it. The channel answers
  without GitHub now.
* each artifact, with its size, its sha256, and a link to it.
* the components and their upstream versions, which is how a reader
  learns which kernel and which k3s a release carries.
* links to `release.yaml`, to `LICENSES.md`, and to the sources page.

The channel serves `.yaml` and `.md` as `text/plain` already, so
those links open in a browser instead of downloading.

## The versions document

`versions.yaml` is the front page's machine-readable twin: every
release the channel holds, newest first, each with the digest that
adopts it. The same index run writes both, from the same data.

It is a separate document from `channel.yaml` for one reason. Every
cluster in the fleet fetches the channel document on a poll, and that
document has to stay one small, fixed size however many releases have
ever been published. The channel document answers "is there anything
newer", and the versions document answers "what else is here".

Each entry is written in the shape of a catalog entry, `version` above
`digest`, so it copies straight into a Cluster. The document is
written out rather than marshalled for exactly that reason: a
marshaller sorts the keys and would put the digest above the version
it belongs to.

## One stylesheet for the site and the channel

liken.sh and the channel share `brand/liken.css`: the colors, the
type, and the elements that prose and reference tables need. Both
carry a light and a dark scheme now, chosen by the reader's system
setting, and links take the mark's darkest green on a light page and
its palest on a dark one. Each page also carries the mark itself,
inline, beside its title.

Neither site links the stylesheet over the network, and the channel is
why. The channel lives in object storage, apart from any cluster,
because machines upgrade themselves from it and it has to answer when
the cluster does not. A stylesheet fetched from liken.sh would put the
website back in that path, and the pages would arrive unstyled at
exactly the moment an operator is reading them to find a release to
roll back to. So each site inlines a copy: the website's Makefile
copies the file, and the channel's pages read it through the `brand`
Go package, because a Go program cannot embed a file outside its own
directory.

## The sources index

`/sources/` gets a page too, and the licensing reason makes it more
than a convenience. The GPL and LGPL components in a release require
the channel to offer their corresponding source from the same place
it offers the binaries. The mirror holds 23 files under
`sources/<component>/<version>/`, and an offer that nobody can browse
is an offer that a reader must take on trust. One page, grouped by
component and version, with a direct link to each file, makes the
offer something a person can check. A channel with no mirror gets no
page, because an empty page under a license obligation reads as an
offer with nothing behind it.

One page is enough at this size. The deeper prefixes,
`/sources/kernel/` and below, would each need their own `index.html`
to answer, and that can wait until the mirror is large enough to need
it.

## The error document stays the front page

A missing path returns the front page with status 403, and this
milestone leaves that alone. A dedicated 404 page would still carry
status 403, because the private bucket ACL produces that code, so it
would buy a better sentence and nothing else. The front page is now a
list of every release, so a mistyped version lands on the answer.

## Where it runs

The release workflow has one step after it publishes. The order
matters and it follows the order that already exists there: the
artifacts go up, then `release.yaml`, then `channel.yaml`, and then
the pages. The release exists as soon as its document lands. The
pages are discovery, not the contract, so they come last, and a run
that fails after the document lands leaves a complete release with a
stale index.

The step regenerates and uploads the whole tree each time, about 21
small objects. There is no comparison of what changed, because at
this size the comparison would cost more to read than the upload
costs to run.

The backfill was the same command, run once from a workstation with
the same key.

## What the lab measured

The behaviour the whole design rests on was measured before any of it
was written. A probe object at `_indexprobe/index.html` answered at
`/_indexprobe/index.html`, at `/_indexprobe/`, and at `/_indexprobe`,
all with status 200. So the website hostname class applies its index
document to every prefix. The same probe showed no redirect between
the two directory forms, which is why every link on a page is
root-absolute: a relative link would resolve against the channel's
root for a reader who left the trailing slash off. Every path that is
not an object answered 403, never 404, on this private-ACL bucket.

The backfill ran against the live channel. The key list held 196
objects, the renderer read 19 release documents from the public URL,
and it wrote 21 pages. The digests it printed for the newest release
and for the oldest matched `sha256sum` over the documents that the
channel serves. After the upload, `/`, `/sources/`, and each release's
directory answered with their own page, in both the slashed and the
slashless form. A missing path still lands on the front page.
`grub-core.img` still downloaded its 151,950 bytes from a link on a
page.

The machine path did not move. `channel.yaml` still named
2026.07.25-002, and that release's document still hashed to the digest
the fleet has pinned. The UEFI smoke drill installed node-1 onto a
blank disk, booted it, and reported Ready after 15 seconds.

The shared stylesheet reaches both sites. The built site carries the
scheme and the greens in every page, and so does every page on the
channel. One defect turned up while drilling this: the cli Makefile
listed the scaffold's templates among its inputs but not the release
pages' templates, so an edit to a page did not rebuild the toolkit.
The rule now names the templates, the stylesheet, and the mark.

Unit tests cover the rest: two renders of the same channel produce the
same bytes, a key that is not a release becomes no page, a version the
channel does not serve fails the run and is named in the error, a
channel with no source mirror gets no sources page, and the versions
document lists every release newest first with the right digests.

## The manual

Two pages changed. The CLI reference carries `liken index`
(`docs/content/docs/reference/cli.md`). The release channel reference
carries the index pages and the versions document in its layout, and
says that both are derived and that the machine path does not read
them (`docs/content/docs/reference/release-channel.md`).
