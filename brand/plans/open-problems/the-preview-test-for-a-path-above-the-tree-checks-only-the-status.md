# The preview test for a path above the tree checks only the status

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`TestPathAboveTheTreeGets404WithTheSitesNotFoundPage` in
`brand/preview/main_test.go` writes `outside.html` in the directory
above the site's tree and requests `/../outside.html`. Its name says
that the request gets the site's `404.html`. The test checks only that
the status is 404. It does not read the body.

So the test passes when `handler` in `brand/preview/main.go` answers
the path with Go's plain-text 404 in place of the site's page, and the
part of the behavior that its name states is not tested.
`TestMissingPageGets404WithTheSitesNotFoundPage`, above it, checks
the body for the `<title>Not found</title>` of the site's page.

The test also does not check that the body is not the outside file.
A 404 status with the outside file's text would be a defect too.

## What would confirm it

A change to `handler` that answers every missing path with Go's
`http.NotFound` in place of `notFound`, run against the test. If the
test still passes, it does not test what its name says.

## What a fix has to decide

Whether the test checks the body for the site's not-found page, as
the test of a missing page does, or whether the name changes to state
only the status. The first keeps the name's claim true and also
proves that the outside file's text is not served.
