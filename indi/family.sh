#!/bin/sh
# Builds the tree of one image that builds on the indi image: the
# closure of the image's list, less every file of the indi tree, with a
# loader cache for the two trees together.
#
# A container of this image runs indiserver and socat from the indi
# layer and a driver from this layer, and the loader reads one cache.
# So the cache here names the libraries of both trees, and the image's
# cache replaces the base's.
#
# Usage: family.sh <list name>
set -eu

sh /indi-closure.sh /out "/images/$1" /base
mkdir -p /combined
cp -a /base/. /combined/
cp -a /out/. /combined/
ldconfig -r /combined
mkdir -p /out/etc
cp /combined/etc/ld.so.cache /out/etc/ld.so.cache
