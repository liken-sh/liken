# The patches of a buildroot recipe

buildroot's recipe for a package has patches for the version that the
recipe names. buildroot also applies them to the newer version that
`userland/versions.mk` sets. When the new release already includes
the change of a patch, the patch does not apply, and the build stops
at the package's patch step.

1. Check each patch of the recipe against the source of the new
   version.
2. Write the file name of each patch that the release includes into
   `userland/patches/<package>/<version>/held`, with a comment line
   that says where the release includes it.
3. Report each patch that the bump drops.

`userland/buildroot.sh` stops buildroot from applying a held patch,
and still applies every other patch, as `zic` needs.
