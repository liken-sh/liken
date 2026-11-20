# The trust store

`trust`, at the top of the repository, pins the CA bundle that every
part of `liken` trusts. The OS build reads its `VERSION` through
`trust/fetch.sh`, and every image that verifies TLS copies the bundle
from the `trust` image. `make versions` reports it as `trust`.

Its `--bump` writes these:

- the new date, into `VERSION`
- the version of the image and its `[package.upstream]`, into
  `package.toml`
- the snapshot and its published checksum, into the `Dockerfile`

Then run `make workflows` at the top of the repository. Raise the
revision of each pinned component whose `[depends]` lists `trust`,
such as `indi`. A tracked component whose `[depends]` lists `trust`
is released with no other step.

A new snapshot can remove certificates from the bundle as well as add
them. Read what was removed before you take the bump.
