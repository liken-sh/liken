#!/usr/bin/env bash
# Checks the per-node-csi-driver image before it ships.
#
# Usage: per-node-csi-driver.sh <image>
#
#   <image>  the full reference of the per-node-csi-driver image,
#            tagged with its version, for example
#            ghcr.io/liken-sh/per-node-csi-driver:2026.10.02-001. The
#            image must be in the local Docker daemon. The check reads
#            the version from the tag and requires the binary to report
#            it, so a reference by digest or by :latest fails.
set -euo pipefail
export LC_ALL=C

image=$1

"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE
version=${image##*:}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

container=$(docker create "$image")
docker export "$container" | tar -t | sed 's|^|/|; s|/$||' | sort -u > "$work/image-files.txt"
docker rm "$container" > /dev/null

grep -qx /per-node-csi-driver "$work/image-files.txt" \
  || { echo "the image has no /per-node-csi-driver"; exit 1; }

# The image is the binary on scratch, so a shell or a library directory
# means the build copied in more. The export also lists paths that the
# container runtime adds, so the check lists the paths that must be
# absent and does not compare the whole list.
for path in /bin /sbin /lib /lib64 /usr/bin /usr/sbin /usr/lib
do
  if grep -qx "$path" "$work/image-files.txt"; then
    echo "the image has $path, but it should hold only /per-node-csi-driver"
    exit 1
  fi
done

reported=$(docker run --rm "$image" --version)
grep -Fq "$version" <<< "$reported" \
  || { echo "the image reports '$reported', which does not name $version"; exit 1; }
