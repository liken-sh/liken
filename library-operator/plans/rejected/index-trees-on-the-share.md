# Index trees on the share

An early shape of the design had an `Index` resource: a grouping over
a library, by decade or set or director, materialized by the operator
as a tree of relative symlinks under a dot folder in the share, so
other programs could read the grouping with `ls`. It was set aside
before a build.

The browser never needs the tree, because the catalog answers any
grouping with a query. So an operator-written tree serves only
programs that read the share, and today that set is Jellyfin, which
would have to skip the tree with an ignore file or list every film
twice, and a person with a shell. That is a loop, writes into the
share, and a hazard, for a view nobody asked for.

The other direction stays. A folder of symlinks a person makes by hand
is the plainest way to say "these belong together", and the scanner
can read it as an attribute so the catalog groups by it. The
filesystem is where a person expresses intent, and the catalog is
where queries run. Plan 13 carries that.
