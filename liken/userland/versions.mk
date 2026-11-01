# The program versions that differ from the ones that the pinned
# buildroot commit names.
#
# Root plan 83 has every pin follow the newest upstream release, and
# buildroot's master branch sometimes packages a release later than
# upstream publishes it. Each line below sets a newer version for one
# buildroot package. buildroot reads this file before its package
# recipes, and `override` makes the recipe's own assignment of the
# same variable a no-op. The tarball of each version here has a .hash
# file at patches/<package>/<version>/<package>.hash, and the build
# stops on a download that no .hash file lists.
#
# latest.sh --bump <program> writes a line and its .hash file, and
# latest.sh --bump deletes each line that the new buildroot commit
# meets or passes.
override TZDATA_VERSION = 2026f
override ZIC_VERSION = 2026f
