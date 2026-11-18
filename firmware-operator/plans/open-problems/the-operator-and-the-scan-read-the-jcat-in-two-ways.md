# The operator and the scan read the jcat in two ways

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two functions read the first item of a catalog's jcat: `readJcat` in
`jcat.go` for the operator, and `parseJcat` in `scan/jcat.go` for the
scan, rehearsal, and apply pods. The two programs are separate `main`
packages, so each has its own copy, and the copies differ:

| | `readJcat` | `parseJcat` |
|---|---|---|
| SHA-256 blob | the first one that is 64 hex digits, in lowercase | the last one of kind 1, as written |
| `Id` | refuses an empty name, `.`, `..`, and a name with `/`, `\`, `?`, or `#` | no check |
| decompressed size | at most `maxJcatSize`, 1 MiB | no limit |

The first row can fail a scan. The operator records the lowercase
SHA-256 in the `FirmwareCatalog` status and passes it to the pod in
`CATALOG_SHA256`. `loadCatalog` in `scan/scan.go` then compares the
SHA-256 of the downloaded file, which `sum` writes in lowercase hex,
with the jcat's blob as written. A jcat with an uppercase SHA-256,
which `TestReadJcat` in `catalogfile_test.go` accepts as "an uppercase
checksum", makes every scan `Failed` with "the SHA-256 of F is X, and
the operator expects X", where both values are equal. A jcat with two
SHA-256 blobs makes the two programs compare different blobs.

The `Id` check matters less in the pod, because `loadCatalog` returns
`CatalogMoved` unless the `Id` equals the file that the operator
checked.

## Evidence

- `readJcat` in `jcat.go`, and `parseJcat` in `scan/jcat.go`.
- The comparison `got != cfg.sha256 || (item.sha256 != "" && got !=
  item.sha256)` in `loadCatalog` in `scan/scan.go`, and `sum` in the
  same file.
- The "an uppercase checksum" case of `TestReadJcat`. `scan/jcat_test.go`
  has no such case.

## What would confirm it

A test of `loadCatalog` with the rig of `scan/rig_test.go`, whose
server serves a jcat whose SHA-256 blob is in uppercase. The test checks
that the result is not `Failed`.

## What a fix has to decide

- Whether `parseJcat` follows `readJcat`, or whether one reader moves
  to a package that both programs import, with one set of tests.
- Which blob wins when a jcat has more than one SHA-256 blob.
