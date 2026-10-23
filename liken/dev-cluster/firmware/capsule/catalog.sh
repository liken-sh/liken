#!/usr/bin/env bash
#
# Build the lab's test firmware catalog: signed capsules that raise the
# test device in the capsule firmware (build.sh) to each version that
# VERSIONS names, 2 and 3 by default, an fwupd cabinet for each, and a
# catalog of the releases laid out the way the LVFS publishes its own,
# signed by a lab key.
#
# The catalog is what a FirmwareCatalog names in a drill of
# firmware-operator on a lab guest under FIRMWARE=capsule, where the
# test device starts at version 1.
#
# Three things are signed, each for its own reader:
#
#   the capsule   FmpDxe in the firmware checks its PKCS#7 signature
#                 against edk2's test root certificate, which the
#                 firmware embeds. GenerateCapsule signs it with the
#                 matching test key from the edk2 tree.
#   the catalog   fwupd refuses a catalog whose jcat holds no signature
#                 from a key in /etc/pki/fwupd-metadata. jcat-tool
#                 signs it with the lab key in signing/, and the lab's
#                 certificate goes into that directory beside the
#                 LVFS's own.
#   the cabinet   nothing signs it. The signed catalog gives each
#                 cabinet's SHA-256, which pins it.
#
# The lab key is made once and kept in signing/, which git ignores,
# the way dev-cluster/identity/ keeps the cluster's keys. A new key
# needs a new certificate on every cluster that trusts the old one, so
# the script never replaces it. Delete signing/ to start over.
#
# The capsule header sets OEM flag 0x0001. fwupd's EFI updater
# (fwupd-efi 1.7, fwupdate.c) adds PersistAcrossReset and InitiateReset
# to every capsule whose Flags field is zero, and the capsule firmware
# refuses PersistAcrossReset (build.sh gives the reason). A nonzero
# Flags field stops the updater, and edk2 ignores the OEM bits.
#
# Every step runs in the image that ./Dockerfile describes. The script
# starts that container itself and runs again inside it.
#
# Usage:
#   dev-cluster/firmware/capsule/catalog.sh
#
# BASE_URL is where the guests download the cabinets, written into the
# catalog. Its default is the directory that `make serve` serves the
# catalog under, as a guest reaches the laptop.
#
# VERSIONS lists the versions to build, separated by spaces. fwupd
# offers only a version above the one a guest runs, so a guest that
# already took version 3 needs a catalog with a higher one, such as
# VERSIONS="2 3 4 5".
#
# Results land in firmware/capsule/dist/catalog/:
#
#   firmware.xml.zst          the catalog, under the unversioned name
#                             that a FirmwareCatalog's spec.url names
#   firmware.xml.zst.jcat     its checksum and signature, naming the
#                             versioned file
#   firmware-<serial>-lab.xml.zst
#                             the same catalog under its versioned name,
#                             which changes with every build
#   lab-fmp-<version>.cab     the cabinet of each version
#
# The certificate that verifies the catalog is signing/lab-catalog.pem.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
base_url="${BASE_URL:-http://10.0.2.2:8017/firmware}"

if [ -z "${LIKEN_CAPSULE_BUILDER:-}" ]; then
    if [ ! -d "$here/.cache/edk2/BaseTools" ]; then
        echo "catalog.sh: $here/.cache/edk2 is missing; run build.sh first" >&2
        exit 1
    fi
    builder="$("$here/builder.sh")"
    exec docker run --rm --user "$(id -u):$(id -g)" \
        -e HOME=/tmp -e LIKEN_CAPSULE_BUILDER=1 -e LIKEN_CAPSULE_HOST="$here" \
        -e BASE_URL="$base_url" -e VERSIONS="${VERSIONS:-2 3}" \
        -v "$here:/capsule" -w /capsule "$builder" /capsule/catalog.sh
fi

# The test device's GUID, from LAB_FMP_GUID in ovmf-capsule.patch. It
# is FmpDxe's FILE_GUID, the FMP image type ID, and the firmware class
# that the ESRT lists, so a capsule, a cabinet, and a catalog all name
# the device by it.
guid="b0792605-c842-408f-adf2-edfb07638e83"
component="sh.liken.lab.fmp.firmware"
read -r -a versions <<<"${VERSIONS:-2 3}"

# FmpDxe records each capsule's lowest supported version after an
# update, and refuses a later capsule whose version is below it. 1
# keeps that version at 1, so this check refuses no lab capsule.
lsv=1

edk2="$here/.cache/edk2"
tools="$edk2/BaseTools/Source/Python"
test_keys="$tools/Pkcs7Sign"
signing="$here/signing"
work="$here/.cache/catalog"
out="$here/dist/catalog"

generate_capsule() {
    PYTHONPATH="$tools" python3 "$tools/Capsule/GenerateCapsule.py" "$@" \
        --signer-private-cert "$test_keys/TestCert.pem" \
        --other-public-cert "$test_keys/TestSub.pub.pem" \
        --trusted-public-cert "$test_keys/TestRoot.pub.pem"
}

# fwupd's PKCS#7 engine (libjcat) verifies the signer's certificate
# against the certificates in /etc/pki/fwupd-metadata. The lab key's
# certificate signs itself, so it is the signer and the trust anchor at
# once. Its extensions limit it to signatures, so the key cannot act as
# a certificate authority or serve TLS.
if [ ! -f "$signing/lab-catalog.key" ]; then
    mkdir -p "$signing"
    chmod 700 "$signing"
    openssl req -x509 -newkey rsa:3072 -nodes -days 3650 \
        -subj "/O=liken lab/CN=liken lab firmware catalog" \
        -addext "basicConstraints=critical,CA:FALSE" \
        -addext "keyUsage=critical,digitalSignature" \
        -addext "extendedKeyUsage=codeSigning" \
        -keyout "$signing/lab-catalog.key" -out "$signing/lab-catalog.pem" 2>/dev/null
    chmod 600 "$signing/lab-catalog.key"
    echo "made the lab catalog key in $LIKEN_CAPSULE_HOST/signing"
