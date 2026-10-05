#!/usr/bin/env bash
# Proves the per-node-csi-driver image before it ships.
#
# Usage: per-node-csi-driver.sh <image>
#
#   <image>  the full reference of the per-node-csi-driver image,
#            tagged with its version, for example
#            ghcr.io/liken-sh/per-node-csi-driver:2026.10.02-001. The
#            image must be in the local Docker daemon. The check reads
#            the version from the tag and requires the binary to report
#            it, so a reference by digest or by :latest fails.
#
# The image is the binary alone on scratch, so the whole proof is that
# the one file is there and that it reports the version its tag names.
set -euo pipefail
export LC_ALL=C

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE
version=${image##*:}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Every path in the image, as a process in it sees them.
container=$(docker create "$image")
docker export "$container" | tar -t | sed 's|^|/|; s|/$||' | sort -u > "$work/image-files.txt"
docker rm "$container" > /dev/null

grep -qx /usr/local/bin/per-node-csi-driver "$work/image-files.txt" \
  || { echo "the image has no /usr/local/bin/per-node-csi-driver"; exit 1; }

# The driver runs no other program, so a shell or a library directory
# in the image means the build pulled something in. The export also
# lists paths the runtime injects, so this names what must not be there
# instead of counting what is.
for path in /bin /sbin /lib /lib64 /usr/bin /usr/sbin /usr/lib
do
  if grep -qx "$path" "$work/image-files.txt"; then
    echo "the image has $path, and it runs no program but its own"
    exit 1
  fi
done

reported=$(docker run --rm "$image" --version)
grep -Fq "$version" <<< "$reported" \
  || { echo "the image reports '$reported', which does not name $version"; exit 1; }
