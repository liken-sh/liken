# Index trees on the volume

An early shape of the design had an `Index` resource: a grouping over a
library, by decade or set or director. The operator would write it as a
tree of relative symlinks under a dot folder on the volume, so other
programs could read the grouping with `ls`. It was set aside before a
build.

The media browser never needs the tree, because a catalog query answers
any grouping. So an operator-written tree serves only programs that read
the volume. Today that is Jellyfin, which would list every film twice
unless it skipped the tree, and a person with a shell. That is a loop,
writes to the volume, and a hazard, for a view nobody asked for.

The other direction stays. A folder of symlinks a person makes by hand
is the plainest way to say "these belong together", and the scanner can
read it as an attribute so the catalog groups by it. The open problem on
what a screen shows uses that.