fi

rm -rf "$work" "$out"
mkdir -p "$work" "$out"
today="$(date -u +%F)"
timestamp="$(date -u +%s)"
releases=""

for version in "${versions[@]}"; do
    dir="$work/v$version"
    mkdir -p "$dir"
    printf 'liken lab test device firmware, version %s\n' "$version" >"$dir/payload.bin"

    generate_capsule --encode --guid "$guid" --fw-version "$version" --lsv "$lsv" \
        --capoemflag 0x0001 -o "$dir/firmware.cap" "$dir/payload.bin"
    # Decoding checks the signature against the test root, the same
    # check that FmpDxe makes, before the capsule goes anywhere.
    generate_capsule --decode -o "$dir/decoded" "$dir/firmware.cap" >/dev/null

    # fwupd finds the metainfo in a cabinet by its suffix,
    # .metainfo.xml, and refuses a cabinet without one. It reads the
    # device's GUID, the version, and how to install it from this file.
    cat >"$dir/firmware.metainfo.xml" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<component type="firmware">
  <id>$component</id>
  <name>liken lab test device</name>
  <summary>Firmware for the test device in the liken lab's capsule firmware</summary>
  <description>
    <p>The test device keeps its version in a UEFI variable. This capsule sets it to $version.</p>
  </description>
  <provides>
    <firmware type="flashed">$guid</firmware>
  </provides>
  <metadata_license>CC0-1.0</metadata_license>
  <project_license>BSD-2-Clause-Patent</project_license>
  <developer_name>liken</developer_name>
  <url type="homepage">https://liken.sh</url>
  <releases>
    <release version="$version" date="$today" urgency="low">
      <checksum filename="firmware.cap" target="content"/>
      <description>
        <p>Sets the test device to version $version.</p>
      </description>
    </release>
  </releases>
  <custom>
    <value key="LVFS::VersionFormat">number</value>
    <value key="LVFS::UpdateProtocol">org.uefi.capsule</value>
  </custom>
</component>
EOF

    cab="lab-fmp-$version.cab"
    (cd "$dir" && gcab --create --nopath "$out/$cab" firmware.cap firmware.metainfo.xml)

    # The catalog lists the newest release first, as the LVFS does. Each
    # release names its cabinet by an absolute URL and by checksums of
    # the cabinet (container) and of the capsule inside it (content).
    release="$(cat <<EOF
      <release version="$version" timestamp="$timestamp" urgency="low">
        <location>$base_url/$cab</location>
        <checksum type="sha1" filename="$cab" target="container">$(sha1sum "$out/$cab" | cut -d' ' -f1)</checksum>
        <checksum type="sha256" filename="$cab" target="container">$(sha256sum "$out/$cab" | cut -d' ' -f1)</checksum>
        <checksum type="sha256" filename="firmware.cap" target="content">$(sha256sum "$dir/firmware.cap" | cut -d' ' -f1)</checksum>
        <description>
          <p>Sets the test device to version $version.</p>
        </description>
        <size type="installed">$(stat -c %s "$dir/firmware.cap")</size>
        <size type="download">$(stat -c %s "$out/$cab")</size>
      </release>
EOF
)"
    releases="$release${releases:+$'\n'$releases}"
done

cat >"$work/firmware.xml" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<components origin="liken-lab" version="0.9">
  <component type="firmware">
    <id>$component</id>
    <name>liken lab test device</name>
    <summary>Firmware for the test device in the liken lab's capsule firmware</summary>
    <provides>
      <firmware type="flashed">$guid</firmware>
    </provides>
    <metadata_license>CC0-1.0</metadata_license>
    <project_license>BSD-2-Clause-Patent</project_license>
    <developer_name>liken</developer_name>
    <url type="homepage">https://liken.sh</url>
    <releases>
$releases
    </releases>
    <custom>
      <value key="LVFS::VersionFormat">number</value>
      <value key="LVFS::UpdateProtocol">org.uefi.capsule</value>
    </custom>
  </component>
</components>
EOF

# The LVFS publishes its catalog under a versioned name and under the
# unversioned one, and the jcat beside the unversioned name gives the
# versioned name. firmware-operator reads that name to see that the
# catalog changed, so each build takes a new serial, the time in UTC.
versioned="firmware-$(date -u +%Y%m%d%H%M%S)-lab.xml.zst"
zstd -q -19 "$work/firmware.xml" -o "$out/$versioned"
cp "$out/$versioned" "$out/firmware.xml.zst"

# The jcat holds one item, named for the versioned file, with two
# blobs: its SHA-256, and a PKCS#7 signature by the lab key. fwupd
# needs both: it verifies with JCAT_VERIFY_FLAG_REQUIRE_CHECKSUM and
# JCAT_VERIFY_FLAG_REQUIRE_SIGNATURE.
jcat="$out/firmware.xml.zst.jcat"
(
    cd "$out"
    jcat-tool --basename --kind sha256 self-sign "$jcat" "$versioned" >/dev/null
    jcat-tool --basename sign "$jcat" "$versioned" \
        "$signing/lab-catalog.pem" "$signing/lab-catalog.key" >/dev/null
    jcat-tool --public-key "$signing/lab-catalog.pem" verify "$jcat" >/dev/null
)

# The messages name the paths on the laptop, not in the container.
host="$LIKEN_CAPSULE_HOST"
echo "catalog in $host/dist/catalog, naming $versioned"
echo "signer certificate: $host/signing/lab-catalog.pem"
