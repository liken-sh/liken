# A "scan now" trigger for a Library

`kubectl liken library reenrich` works: it writes `spec.refresh` on the
Library, and the operator's enricher watches that field. A full rescan
has no such field. The only triggers for a walk are the CronJob the
operator stands from `spec.scan.schedule` and the in-cluster webhook.
Neither one is a signal a person can send by editing the Library, so
the `rescan` CLI verb is a stub.

## What a fix needs

The operator's reconcile path needs to watch one field that means "walk
this Library once, now", and launch a one-off scan Job when it changes.
`spec.refresh` is the model to copy: a timestamp the person sets, that
the operator compares against the last walk. The CLI verb then writes
that field, the same shape as `reenrich`.

## The fact vocabulary drift

`reenrich --only <fact>` validates against a 25-fact list. The list is
copied byte-for-byte into `library-operator/cli/facts.go`, because the
CLI image builds from `cli/` alone and cannot import the operator
package. Nothing fails when the two lists drift. A fix moves the
vocabulary into a package both the operator and the CLI import, or adds
a test that reads both and reports a difference.
