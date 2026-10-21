# Each release uploads the page of every release

Open problem. `releases/publish.sh` renders the channel's pages again
on each release and uploads every one of them, so the publish step
gets slower with each release the channel holds. Most of those pages
did not change. A page changes only when its own release is new, when
the index or `versions.yaml` gains a row, or when a template changes.

The full upload is deliberate. The comment above the upload says that
a rerun repairs a page instead of only adding one, and a template
change reaches every old release's page with no separate step. Any
fix must keep both properties.

## The measurement

The publish of `2026.10.21-002` on 2026-10-21 (run 39691258307, job
119537960271) spent 2 minutes 46 seconds in "publish to the channel":

| Part | Time | Grows with releases |
| --- | --- | --- |
| The release's own artifacts, most of it `liken.sqfs` at 448 MB | about 60 s | no |
| `s3cmd ls --recursive` of the bucket, and the render | about 14 s | yes |
| 91 `index.html` pages and `versions.yaml`, about 0.6 s each | about 54 s | yes |

At this rate, each new release adds about 0.6 seconds to every later
publish.

## A candidate

Render every page as today, then upload only the pages whose bytes
differ from the object in the bucket. `s3cmd sync` compares each
file's MD5 with the object's ETag, which is the MD5 for an object
that was not uploaded in parts, and every page is small enough for
one part. The `liken index` renderer then decides what changed, and
the upload decides nothing: a template change produces different
bytes on every page, so every page uploads, and a rerun after a failed
upload sends the pages that still differ. No list of template inputs
needs to be kept in step with the templates.

Two details are not settled:

- The loop sets the MIME type from each file's extension. `sync` takes
  one `--mime-type` for a run, so the pages need one `sync` for each
  type, filtered with `--exclude` and `--include`, or a check that
  `--guess-mime-type` gives `text/html; charset=utf-8`.
- The bucket listing also grows with the channel. It is one request
  for each 1,000 keys, so it is not yet a cost, but `sync` lists the
  same keys again. The renderer could read the listing that `sync`
  already makes.
